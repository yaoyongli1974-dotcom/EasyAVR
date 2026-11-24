package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/config"
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
func newHealthTestApp(t *testing.T, thresholds storage.Config) (*App, *gin.Engine, []string) {
	t.Helper()
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
	st := storage.New(db, thresholds)
	st.SetDirs(map[string]string{
		"data":       dir,
		"snapshots":  dir,
		"certs":      dir,
		"recordings": dir,
	})

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
		cfg.Cluster.Secret, cfg.Map.AMapKey, cfg.DBPath,
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
