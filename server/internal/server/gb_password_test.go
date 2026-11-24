package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func gbConfigData(t *testing.T, base, token string) map[string]any {
	t.Helper()
	var r apiResp
	if code := doJSON(t, http.MethodGet, base+"/api/v1/gb/config", token, nil, &r); code != http.StatusOK {
		t.Fatalf("GET /gb/config status = %d, body = %+v", code, r)
	}
	var data map[string]any
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatalf("decode gb config: %v", err)
	}
	return data
}

// TestGBPasswordNotExposedAndAdminRotation covers EASYAVR-TECH-001 TASK-004:
// the SIP password must never be serialized, and only admins may rotate it.
func TestGBPasswordNotExposedAndAdminRotation(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Initial state: the key is gone, only the configured flag remains.
	before := gbConfigData(t, srv.URL, admin)
	if _, exists := before["password"]; exists {
		t.Fatalf("GET /gb/config still exposes password: %+v", before)
	}
	if _, exists := before["passwordConfigured"]; !exists {
		t.Fatalf("GET /gb/config missing passwordConfigured: %+v", before)
	}

	// Normal (non-admin) user with the operator role, which passes the existing
	// gb28181 policy guard and may read config.
	var created apiResp
	if code := doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "gbop", "password": "gbop-pass", "role": "operator",
	}, &created); code != http.StatusOK {
		t.Fatalf("create operator user status = %d, body = %+v", code, created)
	}
	operator := loginAs(t, srv.URL, "gbop", "gbop-pass")
	if operator == "" {
		t.Fatal("operator login returned empty token")
	}

	// Non-admins are rejected by the admin-only route even with the GB policy.
	var denied apiResp
	if code := doJSON(t, http.MethodPut, B+"/gb/config/password", operator, map[string]string{
		"password": "Hacked123",
	}, &denied); code != http.StatusForbidden {
		t.Fatalf("non-admin rotation status = %d, want 403 (body=%+v)", code, denied)
	}
	if denied.Code != http.StatusForbidden {
		t.Fatalf("non-admin rotation code = %d, want %d", denied.Code, http.StatusForbidden)
	}
	if got := gbConfigData(t, srv.URL, admin); got["passwordConfigured"] != false {
		t.Fatalf("denied rotation changed state: %+v", got)
	}

	// Admin validation: empty, whitespace-only and over-long values are rejected.
	badBodies := []struct {
		name    string
		body    map[string]string
		message string
	}{
		{"empty", map[string]string{"password": ""}, "密码不能为空"},
		{"blank", map[string]string{"password": "   "}, "密码不能为空"},
		{"tooLong", map[string]string{"password": strings.Repeat("a", 129)}, "128"},
	}
	for _, tc := range badBodies {
		t.Run("adminRejects/"+tc.name, func(t *testing.T) {
			var r apiResp
			if code := doJSON(t, http.MethodPut, B+"/gb/config/password", admin, tc.body, &r); code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%+v)", code, r)
			}
			if !strings.Contains(r.Message, tc.message) {
				t.Fatalf("message = %q, want contains %q", r.Message, tc.message)
			}
		})
	}

	// Admin rotation succeeds; surrounding whitespace is trimmed.
	var upd apiResp
	if code := doJSON(t, http.MethodPut, B+"/gb/config/password", admin, map[string]string{
		"password": "  NewSipPass123  ",
	}, &upd); code != http.StatusOK {
		t.Fatalf("admin rotation status = %d, want 200 (body=%+v)", code, upd)
	}
	if upd.Code != 0 {
		t.Fatalf("admin rotation code = %d, want 0", upd.Code)
	}
	var note struct {
		Updated bool   `json:"updated"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal(upd.Data, &note); err != nil {
		t.Fatalf("decode rotation data: %v", err)
	}
	if !note.Updated || note.Note == "" {
		t.Fatalf("unexpected rotation response: %+v", note)
	}
	if strings.Contains(note.Note, "NewSipPass123") {
		t.Fatal("rotation response echoes the password")
	}

	// Both admin and operator can still read config, without the password.
	for name, token := range map[string]string{"admin": admin, "operator": operator} {
		data := gbConfigData(t, srv.URL, token)
		if _, exists := data["password"]; exists {
			t.Fatalf("%s: GET /gb/config still exposes password: %+v", name, data)
		}
		if configured, ok := data["passwordConfigured"].(bool); !ok || !configured {
			t.Fatalf("%s: passwordConfigured = %v, want true", name, data["passwordConfigured"])
		}
		for _, key := range []string{"enabled", "running", "id", "realm", "listen", "rtpIp", "registered"} {
			if _, exists := data[key]; !exists {
				t.Fatalf("%s: GET /gb/config missing %q: %+v", name, key, data)
			}
		}
	}

	// Platform config must not leak the password anywhere either.
	var plat apiResp
	if code := doJSON(t, http.MethodGet, B+"/config/platform", admin, nil, &plat); code != http.StatusOK {
		t.Fatalf("GET /config/platform status = %d, body = %+v", code, plat)
	}
	raw := string(plat.Data)
	if strings.Contains(raw, `"password":`) {
		t.Fatalf("GET /config/platform leaks password: %s", raw)
	}
	if !strings.Contains(raw, `"passwordConfigured":true`) {
		t.Fatalf("GET /config/platform missing passwordConfigured=true: %s", raw)
	}
}
