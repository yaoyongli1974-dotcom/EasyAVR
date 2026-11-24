package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/security"
	"github.com/easyavr/easyavr/internal/snapshot"
	"github.com/easyavr/easyavr/internal/store"
)

// This file is the centralized EASYAVR-TECH-002 security regression suite.
// Every top-level test name starts with Tech002Regression so the whole suite
// can be run with: go test ./internal/server -run Tech002Regression -count=1
// It reuses the existing test helpers from server_test.go / *_test.go and never
// touches ZLM, ffmpeg, the development database or any production resource.

const tech002StrictKey = "P9wQ2rT7yU4iO8pA1sD5fG6hJ0kL3zX8cV9bN2mQ4eR=" // 32 random bytes, base64

// ---------------------------------------------------------------------------
// 1. 凭据加解密 / 2. GORM 自动加密 / 3. 密钥轮换
// ---------------------------------------------------------------------------

func TestTech002RegressionCredentialEncryption(t *testing.T) {
	t.Run("enc prefix plaintext is really encrypted", func(t *testing.T) {
		e, err := security.New("tech002-credential-key", "v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, plain := range []string{"enc:password", "enc:v1:fake-base64", "enc:", "plain-password"} {
			cipher, err := e.Encrypt(plain)
			if err != nil {
				t.Fatalf("Encrypt(%q): %v", plain, err)
			}
			if cipher == plain || !strings.HasPrefix(cipher, "enc:v1:") {
				t.Fatalf("plaintext %q was not encrypted: %q", plain, cipher)
			}
			got, err := e.Decrypt(cipher)
			if err != nil || got != plain {
				t.Fatalf("round trip %q: %q, %v", plain, got, err)
			}
		}
	})

	t.Run("wrong key and tampering fail closed", func(t *testing.T) {
		e, _ := security.New("tech002-key-a", "v1", nil)
		cipher, err := e.Encrypt("camera-secret")
		if err != nil {
			t.Fatal(err)
		}
		other, _ := security.New("tech002-key-b", "v1", nil)
		if plain, err := other.Decrypt(cipher); err == nil || plain == "camera-secret" {
			t.Fatalf("wrong key decrypted: %q, %v", plain, err)
		}
		tampered := cipher[:len(cipher)-2] + "AA"
		if _, err := e.Decrypt(tampered); err == nil {
			t.Fatal("tampered ciphertext was accepted")
		}
		if _, err := e.Decrypt("legacy-plaintext"); err != security.ErrPlaintext {
			t.Fatalf("legacy plaintext: expected ErrPlaintext, got %v", err)
		}
	})

	t.Run("empty values are unchanged", func(t *testing.T) {
		e, _ := security.New("tech002-empty-key", "v1", nil)
		if c, err := e.Encrypt(""); err != nil || c != "" {
			t.Fatalf("empty encrypt: %q, %v", c, err)
		}
		if p, err := e.Decrypt(""); err != nil || p != "" {
			t.Fatalf("empty decrypt: %q, %v", p, err)
		}
	})
}

func TestTech002RegressionGORMAutoEncryption(t *testing.T) {
	setup := func(t *testing.T) (*gorm.DB, *security.Encryptor) {
		t.Helper()
		e, err := security.New("tech002-gorm-key", "v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		security.SetDefault(e)
		t.Cleanup(func() { security.SetDefault(nil) })
		db, err := store.Open(filepath.Join(t.TempDir(), "gorm.db"))
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		return db, e
	}

	t.Run("credential columns are ciphertext at rest and decrypted on read", func(t *testing.T) {
		db, e := setup(t)
		dev := model.Device{Name: "reg-cam", Password: "camera-pass-123", Protocol: "rtsp"}
		if err := db.Create(&dev).Error; err != nil {
			t.Fatal(err)
		}
		ch := model.NotificationChannel{Name: "reg-hook", Type: "webhook", Secret: "hook-secret-456"}
		if err := db.Create(&ch).Error; err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			table, column string
			id            uint
			plain         string
		}{
			{"devices", "password", dev.ID, "camera-pass-123"},
			{"notification_channels", "secret", ch.ID, "hook-secret-456"},
		} {
			var raw string
			if err := db.Table(tc.table).Where("id = ?", tc.id).Pluck(tc.column, &raw).Error; err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, tc.plain) {
				t.Fatalf("%s.%s stored in cleartext: %q", tc.table, tc.column, raw)
			}
			if plain, err := e.Decrypt(raw); err != nil || plain != tc.plain {
				t.Fatalf("%s.%s decrypt: %q, %v", tc.table, tc.column, plain, err)
			}
		}

		var readDev model.Device
		if err := db.First(&readDev, dev.ID).Error; err != nil {
			t.Fatal(err)
		}
		if readDev.Password != "camera-pass-123" {
			t.Fatalf("AfterFind did not decrypt device password: %q", readDev.Password)
		}
		// Repeated Save of the same instance must not double-encrypt.
		readDev.Name = "renamed"
		if err := db.Save(&readDev).Error; err != nil {
			t.Fatal(err)
		}
		var raw string
		db.Table("devices").Where("id = ?", dev.ID).Pluck("password", &raw)
		if strings.HasPrefix(raw, "enc:v1:enc:") || strings.Count(raw, "enc:v1:") != 1 {
			t.Fatalf("credential was double-encrypted: %q", raw)
		}
	})

	t.Run("wrong key fails closed on read", func(t *testing.T) {
		db, _ := setup(t)
		dev := model.Device{Name: "reg-cam-2", Password: "locked-secret", Protocol: "rtsp"}
		if err := db.Create(&dev).Error; err != nil {
			t.Fatal(err)
		}
		wrong, _ := security.New("tech002-wrong-gorm-key", "v1", nil)
		security.SetDefault(wrong)
		var read model.Device
		if err := db.First(&read, dev.ID).Error; err == nil {
			t.Fatal("AfterFind accepted a credential encrypted under a different key")
		}
	})
}

func TestTech002RegressionKeyRotation(t *testing.T) {
	t.Run("legacy ciphertext decrypts under new kid and new writes use new kid", func(t *testing.T) {
		old, err := security.New("tech002-legacy-passphrase", "v1", nil)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := old.Encrypt("legacy-secret")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := base64.StdEncoding.DecodeString(tech002StrictKey); err != nil {
			t.Fatalf("bad test key: %v", err)
		}
		rotated, err := security.NewStrict(tech002StrictKey, "v2", []string{"v1:tech002-legacy-passphrase"})
		if err != nil {
			t.Fatal(err)
		}
		plain, err := rotated.Decrypt(legacy)
		if err != nil || plain != "legacy-secret" {
			t.Fatalf("legacy ciphertext after rotation: %q, %v", plain, err)
		}
		fresh, err := rotated.Encrypt("fresh-secret")
		if err != nil || !strings.HasPrefix(fresh, "enc:v2:") {
			t.Fatalf("new ciphertext kid: %q, %v", fresh, err)
		}
		withoutPrevious, _ := security.NewStrict(tech002StrictKey, "v2", nil)
		if _, err := withoutPrevious.Decrypt(legacy); err == nil {
			t.Fatal("legacy ciphertext decrypted without the previous key")
		}
		unknownKid := "enc:v99:" + strings.SplitN(fresh, ":", 3)[2]
		if _, err := rotated.Decrypt(unknownKid); err == nil {
			t.Fatal("unknown key id was accepted")
		}
	})

	t.Run("strict key parser rejects weak and passphrase keys", func(t *testing.T) {
		if _, err := security.ParseStrictKey("easyavr-password-123"); err == nil {
			t.Fatal("strict parser accepted a passphrase")
		}
		if _, err := security.ParseStrictKey(base64.StdEncoding.EncodeToString(make([]byte, 32))); err == nil {
			t.Fatal("strict parser accepted an all-zero key")
		}
		if _, err := security.ParseStrictKey(tech002StrictKey); err != nil {
			t.Fatalf("strict parser rejected a valid random key: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. 快照 Token / 5. 快照实时权限 / 6. 路径穿越 / 7. 符号链接逃逸
// ---------------------------------------------------------------------------

func TestTech002RegressionSnapshotToken(t *testing.T) {
	srv, cfg, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)
	_, chID := newSnapshotChannel(t, app, "reg-token")
	snapID := addSnapshotFile(t, app, chID, "reg/token.jpg")

	var r apiResp
	code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapID), admin, nil, &r)
	if code != http.StatusOK || r.Code != 0 {
		t.Fatalf("access-url: %d %+v", code, r)
	}
	var issued struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(r.Data, &issued); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(issued.URL, "/api/v1/snapshot-access/s") {
		t.Fatalf("unexpected signed url: %q", issued.URL)
	}
	if status, body := getRaw(t, srv.URL+issued.URL); status != http.StatusOK || body != "JPEG:reg/token.jpg" {
		t.Fatalf("signed access: %d %q", status, body)
	}

	// Tampered / expired / wrong-secret tokens and missing JWT are all 401.
	parts := strings.Split(strings.TrimPrefix(issued.URL, "/api/v1/snapshot-access/"), ".")
	payload := []byte(parts[0])
	if payload[1] == 'A' {
		payload[1] = 'B'
	} else {
		payload[1] = 'A'
	}
	if status, body := getRaw(t, srv.URL+"/api/v1/snapshot-access/"+string(payload)+"."+parts[1]); status != http.StatusUnauthorized {
		t.Fatalf("tampered token: got %d %q, want 401", status, body)
	}
	expired, _, err := snapshot.SnapshotAccessToken(snapID, 1, -time.Minute, cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := getRaw(t, srv.URL+"/api/v1/snapshot-access/"+expired); status != http.StatusUnauthorized {
		t.Fatalf("expired token: got %d, want 401", status)
	}
	wrong, _, err := snapshot.SnapshotAccessToken(snapID, 1, time.Minute, "wrong-secret")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := getRaw(t, srv.URL+"/api/v1/snapshot-access/"+wrong); status != http.StatusUnauthorized {
		t.Fatalf("wrong-secret token: got %d, want 401", status)
	}
	if status := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapID), "", nil, &r); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated access-url: got %d, want 401", status)
	}
}

func TestTech002RegressionSnapshotRealtimePermission(t *testing.T) {
	srv, _, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)
	snapshotRole(t, srv, admin, "reg-video", "video")
	userID, userJWT := snapshotUser(t, srv, admin, "reg-video-user", "reg-video")
	devID, chID := newSnapshotChannel(t, app, "reg-permission")
	gID := snapshotGroup(t, srv, admin, "reg-permission-group")
	var r apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin,
		map[string]any{"groupId": gID, "channelIds": []uint{chID}}, &r)
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/devices/bind", admin,
		map[string]any{"groupId": gID, "deviceIds": []uint{devID}}, &r)
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/user-groups", admin,
		map[string]any{"userId": userID, "groupId": gID, "permissions": "video"}, &r)

	snapID := addSnapshotFile(t, app, chID, "reg/permission.jpg")
	token := snapshotSignedTokenFromAccessURL(t, srv, userJWT, snapID)
	if code, body := snapshotSignedGet(t, srv, token); code != http.StatusOK || body != "JPEG:reg/permission.jpg" {
		t.Fatalf("precondition: token should work, got %d %q", code, body)
	}

	// Disabling the user invalidates the still-unexpired signed URL.
	if err := app.db.Model(&model.User{}).Where("id = ?", userID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	code, body := snapshotSignedGet(t, srv, token)
	if code != http.StatusForbidden {
		t.Fatalf("disabled user: got %d %q, want 403", code, body)
	}
	assertSnapshotAccessNoLocalPath(t, app, body)
}

func TestTech002RegressionPathTraversal(t *testing.T) {
	t.Run("catalog traversal path is refused by signed access", func(t *testing.T) {
		srv, cfg, app := newSnapshotTestServer(t)
		adminID := snapshotUserID(t, app, cfg.AdminUser)
		_, chID := newSnapshotChannel(t, app, "reg-traversal")
		badID := addSnapshotFile(t, app, chID, "../outside.jpg")
		token, _, err := snapshot.SnapshotAccessToken(badID, adminID, time.Minute, cfg.JWTSecret)
		if err != nil {
			t.Fatal(err)
		}
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusForbidden {
			t.Fatalf("path traversal: got %d %q, want 403", code, body)
		}
		if strings.Contains(body, "outside.jpg") || strings.Contains(body, app.snaps.Dir()) {
			t.Fatalf("response leaked path information: %q", body)
		}
	})

	t.Run("ResolvePath rejects absolute, traversal and sibling-prefix paths", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		_ = srv // ResolvePath is a service-level invariant, no HTTP needed.
		sibling := filepath.Join(filepath.Dir(app.snaps.Dir()), filepath.Base(app.snaps.Dir())+"-evil")
		if err := os.MkdirAll(sibling, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sibling, "secret.jpg"), []byte("SECRET"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, rel := range []string{
			"../outside.jpg",
			"/etc/passwd",
			"../" + filepath.Base(app.snaps.Dir()) + "-evil/secret.jpg",
			"",
		} {
			if got, err := app.snaps.ResolvePath(rel); err == nil {
				t.Fatalf("ResolvePath(%q) returned %q, want rejection", rel, got)
			}
		}
		// A normal relative path still resolves inside the snapshot root.
		abs, err := app.snaps.ResolvePath("ok/1.jpg")
		if err == nil || abs != "" {
			t.Fatalf("missing file should be ErrPathNotFound, got %q, %v", abs, err)
		}
	})
}

func TestTech002RegressionSymlinkEscape(t *testing.T) {
	srv, cfg, app := newSnapshotTestServer(t)
	adminID := snapshotUserID(t, app, cfg.AdminUser)
	_, chID := newSnapshotChannel(t, app, "reg-symlink")

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.jpg")
	if err := os.WriteFile(outside, []byte("TOP-SECRET-REGRESSION"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(app.snaps.Dir(), "escape.jpg")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	snap := model.Snapshot{ChannelID: chID, Path: "escape.jpg", URL: "/snapshots/escape.jpg", TakenAt: time.Now()}
	if err := app.db.Create(&snap).Error; err != nil {
		t.Fatal(err)
	}
	token, _, err := snapshot.SnapshotAccessToken(snap.ID, adminID, time.Minute, cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	code, body := snapshotSignedGet(t, srv, token)
	if code != http.StatusForbidden {
		t.Fatalf("symlink escape: got %d %q, want 403", code, body)
	}
	if strings.Contains(body, "TOP-SECRET-REGRESSION") || strings.Contains(body, app.snaps.Dir()) {
		t.Fatalf("symlink escape leaked content or path: %q", body)
	}
}

// ---------------------------------------------------------------------------
// 8. AI 启动状态 / 9. 运行记录一致性 / 10. Runner 生命周期
// ---------------------------------------------------------------------------

type tech002Analyzer struct{ calls *int }

func (a tech002Analyzer) Kind() string { return "cv" }
func (a tech002Analyzer) Analyze(context.Context, ai.AnalyzeInput) ([]ai.Result, error) {
	*a.calls++
	return []ai.Result{{EventType: "regression", Level: "info", Summary: "ok"}}, nil
}

func tech002RunCount(t *testing.T, db *gorm.DB, taskID uint) []model.AITaskRun {
	t.Helper()
	var runs []model.AITaskRun
	if err := db.Where("task_id = ?", taskID).Order("id ASC").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	return runs
}

func tech002WaitRuns(t *testing.T, db *gorm.DB, taskID uint, want int) []model.AITaskRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs := tech002RunCount(t, db, taskID)
		if len(runs) >= want {
			// The run row is created before analysis starts, so a non-terminal
			// row is not proof that the execution finished. Wait for the
			// terminal state to avoid a race-dependent assertion in callers.
			allTerminal := true
			for _, run := range runs[:want] {
				switch run.Status {
				case ai.RunSucceeded, ai.RunFailed, ai.RunTimeout, ai.RunStopped:
					if run.FinishedAt == nil {
						allTerminal = false
					}
				default:
					allTerminal = false
				}
			}
			if allTerminal {
				return runs
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d run(s) of task %d", want, taskID)
	return nil
}

func TestTech002RegressionAIStartupState(t *testing.T) {
	t.Run("successful start marks task running and enables it", func(t *testing.T) {
		srv, a := newRunsTestServer(t)
		_ = srv
		taskID, _, _ := seedRunsTask(t, a, "stopped", `{"intervalSec":0}`)
		calls := 0
		a.runner.SetAnalyzerFactory(func(model.AIProvider) (ai.Analyzer, error) {
			return tech002Analyzer{calls: &calls}, nil
		})
		if err := a.runner.Start(taskID); err != nil {
			t.Fatalf("start: %v", err)
		}
		defer a.runner.Stop(taskID)

		var task model.AITask
		if err := a.db.First(&task, taskID).Error; err != nil {
			t.Fatal(err)
		}
		if task.Status != "running" || !task.Enabled {
			t.Fatalf("start did not persist running/enabled: status=%q enabled=%v", task.Status, task.Enabled)
		}
	})

	t.Run("DB failure aborts start without launching execution", func(t *testing.T) {
		srv, a := newRunsTestServer(t)
		_ = srv
		taskID, _, _ := seedRunsTask(t, a, "stopped", `{"intervalSec":0}`)
		calls := 0
		a.runner.SetAnalyzerFactory(func(model.AIProvider) (ai.Analyzer, error) {
			return tech002Analyzer{calls: &calls}, nil
		})
		if runs := tech002RunCount(t, a.db, taskID); len(runs) != 0 {
			t.Fatal("precondition: task already has run records")
		}
		sqlDB, err := a.db.DB()
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatal(err)
		}
		if err := a.runner.Start(taskID); err == nil {
			t.Fatal("Start succeeded with a closed database")
		}
		if calls != 0 {
			t.Fatalf("analyzer ran despite failed startup: %d calls", calls)
		}
		// Note: the SQL handle is closed, so no post-failure DB query is made here.
	})
}

func TestTech002RegressionRunRecordConsistency(t *testing.T) {
	srv, a := newRunsTestServer(t)
	token := login(t, srv.URL)
	taskID, _, providerID := seedRunsTask(t, a, "stopped", `{"intervalSec":0,"grabFrame":false}`)

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"eventType":"run_consistency","level":"info","summary":"ok"}]`)
	}))
	defer worker.Close()
	a.db.Model(&model.AIProvider{}).Where("id = ?", providerID).Update("endpoint", worker.URL)

	// A single /run produces exactly one event and leaves no orphan run record.
	var run apiResp
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/run", token, nil, &run); code != http.StatusOK || run.Code != 0 {
		t.Fatalf("run once: %d %+v", code, run)
	}
	var result struct {
		Count int `json:"count"`
	}
	json.Unmarshal(run.Data, &result)
	if result.Count != 1 {
		t.Fatalf("expected 1 event, got %d: %s", result.Count, run.Data)
	}
	if runs := tech002RunCount(t, a.db, taskID); len(runs) != 0 {
		t.Fatalf("/run must not create scheduled run records, got %d", len(runs))
	}

	// A scheduled execution creates exactly one terminal run record.
	calls := 0
	a.runner.SetAnalyzerFactory(func(model.AIProvider) (ai.Analyzer, error) {
		return tech002Analyzer{calls: &calls}, nil
	})
	if err := a.runner.Start(taskID); err != nil {
		t.Fatal(err)
	}
	runs := tech002WaitRuns(t, a.db, taskID, 1)
	if runs[0].Status != "succeeded" || runs[0].FinishedAt == nil {
		t.Fatalf("run not finalized: %+v", runs[0])
	}
	a.runner.Stop(taskID)

	// Recover must not replay a terminal run.
	before := len(tech002RunCount(t, a.db, taskID))
	a.db.Model(&model.AITask{}).Where("id = ?", taskID).Update("status", "running")
	a.db.Model(&model.AITaskRun{}).Where("task_id = ?", taskID).Update("status", "succeeded")
	if err := a.runner.Recover(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if after := len(tech002RunCount(t, a.db, taskID)); after != before {
		t.Fatalf("Recover replayed terminal runs: %d -> %d", before, after)
	}

	// Lingering (non-terminal) runs are closed out with a terminal status.
	lingering := model.AITaskRun{TaskID: taskID, Status: "running", WorkerID: "old-worker"}
	if err := a.db.Create(&lingering).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.runner.RecoverTask(taskID); err != nil {
		t.Fatal(err)
	}
	var closed model.AITaskRun
	if err := a.db.First(&closed, lingering.ID).Error; err != nil {
		t.Fatal(err)
	}
	if closed.Status == "running" || closed.FinishedAt == nil || closed.ErrorCode == "" {
		t.Fatalf("lingering run not closed consistently: %+v", closed)
	}
}

func TestTech002RegressionRunnerLifecycle(t *testing.T) {
	srv, a := newRunsTestServer(t)
	_ = srv
	taskID, _, _ := seedRunsTask(t, a, "stopped", `{"intervalSec":0}`)
	calls := 0
	a.runner.SetAnalyzerFactory(func(model.AIProvider) (ai.Analyzer, error) {
		return tech002Analyzer{calls: &calls}, nil
	})
	if err := a.runner.Start(taskID); err != nil {
		t.Fatal(err)
	}
	tech002WaitRuns(t, a.db, taskID, 1)

	// Close is idempotent and blocks until the loop has exited.
	a.runner.Close()
	a.runner.Close()
	if !a.runner.IsClosed() {
		t.Fatal("runner not marked closed")
	}
	select {
	case <-a.runner.Done():
	default:
		t.Fatal("Done channel was not closed")
	}

	runsAfterClose := len(tech002RunCount(t, a.db, taskID))
	if err := a.runner.Start(taskID); err != ai.ErrRunnerClosed {
		t.Fatalf("Start after close = %v, want ErrRunnerClosed", err)
	}
	if err := a.runner.Recover(); err != ai.ErrRunnerClosed {
		t.Fatalf("Recover after close = %v, want ErrRunnerClosed", err)
	}
	if err := a.runner.RecoverTask(taskID); err != ai.ErrRunnerClosed {
		t.Fatalf("RecoverTask after close = %v, want ErrRunnerClosed", err)
	}
	if _, err := a.runner.RunOnce(context.Background(), model.AITask{}); err != ai.ErrRunnerClosed {
		t.Fatalf("RunOnce after close = %v, want ErrRunnerClosed", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(tech002RunCount(t, a.db, taskID)); got != runsAfterClose {
		t.Fatalf("DB write after Close: %d -> %d", runsAfterClose, got)
	}
	if got := calls; got == 0 {
		t.Fatal("analyzer never ran before Close")
	}
}

// ---------------------------------------------------------------------------
// 13. 生产配置检查 / 14. 健康检查 / 15. 审计脱敏
// ---------------------------------------------------------------------------

func TestTech002RegressionProductionConfig(t *testing.T) {
	setting := map[string]string{
		"EASYAVR_ENV":                     "production",
		"EASYAVR_JWT_SECRET":              "a-strong-jwt-secret-value-12345",
		"EASYAVR_ADMIN_PASS":              "StrongAdminPass123",
		"EASYAVR_ALLOW_ORIGIN":            "https://example.com",
		"EASYAVR_DB_DRIVER":               "sqlite",
		"EASYAVR_DB":                      filepath.Join(t.TempDir(), "cfg.db"),
		"EASYAVR_SNAPSHOT_DIR":            filepath.Join(t.TempDir(), "snaps"),
		"EASYAVR_GB_ENABLED":              "false",
		"EASYAVR_GB35114_ENABLED":         "false",
		"EASYAVR_CLUSTER_ENABLED":         "false",
		"EASYAVR_CREDENTIAL_KEY_PREVIOUS": "",
	}
	for k, v := range setting {
		t.Setenv(k, v)
	}
	dir := setting["EASYAVR_SNAPSHOT_DIR"]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EASYAVR_CREDENTIAL_KEY", "easyavr-weak-passphrase")
	err := config.Load().Validate()
	if err == nil {
		t.Fatal("production accepted a passphrase master key")
	}
	if !strings.Contains(err.Error(), "EASYAVR_CREDENTIAL_KEY") {
		t.Fatalf("error did not name the setting: %v", err)
	}
	if strings.Contains(err.Error(), "easyavr-weak-passphrase") {
		t.Fatalf("error leaked the key value: %v", err)
	}

	t.Setenv("EASYAVR_CREDENTIAL_KEY", tech002StrictKey)
	if err := config.Load().Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

// tech002HealthApp builds an isolated health-only app so this suite stays
// independent of other test helpers that may evolve in parallel.
func tech002HealthApp(t *testing.T) (*App, *gin.Engine, []string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "health.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Listen: ":0", DBPath: dbPath,
		JWTSecret: "tech002-health-jwt-secret-9F3A7C1E",
		AdminPass: "tech002-health-admin-secret-2B8D4F6A",
	}
	cfg.ZLM.APIBase = "http://127.0.0.1:1"
	cfg.ZLM.Secret = "tech002-health-zlm-secret-7C1E9A3F"
	cfg.GB.Password = "tech002-health-gb-secret-5D2B8F4A"
	cfg.Cluster.Secret = "tech002-health-cluster-secret-1E6C3A9D"
	cfg.Map.AMapKey = "tech002-health-amap-secret-4F8B2D7E"
	app := New(cfg, db)
	engine := gin.New()
	engine.GET("/readyz", app.readyz)
	engine.GET("/health/details", app.healthzDetailed)
	secrets := []string{
		cfg.JWTSecret, cfg.AdminPass, cfg.ZLM.Secret, cfg.GB.Password,
		cfg.Cluster.Secret, cfg.Map.AMapKey, dbPath,
	}
	return app, engine, secrets
}

func TestTech002RegressionHealthNoSecrets(t *testing.T) {
	_, engine, secrets := tech002HealthApp(t)
	rec, _ := healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertNoSecrets(t, rec.Body.String(), secrets)

	rec, _ = healthRequest(t, engine, "/health/details")
	if rec.Code != http.StatusOK {
		t.Fatalf("health details status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertNoSecrets(t, rec.Body.String(), secrets)

	// The only hard failure remains an unavailable database.
	app, engine2, secrets2 := tech002HealthApp(t)
	sqlDB, err := app.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	rec, _ = healthRequest(t, engine2, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("DB down: got %d, want 503", rec.Code)
	}
	assertNoSecrets(t, rec.Body.String(), secrets2)
}

func TestTech002RegressionAuditRedaction(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	const devicePassword = "audit-regression-device-password"

	var created apiResp
	code := doJSON(t, http.MethodPost, base+"/devices", admin, map[string]any{
		"name": "audit-reg-cam", "protocol": "rtsp", "ip": "10.0.0.101",
		"username": "camera", "password": devicePassword,
	}, &created)
	if code != http.StatusOK || created.Code != 0 {
		t.Fatalf("create device: %d %+v", code, created)
	}

	var audit apiResp
	doJSON(t, http.MethodGet, base+"/audit/logs", admin, nil, &audit)
	var page struct {
		Items []struct {
			Action      string `json:"action"`
			Resource    string `json:"resource"`
			RequestBody string `json:"requestBody"`
		} `json:"items"`
	}
	if err := json.Unmarshal(audit.Data, &page); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if strings.Contains(item.RequestBody, devicePassword) || item.RequestBody != "" {
			t.Fatalf("audit entry leaked a request body: %+v", item)
		}
		if item.Resource == "devices" && item.Action == "create" {
			found = true
		}
	}
	if !found {
		t.Fatal("device create audit entry missing")
	}
	if strings.Contains(string(audit.Data), devicePassword) {
		t.Fatal("audit response echoed the device password")
	}
}
