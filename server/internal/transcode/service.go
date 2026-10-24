// Package transcode runs platform-side ffmpeg pipelines that derive new
// streams from channels already publishing in the media core: bitrate/scale
// transcodes and OSD watermark captions (drawtext). Input and output are the
// media daemon's RTSP endpoints on the loopback, so the camera is contacted
// once and the derived stream is just another ZLM stream.
package transcode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// Service manages derived-stream ffmpeg processes. Processes are owned by the
// running process (PID in the DB row aids ops but Stop always uses the handle).
type Service struct {
	db  *gorm.DB
	zlm *video.Client
	// exec is the process factory; replaced in tests.
	exe func(ctx context.Context, args []string) *exec.Cmd

	mu   sync.Mutex
	jobs map[uint]*proc
}

type proc struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	// stderr tail for LastError on unexpected exit.
	errLog   strings.Builder
	errTail  string
	errDone  chan struct{}
	stopFlag bool
}

func NewService(db *gorm.DB, zlm *video.Client) *Service {
	return &Service{
		db:   db,
		zlm:  zlm,
		exe:  defaultExec,
		jobs: map[uint]*proc{},
	}
}

func defaultExec(ctx context.Context, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

// Start launches the pipeline for a job. The job must have Enabled or be
// explicitly started by hand; a second start is rejected while running.
func (s *Service) Start(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.jobs[id]; p != nil {
		return fmt.Errorf("job %d already running", id)
	}
	var job model.TranscodeJob
	if err := s.db.First(&job, id).Error; err != nil {
		return fmt.Errorf("job %d: %w", id, err)
	}
	var ch model.Channel
	if err := s.db.First(&ch, job.ChannelID).Error; err != nil {
		return fmt.Errorf("channel %d: %w", job.ChannelID, err)
	}
	in := s.zlm.RTSPIngestURL(ch.StreamKey)
	out := s.zlm.RTSPIngestURL(job.StreamKey)
	args := BuildArgs(job, in, out)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := s.exe(ctx, args)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	p := &proc{cmd: cmd, cancel: cancel, errDone: make(chan struct{})}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				p.errLog.Write(buf[:n])
				p.errTail = tail(p.errLog.String())
			}
			if err != nil {
				break
			}
		}
		close(p.errDone)
	}()
	s.jobs[id] = p

	job.Status = "running"
	job.PID = cmd.Process.Pid
	job.LastError = ""
	s.db.Save(&job)
	log.Printf("[transcode] started job %d stream %s pid %d", id, job.StreamKey, job.PID)

	go func() {
		err := cmd.Wait()
		<-p.errDone
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.jobs[id] == p {
			delete(s.jobs, id)
		}
		var job2 model.TranscodeJob
		if s.db.First(&job2, id).Error != nil {
			return
		}
		if p.stopFlag || ctx.Err() != nil {
			job2.Status = "stopped"
			job2.PID = 0
			s.db.Save(&job2)
			log.Printf("[transcode] job %d stopped", id)
			return
		}
		reason := ""
		if err != nil {
			reason = err.Error()
		}
		job2.Status = "failed"
		job2.PID = 0
		job2.LastError = fmt.Sprintf("ffmpeg exited unexpectedly: %s", strings.TrimSpace(p.errTail+reason))
		if job2.Enabled {
			job2.Enabled = false
		}
		s.db.Save(&job2)
		log.Printf("[transcode] job %d failed: %s", id, job2.LastError)
	}()
	return nil
}

// Stop kills the pipeline if running and records a clean stop.
func (s *Service) Stop(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.jobs[id]
	if p == nil {
		return fmt.Errorf("job %d not running", id)
	}
	p.stopFlag = true
	p.cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.jobs[id] == nil {
			return nil
		}
		s.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		s.mu.Lock()
	}
	// Handle dead: force kill, the waiter goroutine will finalize.
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	return nil
}

// Running reports whether the pipeline is alive.
func (s *Service) Running(id uint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id] != nil
}

// StartEnabled relaunches enabled jobs at boot (the media core may not have
// the source stream up; ffmpeg then exits and the job is marked failed).
func (s *Service) StartEnabled() {
	var jobs []model.TranscodeJob
	s.db.Where("enabled = ?", true).Find(&jobs)
	for _, j := range jobs {
		if err := s.Start(j.ID); err != nil {
			log.Printf("[transcode] autostart job %d: %v", j.ID, err)
		}
	}
}

// BuildArgs turns a job into an ffmpeg pipeline from input to output RTSP.
// Deletion of rows keeps it pure for tests.
func BuildArgs(job model.TranscodeJob, input, output string) []string {
	args := []string{"-hide_banner", "-loglevel", "warning",
		"-rtsp_transport", "tcp", "-i", input}
	var vf []string
	if job.Width > 0 || job.Height > 0 {
		w := job.Width
		if w == 0 {
			w = -2
		}
		h := job.Height
		if h == 0 {
			h = -2
		}
		vf = append(vf, fmt.Sprintf("scale=%d:%d", w, h))
	}
	if job.Mode == "osd" || job.Mode == "both" {
		vf = append(vf, drawTextFilter(job.Text, job.Position, job.FontSize))
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast",
		"-b:v", strconv.Itoa(kbps(job.Bitrate))+"k", "-pix_fmt", "yuv420p")
	if len(vf) > 0 {
		args = append(args, "-vf", strings.Join(vf, ","))
	}
	args = append(args, "-c:a", "aac", "-g", "50",
		"-f", "rtsp", "-rtsp_transport", "tcp", output)
	return args
}

func kbps(b int) int {
	if b <= 0 {
		return 2000
	}
	return b
}

// DrawTextFilter builds an ffmpeg drawtext filter captioning the top or
// bottom corner; text is shell- and filter-escaped.
func DrawTextFilter(text, position string, size int) string {
	return drawTextFilter(text, position, size)
}

func drawTextFilter(text, position string, size int) string {
	if size <= 0 {
		size = 24
	}
	var x, y string
	switch position {
	case "top-right":
		x, y = "w-tw-40", "40"
	case "bottom-left":
		x, y = "40", "h-th-40"
	case "bottom-right":
		x, y = "w-tw-40", "h-th-40"
	default:
		x, y = "40", "40"
	}
	return fmt.Sprintf(
		"drawtext=%s:text='%s':x=%s:y=%s:fontsize=%d:fontcolor=white:box=1:boxcolor=black@0.45:boxborderw=12",
		fontFileArg(), escapeText(text), x, y, size)
}

var textEscape = strings.NewReplacer(
	`\`, `\\`,
	`'`, `\'`,
	`%`, `\%`,
	`:`, `\:`,
	`,`, `\,`,
	// expressions like x/y inside text evaluations
	`+`, `\+`,
)

func escapeText(t string) string {
	return textEscape.Replace(t)
}

// FontCandidates are probed in order for a usable TrueType/OTF font: CJK
// first (watermark text is user-facing Chinese), then common distro fonts.
var FontCandidates = []string{
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
}

// DrawFontFile finds a CJK-capable font for drawtext, falling back to common
// distro fonts; exported for tests and handler-side config probes.
func DrawFontFile() string {
	for _, p := range FontCandidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

func fontFileArg() string {
	if f := DrawFontFile(); f != "" {
		return "fontfile=" + f
	}
	return ""
}

// tail keeps a bounded string for LastError so rows stay small.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

// Validate checks user-specified job fields; the handler calls it before save.
func Validate(job *model.TranscodeJob) error {
	switch job.Mode {
	case "transcode", "osd", "both":
	default:
		return fmt.Errorf("不支持的转码模式: %q", job.Mode)
	}
	if job.Mode != "transcode" && strings.TrimSpace(job.Text) == "" {
		return errors.New("OSD 模式需要水印文本")
	}
	switch job.Position {
	case "top-left", "top-right", "bottom-left", "bottom-right":
	default:
		job.Position = "top-left"
	}
	return nil
}
