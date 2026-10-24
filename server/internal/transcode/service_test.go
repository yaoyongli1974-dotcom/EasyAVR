package transcode

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
	"github.com/easyavr/easyavr/internal/video"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "transcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.TranscodeJob{}, &model.Channel{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedJob(t *testing.T, db *gorm.DB) model.TranscodeJob {
	t.Helper()
	dev := model.Device{Name: "cam", Protocol: "rtsp"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	ch := model.Channel{DeviceID: dev.ID, Name: "cam1", StreamKey: "ch_abc123def45678"}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	job := model.TranscodeJob{
		ChannelID: ch.ID, Name: "demo", Mode: "transcode",
		StreamKey: ch.StreamKey + "_t", Bitrate: 2000, Enabled: true,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func findVF(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "-vf" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestBuildArgsTranscode(t *testing.T) {
	job := model.TranscodeJob{Mode: "transcode", Width: 1280, Height: 720, Bitrate: 1500}
	args := BuildArgs(job, "rtsp://in", "rtsp://out")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-rtsp_transport tcp -i rtsp://in",
		"-c:v libx264 -preset veryfast -b:v 1500k",
		"-pix_fmt yuv420p",
		"scale=1280:720",
		"-c:a aac",
		"-f rtsp -rtsp_transport tcp rtsp://out",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "drawtext") {
		t.Errorf("transcode mode must not contain drawtext: %s", joined)
	}
}

func TestBuildArgsOsdEscaped(t *testing.T) {
	job := model.TranscodeJob{
		Mode: "osd", Text: `台'标A:x,y%1+2`, Position: "bottom-right", FontSize: 32,
	}
	args := BuildArgs(job, "in", "out")
	vf := findVF(t, args)
	if !strings.Contains(vf, "drawtext") {
		t.Fatalf("osd mode missing drawtext filter, args: %v", args)
	}
	if !strings.Contains(vf, `:fontsize=32:`) {
		t.Errorf("fontsize not honored: %s", vf)
	}
	if !strings.Contains(vf, "x=w-tw-40") || !strings.Contains(vf, "y=h-th-40") {
		t.Errorf("bottom-right positioning wrong: %s", vf)
	}
	if !strings.Contains(vf, `text='台\'标A\:x\,y\%1\+2'`) {
		t.Errorf("text not escaped as expected: %s", vf)
	}
	if !strings.Contains(vf, "fontfile=/usr") {
		t.Errorf("expected resolved fontfile arg, got: %s", vf)
	}
	// OSD forces a re-encode even without scale settings.
	if !strings.Contains(strings.Join(args, " "), "libx264") {
		t.Errorf("osd mode must re-encode: %v", args)
	}
}

func TestBuildArgsBoth(t *testing.T) {
	job := model.TranscodeJob{Mode: "both", Width: 960, Text: "A", Position: "top-left"}
	vf := findVF(t, BuildArgs(job, "in", "out"))
	if !strings.HasPrefix(vf, "scale=960:-2,") {
		t.Errorf("both mode must chain scale first, got %q", vf)
	}
	if !strings.Contains(vf, "drawtext") {
		t.Errorf("both mode missing drawtext: %q", vf)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(&model.TranscodeJob{Mode: "osd", Text: "  "}); err == nil {
		t.Fatal("osd without text must fail validation")
	}
	if err := Validate(&model.TranscodeJob{Mode: "weird"}); err == nil {
		t.Fatal("invalid mode must fail validation")
	}
	job := &model.TranscodeJob{Mode: "transcode", Position: "corner9"}
	if err := Validate(job); err != nil {
		t.Fatalf("transcode mode with no text must pass: %v", err)
	}
	if job.Position != "top-left" {
		t.Errorf("unknown position should reset to top-left, got %q", job.Position)
	}
}

// A crashing ffmpeg process marks the job failed with its stderr tail, while a
// Stop-cancelled process ends up "stopped" — the two terminal states must not
// blur, and the finished process handle must be released either way.
func TestProcessLifecycleTransitions(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, video.NewClient("http://x", "s", "127.0.0.1", 80, 8080, 554, 1935))
	job := seedJob(t, db)

	var fakeArgs []string
	s.exe = func(ctx context.Context, args []string) *exec.Cmd {
		fakeArgs = args
		return exec.CommandContext(ctx, "true")
	}
	if err := s.Start(job.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !s.Running(job.ID) {
		t.Fatal("expected running right after Start")
	}
	var inArg string
	for i, arg := range fakeArgs {
		if arg == "-i" && i+1 < len(fakeArgs) {
			inArg = fakeArgs[i+1]
		}
	}
	out := fakeArgs[len(fakeArgs)-1]
	if inArg != "rtsp://127.0.0.1:554/live/ch_abc123def45678" {
		t.Errorf("input not built from channel stream key: %q", inArg)
	}
	if out != "rtsp://127.0.0.1:554/live/ch_abc123def45678_t" {
		t.Errorf("output not the derived stream key: %q", out)
	}
	waitTerminal(t, s, job.ID, "failed")
	if err := s.Stop(job.ID); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopping an exited job must fail fast, got %v", err)
	}
}

func TestStopCancelsRunningProcess(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, video.NewClient("http://x", "s", "127.0.0.1", 80, 8080, 554, 1935))
	job := seedJob(t, db)
	s.exe = func(ctx context.Context, args []string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "30")
	}
	if err := s.Start(job.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.Stop(job.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s.Running(job.ID) {
		t.Fatal("stop must release the handle")
	}
	waitTerminal(t, s, job.ID, "stopped")
}

func TestStopFailsFastWhenNotRunning(t *testing.T) {
	db := newTestDB(t)
	s := NewService(db, video.NewClient("http://x", "s", "127.0.0.1", 80, 8080, 554, 1935))
	if err := s.Stop(42); err == nil {
		t.Fatal("stop on non-running job must error")
	}
}

func waitTerminal(t *testing.T, s *Service, id uint, want string) {
	t.Helper()
	var last string
	for i := 0; i < 100; i++ {
		var job model.TranscodeJob
		if err := s.db.First(&job, id).Error; err != nil {
			t.Fatalf("reload job: %v", err)
		}
		if job.Status == want && !s.Running(id) {
			return
		}
		last = job.Status
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %d never reached %q (last %q)", id, want, last)
}
