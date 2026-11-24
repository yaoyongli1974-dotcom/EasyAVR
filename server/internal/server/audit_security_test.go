package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/auth"
	"github.com/easyavr/easyavr/internal/store"
)

// TestAuditDoesNotLeakRequestBody verifies that audit entries never persist raw
// request bodies (which may carry passwords) and that the body still reaches the
// handler untouched.
func TestAuditDoesNotLeakRequestBody(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-secret", "protocol": "rtsp", "ip": "10.0.0.77",
		"username": "camera", "password": "dev-pass-123",
	}, &devResp)
	if devResp.Code != 0 {
		t.Fatalf("create device failed: %+v", devResp)
	}
	var dev struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if dev.ID == 0 {
		t.Fatalf("device not created (body may have been consumed): %+v", devResp)
	}

	var userResp apiResp
	doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "auditpwuser", "nickname": "Audit PW", "password": "user-pass-456", "role": "viewer",
	}, &userResp)
	if userResp.Code != 0 {
		t.Fatalf("create user failed: %+v", userResp)
	}

	// The device must really exist, proving the request body was not consumed by
	// the audit middleware.
	var gotDev apiResp
	status := doJSON(t, http.MethodGet, B+"/devices/"+uintToString(dev.ID), admin, nil, &gotDev)
	if status != http.StatusOK || gotDev.Code != 0 {
		t.Fatalf("device lookup failed: status=%d resp=%+v", status, gotDev)
	}

	var auditResp apiResp
	doJSON(t, http.MethodGet, B+"/audit/logs", admin, nil, &auditResp)
	var page struct {
		Items []struct {
			Method      string `json:"method"`
			Path        string `json:"path"`
			Action      string `json:"action"`
			Resource    string `json:"resource"`
			Result      string `json:"result"`
			RequestBody string `json:"requestBody"`
		} `json:"items"`
		Total int `json:"total"`
	}
	json.Unmarshal(auditResp.Data, &page)
	if page.Total == 0 {
		t.Fatalf("expected audit entries, got none: %+v", auditResp)
	}

	foundDevice, foundUser := false, false
	for _, item := range page.Items {
		if item.RequestBody != "" {
			t.Fatalf("audit entry leaked a request body: %+v", item)
		}
		if item.Resource == "devices" && item.Action == "create" &&
			item.Method == http.MethodPost && item.Path == "/api/v1/devices" && item.Result == "success" {
			foundDevice = true
		}
		if item.Resource == "users" && item.Action == "create" &&
			item.Method == http.MethodPost && item.Path == "/api/v1/users" && item.Result == "success" {
			foundUser = true
		}
	}
	if !foundDevice {
		t.Fatalf("device create audit entry missing: %+v", page.Items)
	}
	if !foundUser {
		t.Fatalf("user create audit entry missing: %+v", page.Items)
	}
}

// TestAuditWriteFailureDoesNotFailRequest verifies a failing audit insert is
// logged but does not change the business response.
func TestAuditWriteFailureDoesNotFailRequest(t *testing.T) {
	srv, baseCfg := newTestServer(t)
	token := login(t, srv.URL)

	cfg := *baseCfg
	cfg.DBPath = filepath.Join(t.TempDir(), "audit-fail.db")
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.Seed(db, cfg.AdminUser, cfg.AdminPass); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatalf("drop audit_logs: %v", err)
	}

	app := &App{
		cfg: &cfg,
		db:  db,
		jwt: auth.NewManager(cfg.JWTSecret, cfg.JWTExpireH),
	}
	engine := gin.New()
	engine.Use(app.authRequired(), app.auditLog())
	engine.GET("/api/v1/ping", func(c *gin.Context) {
		ok(c, gin.H{"pong": true})
	})
	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)

	var resp apiResp
	status := doJSON(t, http.MethodGet, ts.URL+"/api/v1/ping", token, nil, &resp)
	if status != http.StatusOK || resp.Code != 0 {
		t.Fatalf("request failed despite audit error: status=%d resp=%+v", status, resp)
	}
}

func uintToString(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}
