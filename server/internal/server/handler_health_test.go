package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/recording"
	"github.com/easyavr/easyavr/internal/snapshot"
	"github.com/easyavr/easyavr/internal/storage"
	"github.com/easyavr/easyavr/internal/store"
	"github.com/easyavr/easyavr/internal/video"
)

// newHealthTestApp builds an isolated App backed by a throwaway sqlite file in
// t.TempDir(). It never touches the development/production database or any
// global state. The returned secrets are distinctive values injected into cfg
// so leak assertions can check the wire payload.
func newHealthTestApp(t *testing.T, thresholds ...storage.Config) (*App, *gin.Engine, []string) {
	t.Helper()
	var cfgThresholds storage.Config
	if len(thresholds) > 0 {
		cfgThresholds = thresholds[0]
	}
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "health.db"))
	if err != nil {
		t.Fatalf("open isolated db: %v", err)
	}

	cfg := &config.Config{
		Listen:    ":0",
		DBPath:    filepath.Join(dir, "health.db"),
		JWTSecret: "health-jwt-secret-9F3A7C1E",
		AdminPass: "health-admin-secret-2B8D4F6A",
	}
	cfg.ZLM.Secret = "health-zlm-secret-7C1E9A3F"
	cfg.GB.Password = "health-gb-secret-5D2B8F4A"
	cfg.GB.Enabled = true
	cfg.Cluster.Secret = "health-cluster-secret-1E6C3A9D"
	cfg.Map.AMapKey = "health-amap-secret-4F8B2D7E"

	zlm := video.NewClient("http://127.0.0.1:1", cfg.ZLM.Secret, "127.0.0.1", 80, 8080, 554, 1935)
	st := storage.New(db, cfgThresholds)
	st.SetDirs(map[string]string{
		"data":       dir,
		"snapshots":  dir,
		"certs":      dir,
		"recordings": dir,
	})

	// A task holding a distinctive prompt and provider API key: health probes
	// must never surface either value even though the runner reads run history.
	promptSecret := "health-prompt-secret-3C7F1A8B"
	apiKeySecret := "health-provider-apikey-6D9E2B5F"
	dev := model.Device{Name: "health-dev", Protocol: "rtsp"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	ch := model.Channel{DeviceID: dev.ID, Name: "health-cam", SourceURL: "rtsp://127.0.0.1/source", StreamKey: "health-stream"}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	provider := model.AIProvider{
		Name: "cv", Kind: ai.KindCV, Endpoint: "http://127.0.0.1:1/detect",
		APIKey: apiKeySecret, Enabled: true,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	task := model.AITask{
		Name: "health-task", ChannelID: ch.ID, ProviderID: provider.ID,
		TaskType: "cv_detect", Config: `{"prompt":"` + promptSecret + `"}`,
		Enabled: true, Status: "stopped",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	app := &App{
		cfg:     cfg,
		db:      db,
		zlm:     zlm,
		runner:  ai.NewRunner(db, zlm),
		rec:     recording.NewService(db, zlm),
		snaps:   snapshot.NewService(db, zlm, dir),
		storage: st,
	}

	engine := gin.New()
	engine.GET("/readyz", app.readyz)
	engine.GET("/health/details", app.healthzDetailed)

	secrets := []string{
		cfg.JWTSecret, cfg.AdminPass, cfg.ZLM.Secret, cfg.GB.Password,
		cfg.Cluster.Secret, cfg.Map.AMapKey, cfg.DBPath, promptSecret, apiKeySecret,
	}
	return app, engine, secrets
}

type healthData struct {
	Status  string                            `json:"status"`
	Time    string                            `json:"time"`
	Reason  string                            `json:"reason"`
	Checks  map[string]map[string]interface{} `json:"checks"`
	Alerts  []healthAlert                     `json:"alerts"`
	Storage map[string]json.RawMessage        `json:"storage"`
}

func healthRequest(t *testing.T, engine *gin.Engine, path string) (*httptest.ResponseRecorder, healthData) {
	t.Helper()
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var resp apiResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	var data healthData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode data: %v (%s)", err, resp.Data)
	}
	return rec, data
}

func assertNoSecrets(t *testing.T, body string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked sensitive value %q: %s", secret, body)
		}
	}
}

func assertCheckKeys(t *testing.T, data healthData, detailed bool) {
	t.Helper()
	for _, name := range []string{"db", "storage", "zlm", "ai", "gb", "gb35114", "tasks"} {
		check, ok := data.Checks[name]
		if !ok {
			t.Errorf("missing check %q in %+v", name, data.Checks)
			continue
		}
		if detailed {
			if _, ok := check["durationMs"]; !ok {
				t.Errorf("check %q missing durationMs: %+v", name, check)
			}
		}
	}
}

func TestReadyzReadyWhenZLMUnavailable(t *testing.T) {
	_, engine, secrets := newHealthTestApp(t, storage.Config{})

	rec, data := healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if data.Status != "ready" {
		t.Errorf("status=%q want ready", data.Status)
	}
	if data.Time == "" || data.Reason != "" {
		t.Errorf("time=%q reason=%q", data.Time, data.Reason)
	}
	assertCheckKeys(t, data, false)

	if data.Checks["db"]["status"] != "up" {
		t.Errorf("db check=%+v", data.Checks["db"])
	}
	// ZLM points at a closed port: reported down but readiness stays up.
	if data.Checks["zlm"]["healthy"] != false {
		t.Errorf("zlm check=%+v", data.Checks["zlm"])
	}
	if data.Checks["gb"]["enabled"] != true || data.Checks["gb"]["running"] != false {
		t.Errorf("gb check=%+v", data.Checks["gb"])
	}
	if data.Checks["gb35114"]["enabled"] != false {
		t.Errorf("gb35114 check=%+v", data.Checks["gb35114"])
	}
	if data.Checks["tasks"]["recording"] != true || data.Checks["tasks"]["snapshots"] != true || data.Checks["tasks"]["runner"] != true {
		t.Errorf("tasks check=%+v", data.Checks["tasks"])
	}
	if data.Alerts == nil {
		t.Errorf("alerts must be a JSON array: %s", rec.Body.String())
	}
	assertNoSecrets(t, rec.Body.String(), secrets)
}

func TestReadyzDegradedWhenDatabaseUnavailable(t *testing.T) {
	app, engine, secrets := newHealthTestApp(t, storage.Config{})
	sqlDB, err := app.db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	rec, data := healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if data.Status != "degraded" {
		t.Errorf("status=%q want degraded", data.Status)
	}
	if !strings.Contains(data.Reason, "database unavailable") {
		t.Errorf("reason=%q want database unavailable", data.Reason)
	}
	if data.Checks["db"]["status"] != "down" {
		t.Errorf("db check=%+v", data.Checks["db"])
	}
	assertCheckKeys(t, data, false)
	assertNoSecrets(t, rec.Body.String(), secrets)
}

func TestReadyzIgnoresDiskAlerts(t *testing.T) {
	// Force a threshold crossing on the isolated temp volume.
	_, engine, _ := newHealthTestApp(t, storage.Config{
		InfoPercent: 1, WarnPercent: 1, CriticalPercent: 1, MaxWalkEntries: 16,
	})

	rec, data := healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if data.Status != "ready" {
		t.Errorf("disk alert must not fail readiness: status=%q alerts=%+v", data.Status, data.Alerts)
	}
	found := false
	for _, alert := range data.Alerts {
		if alert.Level == storage.LevelCritical || alert.Level == storage.LevelWarning {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning/critical storage alert, got %+v", data.Alerts)
	}
}

func TestHealthzDetailedIncludesTimingsAndStorage(t *testing.T) {
	_, engine, secrets := newHealthTestApp(t, storage.Config{})

	rec, data := healthRequest(t, engine, "/health/details")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if data.Status != "ready" {
		t.Errorf("status=%q want ready", data.Status)
	}
	assertCheckKeys(t, data, true)
	for name, check := range data.Checks {
		ms, ok := check["durationMs"].(float64)
		if !ok || ms < 0 {
			t.Errorf("check %q durationMs=%v", name, check["durationMs"])
		}
	}
	if _, ok := data.Storage["TotalBytes"]; !ok {
		t.Errorf("detailed view missing full storage health: %+v", data.Storage)
	}
	if _, ok := data.Storage["CheckedAt"]; !ok {
		t.Errorf("detailed view missing CheckedAt: %+v", data.Storage)
	}
	assertNoSecrets(t, rec.Body.String(), secrets)
}

func hasAlert(data healthData, message string) bool {
	for _, alert := range data.Alerts {
		if alert.Message == message {
			return true
		}
	}
	return false
}

func assertAILegacyFields(t *testing.T, check map[string]interface{}) {
	t.Helper()
	for _, key := range []string{"status", "available"} {
		if _, ok := check[key]; !ok {
			t.Errorf("checks.ai missing legacy field %q: %+v", key, check)
		}
	}
}

// A live runner with failing/stale runs is reported as degraded with alerts,
// but readiness stays up: AI is a non-core dependency.
func TestReadyzAIReflectsSchedulerState(t *testing.T) {
	app, engine, secrets := newHealthTestApp(t, storage.Config{})

	rec, data := healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusOK || data.Status != "ready" {
		t.Fatalf("baseline status=%d/%q body=%s", rec.Code, data.Status, rec.Body.String())
	}
	baseline := data.Checks["ai"]
	assertAILegacyFields(t, baseline)
	if baseline["status"] != ai.HealthAvailable || baseline["healthy"] != true || baseline["closed"] != false {
		t.Fatalf("baseline ai check=%+v", baseline)
	}
	// Public probe carries aggregate state only: no timestamps or counters.
	for _, key := range []string{"lastRunAt", "lastHeartbeatAt", "consecutiveFailures", "staleHeartbeats"} {
		if _, ok := baseline[key]; ok {
			t.Errorf("public ai check leaked detail %q: %+v", key, baseline)
		}
	}

	var task model.AITask
	if err := app.db.First(&task).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	for range 3 {
		run := model.AITaskRun{TaskID: task.ID, Status: ai.RunFailed, StartedAt: &old, LastRunAt: &old}
		if err := app.db.Create(&run).Error; err != nil {
			t.Fatalf("seed failed run: %v", err)
		}
	}
	stale := model.AITaskRun{
		TaskID: task.ID, Status: ai.RunRunning,
		StartedAt: &old, LastRunAt: &old, HeartbeatAt: &old,
	}
	if err := app.db.Create(&stale).Error; err != nil {
		t.Fatalf("seed stale run: %v", err)
	}

	rec, data = healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusOK || data.Status != "ready" {
		t.Fatalf("AI anomaly must not fail readiness: status=%d/%q body=%s", rec.Code, data.Status, rec.Body.String())
	}
	check := data.Checks["ai"]
	assertAILegacyFields(t, check)
	if check["status"] != ai.HealthDegraded || check["healthy"] != false {
		t.Fatalf("degraded ai check=%+v", check)
	}
	for _, message := range []string{"ai scheduler heartbeat stale", "ai runs failing consecutively"} {
		if !hasAlert(data, message) {
			t.Errorf("missing alert %q in %+v", message, data.Alerts)
		}
	}
	assertNoSecrets(t, rec.Body.String(), secrets)

	// Authenticated detail adds timestamps and counters, still no secrets.
	rec, detail := healthRequest(t, engine, "/health/details")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
	}
	aiDetail := detail.Checks["ai"]
	assertAILegacyFields(t, aiDetail)
	for key, minimum := range map[string]float64{"consecutiveFailures": 3, "staleHeartbeats": 1} {
		value, ok := aiDetail[key].(float64)
		if !ok || value < minimum {
			t.Errorf("%s=%v want >= %v", key, aiDetail[key], minimum)
		}
	}
	if aiDetail["lastHeartbeatAt"] == "" || aiDetail["lastHeartbeatAt"] == nil {
		t.Errorf("lastHeartbeatAt=%v", aiDetail["lastHeartbeatAt"])
	}
	assertNoSecrets(t, rec.Body.String(), secrets)
}

// A closed runner is unavailable and produces an alert without failing the
// public probe.
func TestReadyzAIClosedRunnerAlerts(t *testing.T) {
	app, engine, secrets := newHealthTestApp(t, storage.Config{})
	app.runner.Close()

	rec, data := healthRequest(t, engine, "/readyz")
	if rec.Code != http.StatusOK || data.Status != "ready" {
		t.Fatalf("closed runner must not fail readiness: status=%d/%q body=%s", rec.Code, data.Status, rec.Body.String())
	}
	check := data.Checks["ai"]
	assertAILegacyFields(t, check)
	if check["status"] != ai.HealthUnavailable || check["closed"] != true || check["healthy"] != false {
		t.Fatalf("closed ai check=%+v", check)
	}
	if !hasAlert(data, "ai runner closed") {
		t.Errorf("missing closed alert in %+v", data.Alerts)
	}
	assertNoSecrets(t, rec.Body.String(), secrets)
}
