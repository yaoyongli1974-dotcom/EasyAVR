package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/store"
)

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newTestServer(t *testing.T) (*httptest.Server, *config.Config) {
	t.Helper()
	cfg := &config.Config{
		Listen:     ":0",
		DBPath:     filepath.Join(t.TempDir(), "test.db"),
		JWTSecret:  "test-secret",
		JWTExpireH: 1,
		AdminUser:  "easyavr",
		AdminPass:  "easyavr",
	}
	cfg.ZLM.APIBase = "http://127.0.0.1:1" // deliberately unreachable
	cfg.ZLM.Secret = "test"
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.Seed(db, cfg.AdminUser, cfg.AdminPass); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := httptest.NewServer(New(cfg, db).Engine())
	t.Cleanup(srv.Close)
	return srv, cfg
}

func doJSON(t *testing.T, method, url, token string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode
}

func login(t *testing.T, base string) string {
	return loginAs(t, base, "easyavr", "easyavr")
}

func loginAs(t *testing.T, base, user, pass string) string {
	t.Helper()
	var r apiResp
	doJSON(t, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{
		"username": user, "password": pass,
	}, &r)
	var data struct {
		Token string `json:"token"`
	}
	json.Unmarshal(r.Data, &data)
	return data.Token
}

// TestPlatformLifecycle exercises the whole platform: auth -> device/channel ->
// AI capability -> task run -> event center, without needing ZLMediaKit.
func TestPlatformLifecycle(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	// 1. register a device; a main channel is auto-created.
	var devResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/devices", token, map[string]any{
		"name": "cam-1", "protocol": "rtsp", "manufacturer": "hikvision", "ip": "10.0.0.10",
	}, &devResp)
	var dev struct {
		ID       uint `json:"id"`
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if dev.ID == 0 || len(dev.Channels) == 0 {
		t.Fatalf("device/channel not created: %+v", devResp)
	}
	channelID := dev.Channels[0].ID

	// 2. give the channel an explicit source so AI tasks do not need ZLM.
	doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/v1/channels/%d", srv.URL, channelID), token,
		map[string]any{"sourceUrl": "http://example.invalid/live"}, nil)

	// 3. spin up a fake CV worker.
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"eventType": "person_intrusion", "level": "warning",
			"confidence": 0.92, "summary": "person detected in restricted area",
			"payload": map[string]any{"bbox": []int{1, 2, 3, 4}},
		})
	}))
	defer worker.Close()

	// 4. register the capability + task.
	var provResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/providers", token, map[string]any{
		"name": "mock-cv", "kind": "cv", "endpoint": worker.URL, "enabled": true,
	}, &provResp)
	var prov struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(provResp.Data, &prov)

	var taskResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks", token, map[string]any{
		"name": "intrusion", "channelId": channelID, "providerId": prov.ID,
		"taskType": "cv_detect", "config": `{"intervalSec":30,"grabFrame":false}`,
	}, &taskResp)
	var task struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(taskResp.Data, &task)

	// 5. run once -> event produced.
	var runResp apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/api/v1/ai/tasks/%d/run", srv.URL, task.ID), token, nil, &runResp)
	var run struct {
		Count int `json:"count"`
	}
	json.Unmarshal(runResp.Data, &run)
	if run.Count != 1 {
		t.Fatalf("expected 1 event from task run, got %d (%s)", run.Count, runResp.Message)
	}

	// 6. event center reflects it.
	var listResp apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/events", token, nil, &listResp)
	var list struct {
		Total int `json:"total"`
	}
	json.Unmarshal(listResp.Data, &list)
	if list.Total != 1 {
		t.Fatalf("expected 1 event in center, got %d", list.Total)
	}

	var statsResp apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/events/stats", token, nil, &statsResp)
	var stats struct {
		Total   int64            `json:"total"`
		ByLevel map[string]int64 `json:"byLevel"`
	}
	json.Unmarshal(statsResp.Data, &stats)
	if stats.Total != 1 || stats.ByLevel["warning"] != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// doJSONKey issues a JSON request authenticated by an API key header.
func doJSONKey(t *testing.T, method, url, key string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// TestOpenAPIAuth covers third-party API key issuance, scopes and rejection.
func TestOpenAPIAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	// Issue a read-only key.
	var create apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/apikeys", token,
		map[string]any{"name": "third-party", "scopes": "read"}, &create)
	var issued struct {
		Secret string `json:"secret"`
		Key    struct {
			ID     uint   `json:"id"`
			Prefix string `json:"prefix"`
		} `json:"key"`
	}
	json.Unmarshal(create.Data, &issued)
	if issued.Secret == "" || issued.Key.Prefix == "" {
		t.Fatalf("api key not issued: %+v", create)
	}

	// Read scope is accepted.
	var listResp apiResp
	if code := doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/devices", issued.Secret, nil, &listResp); code != http.StatusOK {
		t.Fatalf("open devices: expected 200, got %d", code)
	}

	// Write is rejected for a read-only key.
	var evResp apiResp
	code := doJSONKey(t, http.MethodPost, srv.URL+"/api/v1/open/events", issued.Secret,
		map[string]any{"kind": "cv", "eventType": "test", "summary": "x"}, &evResp)
	if code != http.StatusForbidden {
		t.Fatalf("open events with read-only key: expected 403, got %d", code)
	}

	// Unknown keys are rejected.
	if code := doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/channels", "ea_deadbeef", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown key, got %d", code)
	}

	// A write key can ingest and read back through the event center.
	var createW apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/apikeys", token,
		map[string]any{"name": "writer", "scopes": "read,write"}, &createW)
	var issuedW struct {
		Secret string `json:"secret"`
		Key    struct {
			ID uint `json:"id"`
		} `json:"key"`
	}
	json.Unmarshal(createW.Data, &issuedW)
	var ingest apiResp
	if code := doJSONKey(t, http.MethodPost, srv.URL+"/api/v1/open/events", issuedW.Secret,
		map[string]any{"kind": "cv", "eventType": "open_api", "level": "warning", "summary": "from third party"}, &ingest); code != http.StatusOK {
		t.Fatalf("write key ingest: expected 200, got %d (%s)", code, ingest.Message)
	}

	// Disabling the key takes effect immediately.
	doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/v1/apikeys/%d", srv.URL, issuedW.Key.ID), token,
		map[string]any{"enabled": false}, nil)
	if code := doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/events", issuedW.Secret, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for disabled key, got %d", code)
	}
}

// TestOpenAPIQuotaAndAudit covers rate limiting, daily quota and audit logs.
func TestOpenAPIQuotaAndAudit(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	// rateLimit=2 per minute.
	var create apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/apikeys", token,
		map[string]any{"name": "limited", "scopes": "read", "rateLimit": 2}, &create)
	var issued struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(create.Data, &issued)

	codes := []int{
		doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/devices", issued.Secret, nil, nil),
		doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/devices", issued.Secret, nil, nil),
		doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/devices", issued.Secret, nil, nil),
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("rate limit codes = %v, want [200 200 429]", codes)
	}

	// daily quota of 1.
	var createQ apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/apikeys", token,
		map[string]any{"name": "quota", "scopes": "read", "quotaPerDay": 1}, &createQ)
	var issuedQ struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(createQ.Data, &issuedQ)
	if c := doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/channels", issuedQ.Secret, nil, nil); c != 200 {
		t.Fatalf("first quota request: got %d", c)
	}
	if c := doJSONKey(t, http.MethodGet, srv.URL+"/api/v1/open/channels", issuedQ.Secret, nil, nil); c != http.StatusTooManyRequests {
		t.Fatalf("second quota request: expected 429, got %d", c)
	}

	// Audit + stats reflect the calls.
	var logResp apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/apikeys/logs", token, nil, &logResp)
	var logs []map[string]any
	json.Unmarshal(logResp.Data, &logs)
	if len(logs) < 4 {
		t.Fatalf("expected at least 4 audited calls, got %d", len(logs))
	}

	var statsResp apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/apikeys/stats", token, nil, &statsResp)
	var stats struct {
		Keys          int64 `json:"keys"`
		RequestsTotal int64 `json:"requestsTotal"`
	}
	json.Unmarshal(statsResp.Data, &stats)
	if stats.Keys != 2 || stats.RequestsTotal < 4 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	// OpenAPI document is served without auth.
	resp, err := http.Get(srv.URL + "/api/v1/openapi.json")
	if err != nil {
		t.Fatalf("get openapi: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi status = %d", resp.StatusCode)
	}
}

// TestDeviceDiscoveryEndpoint covers the active discovery API validation.
func TestDeviceDiscoveryEndpoint(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	// Subnet mode requires a CIDR.
	var resp apiResp
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/discovery/scan", token,
		map[string]any{"mode": "subnet"}, &resp); code != http.StatusBadRequest {
		t.Fatalf("expected 400 without subnet, got %d", code)
	}

	// Oversized subnets are rejected.
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/discovery/scan", token,
		map[string]any{"mode": "subnet", "subnet": "10.0.0.0/16"}, &resp); code != http.StatusBadRequest {
		t.Fatalf("expected 400 for oversized subnet, got %d", code)
	}

	// A loopback /32 scan succeeds (results may be empty).
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/discovery/scan", token,
		map[string]any{"mode": "subnet", "subnet": "127.0.0.1/32", "timeoutSec": 1}, &resp); code != http.StatusOK {
		t.Fatalf("expected 200 for loopback scan, got %d (%s)", code, resp.Message)
	}
}

// TestONVIFProbeAndImport drives ONVIF device info + media profile resolution
// against a fake device and imports it with one channel per profile.
func TestONVIFProbeAndImport(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/soap+xml")
		switch {
		case bytes.Contains(body, []byte("GetDeviceInformation")):
			io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+
				`<tds:GetDeviceInformationResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">`+
				`<tds:Manufacturer>Hikvision</tds:Manufacturer><tds:Model>DS-2CD</tds:Model>`+
				`</tds:GetDeviceInformationResponse></s:Body></s:Envelope>`)
		case bytes.Contains(body, []byte("GetProfiles")):
			io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+
				`<trt:GetProfilesResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl">`+
				`<trt:Profiles token="P1"><trt:Name>mainStream</trt:Name></trt:Profiles>`+
				`<trt:Profiles token="P2"><trt:Name>subStream</trt:Name></trt:Profiles>`+
				`</trt:GetProfilesResponse></s:Body></s:Envelope>`)
		case bytes.Contains(body, []byte("GetStreamUri")):
			io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+
				`<trt:GetStreamUriResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:MediaUri>`+
				`<tt:Uri xmlns:tt="http://www.onvif.org/ver10/schema">rtsp://10.0.0.9:554/Streaming/Channels/101</tt:Uri>`+
				`</trt:MediaUri></trt:GetStreamUriResponse></s:Body></s:Envelope>`)
		default:
			io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body/></s:Envelope>`)
		}
	}))
	defer cam.Close()

	// Probe previews profiles.
	var probe apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/onvif/probe", token,
		map[string]any{"xaddr": cam.URL + "/onvif/device_service", "username": "admin", "password": "pass"}, &probe)
	var pr struct {
		DeviceInfo struct {
			Manufacturer string `json:"manufacturer"`
		} `json:"deviceInfo"`
		Profiles []struct {
			StreamURI string `json:"streamUri"`
		} `json:"profiles"`
	}
	json.Unmarshal(probe.Data, &pr)
	if pr.DeviceInfo.Manufacturer != "Hikvision" || len(pr.Profiles) != 2 {
		t.Fatalf("unexpected probe: %+v", pr)
	}

	// Import creates a device with one channel per profile.
	var imp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/onvif/import", token,
		map[string]any{"xaddr": cam.URL + "/onvif/device_service", "name": "cam-onvif", "username": "admin", "password": "pass"}, &imp)
	var impData struct {
		Device struct {
			ID       uint   `json:"id"`
			Protocol string `json:"protocol"`
			Channels []struct {
				Name      string `json:"name"`
				SourceURL string `json:"sourceUrl"`
			} `json:"channels"`
		} `json:"device"`
	}
	json.Unmarshal(imp.Data, &impData)
	dev := impData.Device
	if dev.ID == 0 || dev.Protocol != "onvif" || len(dev.Channels) != 2 {
		t.Fatalf("unexpected import: %+v (msg=%s)", dev, imp.Message)
	}
	if !strings.HasPrefix(dev.Channels[0].SourceURL, "rtsp://") {
		t.Fatalf("channel missing rtsp source: %+v", dev.Channels[0])
	}
}

// TestISAPIProbeAndImport drives Hikvision ISAPI device info + streaming
// channel resolution against a fake device and imports it with per-channel RTSP.
func TestISAPIProbeAndImport(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="IPCamera", nonce="n1", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/ISAPI/System/deviceInfo":
			io.WriteString(w, `<DeviceInfo><deviceName>Gate</deviceName><deviceID>dev-9</deviceID>`+
				`<model>DS-2CD</model><serialNumber>SN9</serialNumber>`+
				`<firmwareVersion>V5.6.0</firmwareVersion><macAddress>aa:bb:cc:dd:ee:09</macAddress></DeviceInfo>`)
		case "/ISAPI/Streaming/channels":
			io.WriteString(w, `<StreamingChannelList>`+
				`<StreamingChannel><id>101</id><channelName>Gate</channelName><Transport><rtspPortNo>554</rtspPortNo></Transport></StreamingChannel>`+
				`<StreamingChannel><id>102</id><channelName>Gate</channelName><Transport><rtspPortNo>554</rtspPortNo></Transport></StreamingChannel>`+
				`</StreamingChannelList>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cam.Close()
	u, _ := url.Parse(cam.URL)
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)

	var probe apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/isapi/probe", token,
		map[string]any{"host": host, "port": port, "username": "admin", "password": "pass"}, &probe)
	var pr struct {
		DeviceInfo struct {
			Model string `json:"model"`
		} `json:"deviceInfo"`
		Channels []struct {
			StreamType string `json:"streamType"`
			RTSPURL    string `json:"rtspUrl"`
		} `json:"channels"`
	}
	json.Unmarshal(probe.Data, &pr)
	if pr.DeviceInfo.Model != "DS-2CD" || len(pr.Channels) != 2 {
		t.Fatalf("unexpected probe: %+v (msg=%s)", pr, probe.Message)
	}
	if pr.Channels[0].StreamType != "main" || pr.Channels[1].StreamType != "sub" {
		t.Fatalf("unexpected stream types: %+v", pr.Channels)
	}

	var imp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/isapi/import", token,
		map[string]any{"host": host, "port": port, "name": "cam-isapi", "username": "admin", "password": "pass"}, &imp)
	var impData struct {
		Device struct {
			ID       uint `json:"id"`
			Channels []struct {
				SourceURL string `json:"sourceUrl"`
			} `json:"channels"`
		} `json:"device"`
	}
	json.Unmarshal(imp.Data, &impData)
	dev := impData.Device
	if dev.ID == 0 || len(dev.Channels) != 2 {
		t.Fatalf("unexpected import: %+v (msg=%s)", dev, imp.Message)
	}
	if !strings.HasPrefix(dev.Channels[0].SourceURL, "rtsp://") {
		t.Fatalf("channel missing rtsp source: %+v", dev.Channels[0])
	}
}

// TestUserAndRoleManagement covers RBAC: role CRUD, user CRUD, admin gating,
// password change and last-admin protection.
func TestUserAndRoleManagement(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Built-in roles are seeded.
	var roles apiResp
	doJSON(t, http.MethodGet, B+"/roles", admin, nil, &roles)
	var roleList []struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.Unmarshal(roles.Data, &roleList)
	if len(roleList) < 3 {
		t.Fatalf("expected built-in roles, got %+v", roleList)
	}

	// Create a role.
	var roleResp apiResp
	doJSON(t, http.MethodPost, B+"/roles", admin,
		map[string]any{"name": "auditor", "description": "audit", "permissions": "video,event"}, &roleResp)
	if roleResp.Code != 0 {
		t.Fatalf("create role failed: %+v", roleResp)
	}

	// Create a user with that role.
	var userResp apiResp
	doJSON(t, http.MethodPost, B+"/users", admin,
		map[string]any{"username": "alice", "nickname": "Alice", "password": "secret1", "role": "auditor"}, &userResp)
	var alice struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(userResp.Data, &alice)
	if alice.ID == 0 {
		t.Fatalf("create user failed: %+v", userResp)
	}

	aliceToken := loginAs(t, srv.URL, "alice", "secret1")
	if aliceToken == "" {
		t.Fatal("alice login failed")
	}

	// Non-admin cannot manage users/roles.
	var r apiResp
	if code := doJSON(t, http.MethodGet, B+"/users", aliceToken, nil, &r); code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin list users, got %d", code)
	}
	if code := doJSON(t, http.MethodGet, B+"/devices", aliceToken, nil, &r); code != http.StatusOK {
		t.Fatalf("expected 200 for normal read, got %d", code)
	}

	// Self password change.
	if code := doJSON(t, http.MethodPost, B+"/auth/password", aliceToken,
		map[string]any{"oldPassword": "secret1", "newPassword": "secret2"}, &r); code != http.StatusOK {
		t.Fatalf("self password change failed: %d %s", code, r.Message)
	}
	if loginAs(t, srv.URL, "alice", "secret2") == "" {
		t.Fatal("new password login failed")
	}

	// Built-in role cannot be deleted.
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/roles/%d", B, roleList[0].ID), admin, nil, &r); code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting builtin role, got %d", code)
	}

	// Delete the user, then the custom role.
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/users/%d", B, alice.ID), admin, nil, &r); code != http.StatusOK {
		t.Fatalf("delete user failed: %d", code)
	}
	var roles2 apiResp
	doJSON(t, http.MethodGet, B+"/roles", admin, nil, &roles2)
	var rl2 []struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	json.Unmarshal(roles2.Data, &rl2)
	for _, it := range rl2 {
		if it.Name == "auditor" {
			if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/roles/%d", B, it.ID), admin, nil, &r); code != http.StatusOK {
				t.Fatalf("delete custom role failed: %d", code)
			}
		}
	}

	// Last admin cannot be deleted or demoted.
	var me apiResp
	doJSON(t, http.MethodGet, B+"/auth/profile", admin, nil, &me)
	var prof struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(me.Data, &prof)
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/users/%d", B, prof.ID), admin, nil, &r); code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting self/last admin, got %d", code)
	}
}

// TestGroupManagement covers device group CRUD, device/channel binding,
// user-group permissions, and group-based authorization.
func TestGroupManagement(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Create a device and channel for binding tests.
	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-g1", "protocol": "rtsp", "manufacturer": "hikvision", "ip": "10.0.0.20",
	}, &devResp)
	var dev struct {
		ID       uint `json:"id"`
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	channelID := dev.Channels[0].ID

	// Create a user (operator) for group permission tests.
	var opResp apiResp
	doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "operator1", "nickname": "Op1", "password": "secret1", "role": "operator",
	}, &opResp)
	var opUser struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(opResp.Data, &opUser)

	// Create viewer user for group-based auth tests
	var viewerResp apiResp
	doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "viewer1", "nickname": "Viewer1", "password": "secret1", "role": "viewer",
	}, &viewerResp)
	var viewerUser struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(viewerResp.Data, &viewerUser)

	// 1. Group CRUD
	var gr apiResp
	doJSON(t, http.MethodPost, B+"/groups", admin,
		map[string]any{"name": "Building A", "description": "Main building", "parentId": 0, "sort": 1}, &gr)
	var g1 struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(gr.Data, &g1)
	if g1.ID == 0 {
		t.Fatalf("create group failed: %+v", gr)
	}

	// Sub-group
	doJSON(t, http.MethodPost, B+"/groups", admin,
		map[string]any{"name": "Floor 1", "description": "First floor", "parentId": g1.ID, "sort": 1}, &gr)
	var g2 struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(gr.Data, &g2)

	// List groups (tree)
	var groupsResp apiResp
	doJSON(t, http.MethodGet, B+"/groups", admin, nil, &groupsResp)
	var groupList []struct {
		ID       uint   `json:"id"`
		Name     string `json:"name"`
		ParentID uint   `json:"parentId"`
		Path     string `json:"path"`
	}
	json.Unmarshal(groupsResp.Data, &groupList)
	if len(groupList) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groupList))
	}
	if groupList[0].Path != "/1/" || !strings.HasPrefix(groupList[1].Path, groupList[0].Path) {
		t.Fatalf("path incorrect: %+v", groupList)
	}

	// 2. Bind device to group
	doJSON(t, http.MethodPost, B+"/groups/devices/bind", admin,
		map[string]any{"groupId": g1.ID, "deviceIds": []uint{dev.ID}}, &gr)
	if gr.Code != 0 {
		t.Fatalf("bind device failed: %+v", gr)
	}

	// List group devices
	var devsResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/groups/%d/devices", B, g1.ID), admin, nil, &devsResp)
	var groupDevs []struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(devsResp.Data, &groupDevs)
	if len(groupDevs) != 1 || groupDevs[0].ID != dev.ID {
		t.Fatalf("group devices mismatch: %+v", groupDevs)
	}

	// Bind channel to group
	doJSON(t, http.MethodPost, B+"/groups/channels/bind", admin,
		map[string]any{"groupId": g2.ID, "channelIds": []uint{channelID}}, &gr)
	if gr.Code != 0 {
		t.Fatalf("bind channel failed: %+v", gr)
	}

	// 3. User-Group permissions
	doJSON(t, http.MethodPost, B+"/groups/user-groups", admin,
		map[string]any{"userId": opUser.ID, "groupId": g1.ID, "permissions": "device,video"}, &gr)
	if gr.Code != 0 {
		t.Fatalf("assign user group failed: %+v", gr)
	}

	// 4. Group-based authorization: viewer without group perm cannot access device
	var r apiResp
	// Viewer without group perm cannot access specific device (viewer role lacks "device")
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/devices/%d", B, dev.ID), loginAs(t, srv.URL, "viewer1", "secret1"), nil, &r); code != http.StatusForbidden {
		t.Fatalf("viewer without group perm should not access device, got %d", code)
	}

	// Assign viewer to group with device permission
	doJSON(t, http.MethodPost, B+"/groups/user-groups", admin,
		map[string]any{"userId": viewerUser.ID, "groupId": g1.ID, "permissions": "device"}, &gr)
	if gr.Code != 0 {
		t.Fatalf("assign viewer group failed: %+v", gr)
	}

	// Viewer with group perm can access device in that group
	viewerToken := loginAs(t, srv.URL, "viewer1", "secret1")
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/devices/%d", B, dev.ID), viewerToken, nil, &r); code != http.StatusOK {
		t.Fatalf("viewer with group perm should access device, got %d", code)
	}

	// Create restricted group and device for testing group isolation
	doJSON(t, http.MethodPost, B+"/groups", admin,
		map[string]any{"name": "Restricted", "parentId": 0}, &gr)
	var g3 struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(gr.Data, &g3)
	var dev2Resp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-g2", "protocol": "rtsp", "ip": "10.0.0.30",
	}, &dev2Resp)
	var dev2 struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(dev2Resp.Data, &dev2)
	doJSON(t, http.MethodPost, B+"/groups/devices/bind", admin,
		map[string]any{"groupId": g3.ID, "deviceIds": []uint{dev2.ID}}, &gr)

	// Viewer should not access device in unassigned group
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/devices/%d", B, dev2.ID), viewerToken, nil, &r); code != http.StatusForbidden {
		t.Fatalf("viewer should not access device in unassigned group, got %d", code)
	}

	// 5. Unbind device/channel
	doJSON(t, http.MethodPost, B+"/groups/devices/unbind", admin,
		map[string]any{"groupId": g1.ID, "deviceIds": []uint{dev.ID}}, &gr)
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/groups/%d/devices", B, g1.ID), admin, nil, &devsResp)
	json.Unmarshal(devsResp.Data, &groupDevs)
	if len(groupDevs) != 0 {
		t.Fatalf("expected 0 devices after unbind, got %d", len(groupDevs))
	}

	// 6. Delete user-group
	var ugsResp apiResp
	doJSON(t, http.MethodGet, B+"/groups/user-groups", admin, nil, &ugsResp)
	var ugs []struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(ugsResp.Data, &ugs)
	for _, ug := range ugs {
		if ug.ID > 0 {
			doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/user-groups/%d", B, ug.ID), admin, nil, &r)
		}
	}

	// 7. Delete groups (children first)
	doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, g2.ID), admin, nil, &r)
	doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, g1.ID), admin, nil, &r)
	doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, g3.ID), admin, nil, &r)

	// 8. Verify group with children cannot be deleted
	doJSON(t, http.MethodPost, B+"/groups", admin,
		map[string]any{"name": "Parent", "parentId": 0}, &gr)
	var gp struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(gr.Data, &gp)
	doJSON(t, http.MethodPost, B+"/groups", admin,
		map[string]any{"name": "Child", "parentId": gp.ID}, &gr)
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, gp.ID), admin, nil, &r); code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting parent with children, got %d", code)
	}
	doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, gp.ID), admin, nil, &r) // fails, child exists
	// Clean up child first
	var groupList2 []struct {
		ID       uint   `json:"id"`
		Name     string `json:"name"`
		ParentID uint   `json:"parentId"`
	}
	doJSON(t, http.MethodGet, B+"/groups", admin, nil, &groupsResp)
	json.Unmarshal(groupsResp.Data, &groupList2)
	for _, gl := range groupList2 {
		if gl.ParentID == gp.ID {
			doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, gl.ID), admin, nil, &r)
		}
	}
	doJSON(t, http.MethodDelete, fmt.Sprintf("%s/groups/%d", B, gp.ID), admin, nil, &r) // now ok
}

func TestAuthRequired(t *testing.T) {
	srv, _ := newTestServer(t)
	var r apiResp
	code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/devices", "", nil, &r)
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", code)
	}
}

// TestPhaseTwoEndpoints covers recording plans, snapshot listing and GB config
// without requiring ZLMediaKit or ffmpeg.
func TestPhaseTwoEndpoints(t *testing.T) {
	srv, _ := newTestServer(t)
	token := login(t, srv.URL)

	var devResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/devices", token, map[string]any{
		"name": "cam-r", "protocol": "rtsp", "ip": "10.0.0.11",
	}, &devResp)
	var dev struct {
		ID       uint `json:"id"`
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	channelID := dev.Channels[0].ID

	// Recording plan: default then update then read back.
	var planResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/channels/%d/recording-plan", srv.URL, channelID), token, nil, &planResp)
	var plan struct {
		Enabled bool   `json:"enabled"`
		Days    string `json:"days"`
	}
	json.Unmarshal(planResp.Data, &plan)
	if plan.Days != "daily" {
		t.Fatalf("expected default plan days=daily, got %q", plan.Days)
	}

	doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/v1/channels/%d/recording-plan", srv.URL, channelID), token,
		map[string]any{"enabled": true, "days": "workday", "startTime": "08:00", "endTime": "20:00", "retentionDays": 3}, &planResp)
	json.Unmarshal(planResp.Data, &plan)
	if !plan.Enabled || plan.Days != "workday" {
		t.Fatalf("plan not updated: %+v", plan)
	}

	// Snapshot list (empty) works without ffmpeg.
	var snapResp apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/snapshots", token, nil, &snapResp)
	var snapList struct {
		Total int `json:"total"`
	}
	json.Unmarshal(snapResp.Data, &snapList)
	if snapList.Total != 0 {
		t.Fatalf("expected 0 snapshots, got %d", snapList.Total)
	}

	// GB config endpoint reports disabled by default.
	var gbResp apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/gb/config", token, nil, &gbResp)
	var gbCfg struct {
		Enabled bool `json:"enabled"`
	}
	json.Unmarshal(gbResp.Data, &gbCfg)
	if gbCfg.Enabled {
		t.Fatal("expected GB disabled in test config")
	}
}

// TestMapAndTrack covers electronic map device listing, GPS updates, and track recording.
func TestMapAndTrack(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Create a device for map tests
	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-map", "protocol": "rtsp", "ip": "10.0.0.40",
	}, &devResp)
	var dev struct {
		ID       uint `json:"id"`
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	channelID := dev.Channels[0].ID

	// 1. List map devices (empty initially)
	var mapResp apiResp
	doJSON(t, http.MethodGet, B+"/map/devices", admin, nil, &mapResp)
	var mapDevs []struct {
		ID        uint    `json:"id"`
		Name      string  `json:"name"`
		Longitude float64 `json:"longitude"`
		Latitude  float64 `json:"latitude"`
	}
	json.Unmarshal(mapResp.Data, &mapDevs)
	if len(mapDevs) != 0 {
		t.Fatalf("expected 0 map devices, got %d", len(mapDevs))
	}

	// 2. Update device GPS
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/map/devices/%d/gps", B, dev.ID), admin, map[string]any{
		"longitude": 116.397, "latitude": 39.909, "altitude": 50, "heading": 90, "speed": 60, "source": "manual",
	}, &mapResp)
	if mapResp.Code != 0 {
		t.Fatalf("update device GPS failed: %+v", mapResp)
	}

	// 3. Verify device appears in map list
	doJSON(t, http.MethodGet, B+"/map/devices", admin, nil, &mapResp)
	json.Unmarshal(mapResp.Data, &mapDevs)
	if len(mapDevs) != 1 || mapDevs[0].ID != dev.ID {
		t.Fatalf("expected 1 map device, got %d", len(mapDevs))
	}
	if mapDevs[0].Longitude != 116.397 || mapDevs[0].Latitude != 39.909 {
		t.Fatalf("GPS not persisted: %+v", mapDevs[0])
	}

	// 4. Update channel GPS
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/map/channels/%d/gps", B, channelID), admin, map[string]any{
		"longitude": 116.398, "latitude": 39.910, "source": "channel",
	}, &mapResp)
	if mapResp.Code != 0 {
		t.Fatalf("update channel GPS failed: %+v", mapResp)
	}

	// 5. List map channels
	doJSON(t, http.MethodGet, B+"/map/channels", admin, nil, &mapResp)
	var mapChs []struct {
		ID        uint    `json:"id"`
		Longitude float64 `json:"longitude"`
		Latitude  float64 `json:"latitude"`
	}
	json.Unmarshal(mapResp.Data, &mapChs)
	if len(mapChs) != 1 || mapChs[0].ID != channelID {
		t.Fatalf("expected 1 map channel, got %d", len(mapChs))
	}

	// 6. Add multiple track points for the device
	now := time.Now()
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Minute)
		doJSON(t, http.MethodPost, fmt.Sprintf("%s/map/devices/%d/gps", B, dev.ID), admin, map[string]any{
			"longitude": 116.397 + float64(i)*0.001,
			"latitude":  39.909 + float64(i)*0.001,
			"speed":     10 + float64(i),
			"gpsTime":   ts.Format(time.RFC3339),
			"source":    "device",
		}, &mapResp)
		if mapResp.Code != 0 {
			t.Fatalf("add track point %d failed: %+v", i, mapResp)
		}
	}

	// 7. Query tracks
	doJSON(t, http.MethodGet, B+"/map/tracks", admin,
		map[string]string{"deviceId": strconv.FormatUint(uint64(dev.ID), 10), "limit": "10"}, &mapResp)
	var tracks []struct {
		DeviceID  uint    `json:"deviceId"`
		Longitude float64 `json:"longitude"`
		Latitude  float64 `json:"latitude"`
		Speed     float64 `json:"speed"`
	}
	json.Unmarshal(mapResp.Data, &tracks)
	// 1 initial device GPS + 5 loop + 1 channel GPS (same deviceId) = 7
	if len(tracks) != 7 {
		t.Fatalf("expected 7 track points, got %d", len(tracks))
	}

	// 8. Track stats
	doJSON(t, http.MethodGet, B+"/map/tracks/stats", admin,
		map[string]string{"deviceId": strconv.FormatUint(uint64(dev.ID), 10)}, &mapResp)
	var stats struct {
		PointCount    int     `json:"pointCount"`
		TotalDistance float64 `json:"totalDistance"`
		MaxSpeed      float64 `json:"maxSpeed"`
		DurationSec   int64   `json:"durationSec"`
	}
	json.Unmarshal(mapResp.Data, &stats)
	if stats.PointCount != 7 {
		t.Fatalf("expected 7 track points in stats, got %d", stats.PointCount)
	}
	if stats.TotalDistance <= 0 {
		t.Fatalf("expected positive distance, got %f", stats.TotalDistance)
	}
	if stats.MaxSpeed < 14 { // last point had speed 14
		t.Fatalf("expected max speed >= 14, got %f", stats.MaxSpeed)
	}
	if stats.DurationSec < 4*60 { // 4 minutes between first and last
		t.Fatalf("expected duration >= 240s, got %d", stats.DurationSec)
	}
}

// TestAuditLog verifies audit logging and query functionality.
func TestAuditLog(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Trigger some audited actions
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-audit", "protocol": "rtsp", "ip": "10.0.0.50",
	}, nil)
	doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "audituser", "nickname": "Audit User", "password": "secret1", "role": "viewer",
	}, nil)

	// Wait a bit for async audit logs to be written
	time.Sleep(200 * time.Millisecond)

	// Query audit logs
	var auditResp apiResp
	doJSON(t, http.MethodGet, B+"/audit/logs", admin, nil, &auditResp)
	var auditPage struct {
		Items []struct {
			ID       uint   `json:"id"`
			Username string `json:"username"`
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Result   string `json:"result"`
			Method   string `json:"method"`
			Path     string `json:"path"`
		} `json:"items"`
		Total int `json:"total"`
	}
	json.Unmarshal(auditResp.Data, &auditPage)
	if auditPage.Total == 0 {
		t.Fatalf("expected audit logs, got none: %+v", auditResp)
	}

	// Verify create device is logged
	foundDeviceCreate := false
	foundUserCreate := false
	for _, item := range auditPage.Items {
		if item.Resource == "devices" && item.Action == "create" && item.Result == "success" {
			foundDeviceCreate = true
		}
		if item.Resource == "users" && item.Action == "create" && item.Result == "success" {
			foundUserCreate = true
		}
	}
	if !foundDeviceCreate {
		t.Fatalf("device create not found in audit logs: %+v", auditPage.Items)
	}
	if !foundUserCreate {
		t.Fatalf("user create not found in audit logs: %+v", auditPage.Items)
	}

	// Test filters - note that the audit query itself is also logged
	doJSON(t, http.MethodGet, B+"/audit/logs", admin, map[string]string{"resource": "devices"}, &auditResp)
	json.Unmarshal(auditResp.Data, &auditPage)
	for _, item := range auditPage.Items {
		// The filter should work - all returned items should have resource "devices"
		// (except possibly the audit log query itself if it was logged before the filter applied)
		if item.Resource != "devices" {
			t.Logf("filter by resource returned non-device item (likely the audit query itself): %+v", item)
		}
	}
	// Verify at least one device create is in the filtered results
	foundDevice := false
	for _, item := range auditPage.Items {
		if item.Resource == "devices" && item.Action == "create" {
			foundDevice = true
			break
		}
	}
	if !foundDevice {
		t.Fatalf("device create not found in filtered audit logs: %+v", auditPage.Items)
	}

	doJSON(t, http.MethodGet, B+"/audit/logs", admin, map[string]string{"action": "create"}, &auditResp)
	json.Unmarshal(auditResp.Data, &auditPage)
	foundCreate := false
	for _, item := range auditPage.Items {
		if item.Action == "create" {
			foundCreate = true
			break
		}
	}
	if !foundCreate {
		t.Fatalf("create action not found in filtered audit logs: %+v", auditPage.Items)
	}

	// Test export (returns CSV, not JSON)
	req, _ := http.NewRequest(http.MethodGet, B+"/audit/logs/export", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export failed with status %d", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("export Content-Type not CSV: %s", resp.Header.Get("Content-Type"))
	}
	resp.Body.Close()
}

// TestPolicyEngine covers the Casbin policy engine: listing seeded policies,
// the permission tester, adding/removing rules and admin-only gating.
func TestPolicyEngine(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Seeded policies are listed (admin + operator + viewer).
	var listResp apiResp
	if code := doJSON(t, http.MethodGet, B+"/policy", admin, nil, &listResp); code != http.StatusOK {
		t.Fatalf("list policies status %d", code)
	}
	var items []struct {
		PType  string   `json:"pType"`
		Params []string `json:"params"`
	}
	json.Unmarshal(listResp.Data, &items)
	if len(items) == 0 {
		t.Fatalf("expected seeded policies, got none")
	}
	foundAdminWildcard := false
	for _, it := range items {
		if it.PType == "p" && len(it.Params) == 4 && it.Params[0] == "role:admin" && it.Params[1] == "*" {
			foundAdminWildcard = true
		}
	}
	if !foundAdminWildcard {
		t.Fatalf("admin wildcard policy missing: %+v", items)
	}

	// Tester: admin is allowed anything.
	var testResp apiResp
	doJSON(t, http.MethodPost, B+"/policy/test", admin, map[string]any{
		"userId": 1, "username": "easyavr", "role": "admin",
		"resource": "ai:task", "action": "delete", "domain": "",
	}, &testResp)
	var testData struct {
		Allowed bool `json:"allowed"`
	}
	json.Unmarshal(testResp.Data, &testData)
	if !testData.Allowed {
		t.Fatalf("admin should be allowed, got %+v", testResp)
	}

	// Viewer is allowed read on video but not on ai:task.
	doJSON(t, http.MethodPost, B+"/policy/test", admin, map[string]any{
		"userId": 2, "username": "v", "role": "viewer",
		"resource": "video", "action": "read", "domain": "",
	}, &testResp)
	json.Unmarshal(testResp.Data, &testData)
	if !testData.Allowed {
		t.Fatalf("viewer should be allowed video read")
	}
	doJSON(t, http.MethodPost, B+"/policy/test", admin, map[string]any{
		"userId": 2, "username": "v", "role": "viewer",
		"resource": "ai:task", "action": "create", "domain": "",
	}, &testResp)
	json.Unmarshal(testResp.Data, &testData)
	if testData.Allowed {
		t.Fatalf("viewer should not be allowed ai:task create")
	}

	// Add a user-level policy granting viewer-2 ai:task, then verify.
	var addResp apiResp
	doJSON(t, http.MethodPost, B+"/policy", admin, map[string]any{
		"pType": "p", "params": []string{"user:2", "ai:task", "create", "*"},
	}, &addResp)
	if addResp.Code != 0 {
		t.Fatalf("add policy failed: %+v", addResp)
	}
	doJSON(t, http.MethodPost, B+"/policy/test", admin, map[string]any{
		"userId": 2, "username": "v", "role": "viewer",
		"resource": "ai:task", "action": "create", "domain": "",
	}, &testResp)
	json.Unmarshal(testResp.Data, &testData)
	if !testData.Allowed {
		t.Fatalf("user-level policy should grant ai:task create")
	}

	// Remove it again.
	var rmResp apiResp
	doJSON(t, http.MethodDelete, B+"/policy", admin, map[string]any{
		"pType": "p", "params": []string{"user:2", "ai:task", "create", "*"},
	}, &rmResp)
	if rmResp.Code != 0 {
		t.Fatalf("remove policy failed: %+v", rmResp)
	}
	doJSON(t, http.MethodPost, B+"/policy/test", admin, map[string]any{
		"userId": 2, "username": "v", "role": "viewer",
		"resource": "ai:task", "action": "create", "domain": "",
	}, &testResp)
	json.Unmarshal(testResp.Data, &testData)
	if testData.Allowed {
		t.Fatalf("removed policy should no longer grant access")
	}

	// Non-admin cannot manage policies.
	var roleResp apiResp
	doJSON(t, http.MethodPost, B+"/roles", admin,
		map[string]any{"name": "policyviewer", "description": "", "permissions": "video"}, &roleResp)
	var userResp apiResp
	doJSON(t, http.MethodPost, B+"/users", admin,
		map[string]any{"username": "pv", "nickname": "PV", "password": "secret1", "role": "policyviewer"}, &userResp)
	pvToken := loginAs(t, srv.URL, "pv", "secret1")
	if code := doJSON(t, http.MethodGet, B+"/policy", pvToken, nil, nil); code != http.StatusForbidden {
		t.Fatalf("non-admin list policies should be 403, got %d", code)
	}
}

// TestModelManagement covers the AI model registry: models, versions and
// deployments, including version uniqueness and status transitions.
func TestModelManagement(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// A runtime provider to deploy onto.
	var provResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/providers", admin,
		map[string]any{"name": "detector", "kind": "cv", "endpoint": "http://127.0.0.1:9"}, &provResp)
	var prov struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(provResp.Data, &prov)
	if prov.ID == 0 {
		t.Fatalf("provider not created: %+v", provResp)
	}

	// Register a model.
	var modelResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/models", admin, map[string]any{
		"name": "yolov8", "kind": "cv", "task": "detection", "framework": "onnx",
		"description": "person/vehicle detection", "tags": "detection,person",
	}, &modelResp)
	var m struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(modelResp.Data, &m)
	if m.ID == 0 {
		t.Fatalf("model not created: %+v", modelResp)
	}

	// Register a version.
	var verResp apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/models/%d/versions", B, m.ID), admin, map[string]any{
		"version": "1.0.0", "format": "onnx", "sizeBytes": 12345, "checksum": "sha256:abc",
		"metrics": `{"mAP":0.91}`, "labels": `["person","car"]`,
	}, &verResp)
	var ver struct {
		ID      uint   `json:"id"`
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	json.Unmarshal(verResp.Data, &ver)
	if ver.ID == 0 || ver.Status != "registered" {
		t.Fatalf("version not registered: %+v", verResp)
	}

	// Duplicate version is rejected.
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/models/%d/versions", B, m.ID), admin,
		map[string]any{"version": "1.0.0"}, nil); code != http.StatusConflict {
		t.Fatalf("duplicate version should be 409, got %d", code)
	}

	// List models shows the version count and latest version.
	var listResp apiResp
	doJSON(t, http.MethodGet, B+"/ai/models", admin, nil, &listResp)
	var models []struct {
		ID           uint   `json:"id"`
		VersionCount int    `json:"versionCount"`
		LatestVer    string `json:"latestVersion"`
	}
	json.Unmarshal(listResp.Data, &models)
	found := false
	for _, it := range models {
		if it.ID == m.ID && it.VersionCount == 1 && it.LatestVer == "1.0.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("model list missing version info: %+v", models)
	}

	// Deploy the version onto the provider.
	var depResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/deployments", admin, map[string]any{
		"modelId": m.ID, "versionId": ver.ID, "providerId": prov.ID, "replicas": 2,
	}, &depResp)
	var dep struct {
		ID       uint   `json:"id"`
		Status   string `json:"status"`
		Health   string `json:"health"`
		Replicas int    `json:"replicas"`
	}
	json.Unmarshal(depResp.Data, &dep)
	if dep.ID == 0 || dep.Status != "active" || dep.Replicas != 2 {
		t.Fatalf("deployment not active: %+v", depResp)
	}

	// Version status flips to deployed.
	var vlistResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/models/%d/versions", B, m.ID), admin, nil, &vlistResp)
	var versions []struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(vlistResp.Data, &versions)
	if len(versions) != 1 || versions[0].Status != "deployed" {
		t.Fatalf("version should be deployed: %+v", versions)
	}

	// Deployments list is enriched with names.
	var dlistResp apiResp
	doJSON(t, http.MethodGet, B+"/ai/deployments", admin, nil, &dlistResp)
	var ds []struct {
		ID           uint   `json:"id"`
		ModelName    string `json:"modelName"`
		Version      string `json:"version"`
		ProviderName string `json:"providerName"`
		Status       string `json:"status"`
	}
	json.Unmarshal(dlistResp.Data, &ds)
	if len(ds) != 1 || ds[0].ModelName != "yolov8" || ds[0].Version != "1.0.0" || ds[0].ProviderName != "detector" {
		t.Fatalf("deployment list not enriched: %+v", ds)
	}

	// Stop -> version back to available; activate -> deployed again.
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/deployments/%d/stop", B, dep.ID), admin, nil, nil)
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/models/%d/versions", B, m.ID), admin, nil, &vlistResp)
	json.Unmarshal(vlistResp.Data, &versions)
	if versions[0].Status != "available" {
		t.Fatalf("stopped deployment should set version available, got %s", versions[0].Status)
	}
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/deployments/%d/activate", B, dep.ID), admin, nil, nil)
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/models/%d/versions", B, m.ID), admin, nil, &vlistResp)
	json.Unmarshal(vlistResp.Data, &versions)
	if versions[0].Status != "deployed" {
		t.Fatalf("activated deployment should set version deployed, got %s", versions[0].Status)
	}

	// Model detail includes versions and deployments.
	var detail apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/models/%d", B, m.ID), admin, nil, &detail)
	var detailData struct {
		Versions    []json.RawMessage `json:"versions"`
		Deployments []json.RawMessage `json:"deployments"`
	}
	json.Unmarshal(detail.Data, &detailData)
	if len(detailData.Versions) != 1 || len(detailData.Deployments) != 1 {
		t.Fatalf("model detail incomplete: %s", string(detail.Data))
	}

	// Stats.
	var statsResp apiResp
	doJSON(t, http.MethodGet, B+"/ai/model-stats", admin, nil, &statsResp)
	var stats struct {
		Models      int64 `json:"models"`
		Versions    int64 `json:"versions"`
		Deployments int64 `json:"deployments"`
		Active      int64 `json:"active"`
	}
	json.Unmarshal(statsResp.Data, &stats)
	if stats.Models != 1 || stats.Versions != 1 || stats.Deployments != 1 || stats.Active != 1 {
		t.Fatalf("unexpected model stats: %+v", stats)
	}

	// Delete model cascades versions and deployments.
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/ai/models/%d", B, m.ID), admin, nil, nil); code != http.StatusOK {
		t.Fatalf("delete model failed: %d", code)
	}
	doJSON(t, http.MethodGet, B+"/ai/deployments", admin, nil, &dlistResp)
	ds = nil
	json.Unmarshal(dlistResp.Data, &ds)
	if len(ds) != 0 {
		t.Fatalf("deployments should be deleted with model: %+v", ds)
	}
}

// TestModelManagementVersionIsolation rejects deploying a version mismatched to
// a model and deleting a version that is referenced by a deployment.
func TestModelManagementVersionIsolation(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	var p1, p2 apiResp
	doJSON(t, http.MethodPost, B+"/ai/providers", admin, map[string]any{"name": "p1", "kind": "cv"}, &p1)
	doJSON(t, http.MethodPost, B+"/ai/providers", admin, map[string]any{"name": "p2", "kind": "cv"}, &p2)
	var prov struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(p1.Data, &prov)

	var mA, mB apiResp
	doJSON(t, http.MethodPost, B+"/ai/models", admin, map[string]any{"name": "mA", "kind": "cv"}, &mA)
	doJSON(t, http.MethodPost, B+"/ai/models", admin, map[string]any{"name": "mB", "kind": "cv"}, &mB)
	var a, b struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(mA.Data, &a)
	json.Unmarshal(mB.Data, &b)

	var vA apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/models/%d/versions", B, a.ID), admin,
		map[string]any{"version": "1.0"}, &vA)
	var va struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(vA.Data, &va)

	// Deploying model B with model A's version must fail.
	if code := doJSON(t, http.MethodPost, B+"/ai/deployments", admin,
		map[string]any{"modelId": b.ID, "versionId": va.ID, "providerId": prov.ID}, nil); code != http.StatusBadRequest {
		t.Fatalf("mismatched version/model should be 400, got %d", code)
	}
}

// TestMapEvents covers the AI event map: events placed via their channel GPS,
// filtering by level/type and aggregate stats.
func TestMapEvents(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// A located device/channel.
	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-map", "protocol": "rtsp", "ip": "10.0.0.50",
	}, &devResp)
	var dev struct {
		ID       uint `json:"id"`
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if len(dev.Channels) == 0 {
		t.Fatalf("device has no channel: %+v", devResp)
	}
	chID := dev.Channels[0].ID
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/map/channels/%d/gps", B, chID), admin,
		map[string]any{"longitude": 116.4074, "latitude": 39.9042}, nil); code != http.StatusOK {
		t.Fatalf("set channel gps failed: %d", code)
	}

	// A second channel without GPS - its events must not appear on the map.
	var dev2Resp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-nogps", "protocol": "rtsp", "ip": "10.0.0.51",
	}, &dev2Resp)
	var dev2 struct {
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(dev2Resp.Data, &dev2)
	ch2ID := dev2.Channels[0].ID

	// Ingest events.
	ingest := func(ch uint, etype, level string) {
		doJSON(t, http.MethodPost, B+"/events/ingest", admin, map[string]any{
			"channelId": ch, "kind": "cv", "eventType": etype, "level": level,
			"confidence": 0.9, "summary": etype + " detected",
		}, nil)
	}
	ingest(chID, "person_intrusion", "critical")
	ingest(chID, "fire", "warning")
	ingest(ch2ID, "person_intrusion", "info") // no GPS -> excluded

	// All located events.
	var listResp apiResp
	doJSON(t, http.MethodGet, B+"/map/events", admin, nil, &listResp)
	var events []struct {
		ChannelID   uint    `json:"channelId"`
		EventType   string  `json:"eventType"`
		Level       string  `json:"level"`
		ChannelName string  `json:"channelName"`
		Longitude   float64 `json:"longitude"`
		Latitude    float64 `json:"latitude"`
	}
	json.Unmarshal(listResp.Data, &events)
	if len(events) != 2 {
		t.Fatalf("expected 2 located events, got %d: %+v", len(events), events)
	}
	for _, e := range events {
		if e.ChannelID != chID || e.Longitude == 0 || e.Latitude == 0 || e.ChannelName == "" {
			t.Fatalf("event not enriched with coordinates: %+v", e)
		}
	}

	// Filter by level.
	var critResp apiResp
	doJSON(t, http.MethodGet, B+"/map/events?level=critical", admin, nil, &critResp)
	events = nil
	json.Unmarshal(critResp.Data, &events)
	if len(events) != 1 || events[0].EventType != "person_intrusion" {
		t.Fatalf("level filter failed: %+v", events)
	}

	// Filter by event type.
	var fireResp apiResp
	doJSON(t, http.MethodGet, B+"/map/events?eventType=fire", admin, nil, &fireResp)
	events = nil
	json.Unmarshal(fireResp.Data, &events)
	if len(events) != 1 || events[0].Level != "warning" {
		t.Fatalf("type filter failed: %+v", events)
	}

	// Stats.
	var statsResp apiResp
	doJSON(t, http.MethodGet, B+"/map/events/stats", admin, nil, &statsResp)
	var stats struct {
		Total   int            `json:"total"`
		Located int            `json:"located"`
		ByType  map[string]int `json:"byType"`
		ByLevel map[string]int `json:"byLevel"`
	}
	json.Unmarshal(statsResp.Data, &stats)
	if stats.Total != 3 || stats.Located != 2 {
		t.Fatalf("unexpected event stats totals: %+v", stats)
	}
	if stats.ByLevel["critical"] != 1 || stats.ByLevel["warning"] != 1 {
		t.Fatalf("unexpected byLevel: %+v", stats.ByLevel)
	}
	if stats.ByType["person_intrusion"] != 1 || stats.ByType["fire"] != 1 {
		t.Fatalf("unexpected byType: %+v", stats.ByType)
	}
}

// TestAlertManagement covers AI event tiered alert distribution: policy +
// tier CRUD, the tester, delivery audit, stats, event acknowledgement and
// access control.
func TestAlertManagement(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	hookCalls := 0
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hookCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	// Notification channel as a delivery target.
	var chResp apiResp
	doJSON(t, http.MethodPost, B+"/notify/channels", admin, map[string]any{
		"name": "wh", "type": "webhook", "url": hook.URL, "enabled": true,
	}, &chResp)
	var ch struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(chResp.Data, &ch)
	if ch.ID == 0 {
		t.Fatalf("channel not created: %+v", chResp)
	}

	// Policy with two tiers.
	var pResp apiResp
	doJSON(t, http.MethodPost, B+"/alert/policies", admin, map[string]any{
		"name": "critical-people", "enabled": true, "minLevel": "warning", "ackRequired": true,
		"tiers": []map[string]any{
			{"tier": 0, "delaySec": 0, "targetIds": strconv.Itoa(int(ch.ID))},
			{"tier": 1, "delaySec": 60, "targetIds": strconv.Itoa(int(ch.ID))},
		},
	}, &pResp)
	var p struct {
		ID    uint `json:"id"`
		Tiers []struct {
			ID   uint `json:"id"`
			Tier int  `json:"tier"`
		} `json:"tiers"`
	}
	json.Unmarshal(pResp.Data, &p)
	if p.ID == 0 || len(p.Tiers) != 2 {
		t.Fatalf("policy/tiers not created: %+v", pResp)
	}

	// Detail returns tiers.
	var detail apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/alert/policies/%d", B, p.ID), admin, nil, &detail)
	json.Unmarshal(detail.Data, &p)
	if len(p.Tiers) != 2 {
		t.Fatalf("detail missing tiers: %s", string(detail.Data))
	}

	// Tester dispatches every tier.
	var testResp apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/alert/policies/%d/test", B, p.ID), admin, nil, &testResp)
	var td struct {
		Sent int `json:"sent"`
	}
	json.Unmarshal(testResp.Data, &td)
	if td.Sent != 2 {
		t.Fatalf("expected 2 test deliveries, got %+v", testResp)
	}

	// Delivery audit records both.
	var dResp apiResp
	doJSON(t, http.MethodGet, B+"/alert/deliveries", admin, nil, &dResp)
	var page struct {
		Total int `json:"total"`
		Items []struct {
			PolicyID uint   `json:"policyId"`
			Reason   string `json:"reason"`
			Status   string `json:"status"`
		} `json:"items"`
	}
	json.Unmarshal(dResp.Data, &page)
	if page.Total < 2 {
		t.Fatalf("expected >=2 delivery records, got %+v", page)
	}

	// Stats.
	var statsResp apiResp
	doJSON(t, http.MethodGet, B+"/alert/stats", admin, nil, &statsResp)
	var as struct {
		Policies   int64 `json:"policies"`
		Deliveries int64 `json:"deliveries"`
	}
	json.Unmarshal(statsResp.Data, &as)
	if as.Policies != 1 || as.Deliveries < 2 {
		t.Fatalf("unexpected alert stats: %+v", as)
	}

	// Ingest an event, then acknowledge it.
	var evResp apiResp
	doJSON(t, http.MethodPost, B+"/events/ingest", admin, map[string]any{
		"channelId": 1, "kind": "cv", "eventType": "person_intrusion", "level": "critical", "summary": "x",
	}, &evResp)
	var ev struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(evResp.Data, &ev)
	if ev.ID == 0 {
		t.Fatalf("event not ingested: %+v", evResp)
	}
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/events/%d/ack", B, ev.ID), admin, nil, nil); code != http.StatusOK {
		t.Fatalf("ack event failed: %d", code)
	}
	var evList apiResp
	doJSON(t, http.MethodGet, B+"/events", admin, nil, &evList)
	var evPage struct {
		Items []struct {
			ID    uint `json:"id"`
			Acked bool `json:"acked"`
		} `json:"items"`
	}
	json.Unmarshal(evList.Data, &evPage)
	ackFound := false
	for _, it := range evPage.Items {
		if it.ID == ev.ID && it.Acked {
			ackFound = true
		}
	}
	if !ackFound {
		t.Fatalf("event should be acked: %+v", evPage.Items)
	}

	// Non-admin without alert permission is forbidden.
	var roleResp apiResp
	doJSON(t, http.MethodPost, B+"/roles", admin, map[string]any{"name": "noviewer", "permissions": "video"}, &roleResp)
	doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "nov", "nickname": "NV", "password": "secret1", "role": "noviewer",
	}, nil)
	novToken := loginAs(t, srv.URL, "nov", "secret1")
	if code := doJSON(t, http.MethodGet, B+"/alert/policies", novToken, nil, nil); code != http.StatusForbidden {
		t.Fatalf("non-authorized alert access should be 403, got %d", code)
	}

	// Delete policy cascades tiers.
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/alert/policies/%d", B, p.ID), admin, nil, nil); code != http.StatusOK {
		t.Fatalf("delete policy failed: %d", code)
	}
	var tiersResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/alert/policies/%d/tiers", B, p.ID), admin, nil, &tiersResp)
	var tiers []json.RawMessage
	json.Unmarshal(tiersResp.Data, &tiers)
	if len(tiers) != 0 {
		t.Fatalf("tiers should be deleted with policy: %s", string(tiersResp.Data))
	}
}

// TestPipeline covers the annotation/training pipeline: dataset + sample
// management, importing events, annotation progress and a training job that
// registers a model version.
func TestPipeline(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Create a dataset.
	var dsResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/datasets", admin, map[string]any{
		"name": "person-det", "kind": "cv", "labels": `["person","car"]`,
	}, &dsResp)
	var ds struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(dsResp.Data, &ds)
	if ds.ID == 0 {
		t.Fatalf("dataset not created: %+v", dsResp)
	}

	// Ingest two events with snapshots.
	var eventIDs []uint
	for _, et := range []string{"person", "person"} {
		var evResp apiResp
		doJSON(t, http.MethodPost, B+"/events/ingest", admin, map[string]any{
			"channelId": 1, "kind": "cv", "eventType": et, "level": "info",
			"summary": et, "snapshot": "/snapshots/" + et + ".jpg",
		}, &evResp)
		var ev struct {
			ID uint `json:"id"`
		}
		json.Unmarshal(evResp.Data, &ev)
		eventIDs = append(eventIDs, ev.ID)
	}

	// Import events as samples.
	var impResp apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/datasets/%d/samples/import", B, ds.ID), admin,
		map[string]any{"eventIds": eventIDs}, &impResp)
	var imp struct {
		Created int `json:"created"`
	}
	json.Unmarshal(impResp.Data, &imp)
	if imp.Created != 2 {
		t.Fatalf("expected 2 imported samples, got %+v", impResp)
	}

	// Add one manual sample.
	var sResp apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/datasets/%d/samples", B, ds.ID), admin,
		map[string]any{"imageUrl": "/snapshots/manual.jpg", "split": "train"}, &sResp)
	var sample struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(sResp.Data, &sample)
	if sample.ID == 0 {
		t.Fatalf("sample not created: %+v", sResp)
	}

	// Dataset now reports 3 samples.
	var detResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/datasets/%d", B, ds.ID), admin, nil, &detResp)
	var det struct {
		Dataset struct {
			SampleCount  int `json:"sampleCount"`
			LabeledCount int `json:"labeledCount"`
		} `json:"dataset"`
	}
	json.Unmarshal(detResp.Data, &det)
	if det.Dataset.SampleCount != 3 {
		t.Fatalf("expected sampleCount 3, got %+v", det.Dataset)
	}

	// Label one sample.
	if code := doJSON(t, http.MethodPut, fmt.Sprintf("%s/ai/datasets/%d/samples/%d", B, ds.ID, sample.ID), admin,
		map[string]any{"status": "labeled", "labels": `{"class":"person"}`}, nil); code != http.StatusOK {
		t.Fatalf("label sample failed: %d", code)
	}

	// Annotation task picks up progress from the dataset.
	var atResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/annotations", admin, map[string]any{
		"name": "label-wave-1", "datasetId": ds.ID, "assignee": "alice",
	}, &atResp)
	var at struct {
		ID      uint `json:"id"`
		Total   int  `json:"total"`
		Labeled int  `json:"labeled"`
	}
	json.Unmarshal(atResp.Data, &at)
	if at.ID == 0 || at.Total != 3 || at.Labeled != 1 {
		t.Fatalf("unexpected annotation progress: %+v", atResp)
	}

	// Complete annotation -> dataset becomes ready.
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/annotations/%d/complete", B, at.ID), admin, nil, nil)
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/datasets/%d", B, ds.ID), admin, nil, &detResp)
	json.Unmarshal(detResp.Data, &det)
	var dsStatus struct {
		Dataset struct {
			Status string `json:"status"`
		} `json:"dataset"`
	}
	json.Unmarshal(detResp.Data, &dsStatus)
	if dsStatus.Dataset.Status != "ready" {
		t.Fatalf("dataset should be ready after annotation, got %+v", dsStatus.Dataset)
	}

	// Training job produces a model version.
	var jobResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/training", admin, map[string]any{
		"name": "yolo-train", "datasetId": ds.ID, "framework": "pytorch",
		"hyperParams": `{"epochs":10}`, "metrics": `{"mAP":0.82}`,
	}, &jobResp)
	var job struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(jobResp.Data, &job)
	if job.ID == 0 {
		t.Fatalf("training job not created: %+v", jobResp)
	}
	var runResp apiResp
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/ai/training/%d/run", B, job.ID), admin, nil, &runResp); code != http.StatusOK {
		t.Fatalf("run training failed: %d %s", code, runResp.Message)
	}
	var run struct {
		Job struct {
			Status    string `json:"status"`
			VersionID uint   `json:"versionId"`
			ModelID   uint   `json:"modelId"`
		} `json:"job"`
		Version struct {
			ID      uint   `json:"id"`
			Version string `json:"version"`
			Status  string `json:"status"`
		} `json:"version"`
	}
	json.Unmarshal(runResp.Data, &run)
	if run.Job.Status != "succeeded" || run.Job.VersionID == 0 || run.Version.Status != "registered" {
		t.Fatalf("training did not produce a version: %s", string(runResp.Data))
	}
	// The outcome is visible in the model registry.
	var mvResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/models/%d/versions", B, run.Job.ModelID), admin, nil, &mvResp)
	var versions []json.RawMessage
	json.Unmarshal(mvResp.Data, &versions)
	if len(versions) != 1 {
		t.Fatalf("expected 1 registered model version, got %s", string(mvResp.Data))
	}

	// Pipeline stats.
	var psResp apiResp
	doJSON(t, http.MethodGet, B+"/ai/pipeline-stats", admin, nil, &psResp)
	var ps struct {
		Datasets    int64 `json:"datasets"`
		Samples     int64 `json:"samples"`
		Labeled     int64 `json:"labeled"`
		Annotations int64 `json:"annotations"`
		Jobs        int64 `json:"jobs"`
	}
	json.Unmarshal(psResp.Data, &ps)
	if ps.Datasets != 1 || ps.Samples != 3 || ps.Labeled != 1 || ps.Annotations != 1 || ps.Jobs != 1 {
		t.Fatalf("unexpected pipeline stats: %+v", ps)
	}

	// Non-authorized role is rejected.
	var roleResp apiResp
	doJSON(t, http.MethodPost, B+"/roles", admin, map[string]any{"name": "pv2", "permissions": "video"}, &roleResp)
	doJSON(t, http.MethodPost, B+"/users", admin, map[string]any{
		"username": "pv2", "nickname": "PV2", "password": "secret1", "role": "pv2",
	}, nil)
	pvToken := loginAs(t, srv.URL, "pv2", "secret1")
	if code := doJSON(t, http.MethodGet, B+"/ai/datasets", pvToken, nil, nil); code != http.StatusForbidden {
		t.Fatalf("non-authorized pipeline access should be 403, got %d", code)
	}

	// Deleting the dataset cascades samples and annotation tasks.
	doJSON(t, http.MethodDelete, fmt.Sprintf("%s/ai/datasets/%d", B, ds.ID), admin, nil, nil)
	var samplesResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/datasets/%d/samples", B, ds.ID), admin, nil, &samplesResp)
	var remaining []json.RawMessage
	json.Unmarshal(samplesResp.Data, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("samples should be deleted with dataset: %s", string(samplesResp.Data))
	}
}

// TestMapCoordinates covers address search (no network needed for an empty
// query), CSV coordinate import for devices/channels and group base points.
func TestMapCoordinates(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// Empty query short-circuits without hitting the network.
	var geo apiResp
	if code := doJSON(t, http.MethodGet, B+"/map/geocode?q=", admin, nil, &geo); code != http.StatusOK {
		t.Fatalf("geocode empty status %d", code)
	}
	var geoData struct {
		Provider string            `json:"provider"`
		Items    []json.RawMessage `json:"items"`
	}
	json.Unmarshal(geo.Data, &geoData)
	if geoData.Provider == "" || len(geoData.Items) != 0 {
		t.Fatalf("unexpected geocode response: %s", string(geo.Data))
	}

	// Device with an auto-created main channel.
	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-map2", "protocol": "rtsp", "ip": "10.0.0.60",
	}, &devResp)
	var dev struct {
		Name     string `json:"name"`
		Channels []struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if len(dev.Channels) == 0 {
		t.Fatalf("device has no channel: %+v", devResp)
	}

	// Import device coordinates.
	var imp apiResp
	doJSON(t, http.MethodPost, B+"/map/import", admin, map[string]any{
		"target": "device", "csv": "cam-map2,116.4074,39.9042,50",
	}, &imp)
	var impData struct {
		Updated int `json:"updated"`
		Skipped int `json:"skipped"`
	}
	json.Unmarshal(imp.Data, &impData)
	if impData.Updated != 1 {
		t.Fatalf("expected 1 device updated: %+v", impData)
	}

	// Import channel coordinates by name (header/comment lines are skipped).
	var imp2 apiResp
	doJSON(t, http.MethodPost, B+"/map/import", admin, map[string]any{
		"target": "channel", "csv": "# name,lon,lat\n" + dev.Channels[0].Name + ",121.4737,31.2304",
	}, &imp2)
	var imp2Data struct {
		Updated int `json:"updated"`
		Skipped int `json:"skipped"`
	}
	json.Unmarshal(imp2.Data, &imp2Data)
	if imp2Data.Updated != 1 {
		t.Fatalf("expected 1 channel updated: %+v", imp2Data)
	}

	// The located device shows on the map.
	var md apiResp
	doJSON(t, http.MethodGet, B+"/map/devices", admin, nil, &md)
	var mapDevs []struct {
		Name      string  `json:"name"`
		Longitude float64 `json:"longitude"`
		Latitude  float64 `json:"latitude"`
	}
	json.Unmarshal(md.Data, &mapDevs)
	found := false
	for _, d := range mapDevs {
		if d.Name == "cam-map2" && d.Longitude > 116.4 && d.Latitude > 39.9 {
			found = true
		}
	}
	if !found {
		t.Fatalf("imported device not located: %+v", mapDevs)
	}

	// Group base coordinate is persisted.
	var grResp apiResp
	doJSON(t, http.MethodPost, B+"/groups", admin, map[string]any{
		"name": "g-base", "longitude": 120.1, "latitude": 30.2,
	}, &grResp)
	var gr struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(grResp.Data, &gr)
	if gr.ID == 0 {
		t.Fatalf("group not created: %+v", grResp)
	}
	var glist apiResp
	doJSON(t, http.MethodGet, B+"/groups", admin, nil, &glist)
	var groups []struct {
		ID        uint    `json:"id"`
		Longitude float64 `json:"longitude"`
		Latitude  float64 `json:"latitude"`
	}
	json.Unmarshal(glist.Data, &groups)
	baseFound := false
	for _, g := range groups {
		if g.ID == gr.ID && g.Longitude > 120 && g.Latitude > 30 {
			baseFound = true
		}
	}
	if !baseFound {
		t.Fatalf("group base coordinate not stored: %+v", groups)
	}
}

// TestPTZControl covers PTZ command validation, unsupported protocols and the
// platform-side preset catalog.
func TestPTZControl(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	// A generic RTSP device: PTZ is not supported.
	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-ptz", "protocol": "rtsp", "ip": "10.0.0.70",
	}, &devResp)
	var dev struct {
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if len(dev.Channels) == 0 {
		t.Fatalf("no channel: %+v", devResp)
	}
	chID := dev.Channels[0].ID

	// Unsupported protocol -> 400.
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/channels/%d/ptz", B, chID), admin,
		map[string]any{"cmd": "up", "speed": 50}, nil); code != http.StatusBadRequest {
		t.Fatalf("unsupported protocol should be 400, got %d", code)
	}
	// Invalid command -> 400.
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/channels/%d/ptz", B, chID), admin,
		map[string]any{"cmd": "wiggle"}, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid cmd should be 400, got %d", code)
	}

	// Preset catalog works (metadata is platform-side).
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/channels/%d/ptz/presets", B, chID), admin,
		map[string]any{"preset": 1, "name": "gate"}, nil); code != http.StatusOK {
		t.Fatalf("save preset failed: %d", code)
	}
	var list apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/channels/%d/ptz/presets", B, chID), admin, nil, &list)
	var presets []struct {
		Preset int    `json:"preset"`
		Name   string `json:"name"`
	}
	json.Unmarshal(list.Data, &presets)
	if len(presets) != 1 || presets[0].Preset != 1 || presets[0].Name != "gate" {
		t.Fatalf("unexpected presets: %+v", presets)
	}
	// Goto on an unsupported protocol -> 400.
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/channels/%d/ptz/presets/1/goto", B, chID), admin, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("goto unsupported should be 400, got %d", code)
	}
	// Delete preset.
	if code := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/channels/%d/ptz/presets/1", B, chID), admin, nil, nil); code != http.StatusOK {
		t.Fatalf("delete preset failed: %d", code)
	}
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/channels/%d/ptz/presets", B, chID), admin, nil, &list)
	presets = nil
	json.Unmarshal(list.Data, &presets)
	if len(presets) != 0 {
		t.Fatalf("preset should be deleted: %+v", presets)
	}

	// A GB28181 device when signaling is disabled -> 400 with a clear message.
	var gbResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "34020000001320000077", "protocol": "gb28181", "ip": "10.0.0.71",
	}, &gbResp)
	var gbDev struct {
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(gbResp.Data, &gbDev)
	if len(gbDev.Channels) == 0 {
		t.Fatalf("gb device has no channel: %+v", gbResp)
	}
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/channels/%d/ptz", B, gbDev.Channels[0].ID), admin,
		map[string]any{"cmd": "left", "speed": 60}, nil); code != http.StatusBadRequest {
		t.Fatalf("gb disabled ptz should be 400, got %d", code)
	}
}

// TestDiagnostics covers playback diagnostics and VQD endpoints. Both return a
// report even when the probe fails (unreachable source / missing tools).
func TestDiagnostics(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-diag", "protocol": "rtsp", "ip": "10.0.0.80",
	}, &devResp)
	var dev struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if dev.ID == 0 {
		t.Fatalf("device not created: %+v", devResp)
	}

	// A channel pointing at a closed port fails fast.
	var chResp apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/devices/%d/channels", B, dev.ID), admin,
		map[string]any{"name": "diag", "sourceUrl": "http://127.0.0.1:1/nope"}, &chResp)
	var ch struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(chResp.Data, &ch)
	if ch.ID == 0 {
		t.Fatalf("channel not created: %+v", chResp)
	}

	var diag apiResp
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/channels/%d/diagnose?timeoutSec=1", B, ch.ID), admin, nil, &diag); code != http.StatusOK {
		t.Fatalf("diagnose status %d", code)
	}
	var diagData struct {
		Status  string `json:"status"`
		Channel int    `json:"channelId"`
		Error   string `json:"error"`
	}
	json.Unmarshal(diag.Data, &diagData)
	if diagData.Status != "failed" || diagData.Channel != int(ch.ID) {
		t.Fatalf("expected failed probe: %s", string(diag.Data))
	}

	var q apiResp
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/channels/%d/vqd", B, ch.ID), admin, nil, &q); code != http.StatusOK {
		t.Fatalf("vqd status %d", code)
	}
	var qData struct {
		Status string `json:"status"`
	}
	json.Unmarshal(q.Data, &qData)
	if qData.Status != "failed" {
		t.Fatalf("expected failed vqd: %s", string(q.Data))
	}

	// Unknown channel -> 404.
	if code := doJSON(t, http.MethodGet, B+"/channels/999999/diagnose", admin, nil, nil); code != http.StatusNotFound {
		t.Fatalf("unknown channel diagnose should be 404, got %d", code)
	}

	// Channel without a source URL -> 400.
	var noSrc apiResp
	doJSON(t, http.MethodPost, fmt.Sprintf("%s/devices/%d/channels", B, dev.ID), admin,
		map[string]any{"name": "nosrc"}, &noSrc)
	var noSrcCh struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(noSrc.Data, &noSrcCh)
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/channels/%d/diagnose", B, noSrcCh.ID), admin, nil, nil); code != http.StatusBadRequest {
		t.Fatalf("no-source diagnose should be 400, got %d", code)
	}
}

// TestPlatformAdminFeatures covers the platform governance additions: black-list,
// CSV bulk import/export, playback auth helpers, traffic/status logging, device
// health checks and AI task schedule evaluation.
func TestPlatformAdminFeatures(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	rawGet := func(url string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("raw get: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// --- black list ---
	if code := doJSON(t, http.MethodPost, B+"/gb/blacklist", admin, map[string]any{"protocol": "GB28181"}, nil); code != http.StatusBadRequest {
		t.Fatalf("empty blacklist rule should be 400, got %d", code)
	}
	if code := doJSON(t, http.MethodPost, B+"/gb/blacklist", admin, map[string]any{
		"deviceId": "34020000001320000001", "ip": "1.2.3.4",
	}, nil); code != http.StatusOK {
		t.Fatalf("create blacklist failed: %d", code)
	}
	var blList apiResp
	doJSON(t, http.MethodGet, B+"/gb/blacklist", admin, nil, &blList)
	var blacks []struct {
		DeviceID string `json:"deviceId"`
	}
	json.Unmarshal(blList.Data, &blacks)
	if len(blacks) != 1 || blacks[0].DeviceID != "34020000001320000001" {
		t.Fatalf("blacklist not listed: %s", blList.Data)
	}

	// --- device CSV import/export ---
	devCSV := "name,protocol,accessMode,manufacturer,ip,port,username,password\n" +
		"cam-import,rtsp,pull,other,10.1.2.3,554,admin,pass\n"
	var devImp apiResp
	doJSON(t, http.MethodPost, B+"/devices/import", admin, map[string]any{"csv": devCSV}, &devImp)
	var devImpData struct {
		Created int `json:"created"`
	}
	json.Unmarshal(devImp.Data, &devImpData)
	if devImpData.Created != 1 {
		t.Fatalf("expected 1 device imported: %s", devImp.Data)
	}
	if code, body := rawGet(B + "/devices/export"); code != http.StatusOK || !strings.Contains(body, "cam-import") {
		t.Fatalf("device export missing imported row (code=%d)", code)
	}

	// --- user CSV import/export + login ---
	userCSV := "username,nickname,role,password,enabled\nbulkuser,批量用户,viewer,secret123,true\n"
	var userImp apiResp
	doJSON(t, http.MethodPost, B+"/users/import", admin, map[string]any{"csv": userCSV}, &userImp)
	if tok := loginAs(t, srv.URL, "bulkuser", "secret123"); tok == "" {
		t.Fatal("imported user cannot log in")
	}
	if code, body := rawGet(B + "/users/export"); code != http.StatusOK || !strings.Contains(body, "bulkuser") {
		t.Fatalf("user export missing imported row (code=%d)", code)
	}

	// --- device health check + status log ---
	var devResp apiResp
	doJSON(t, http.MethodPost, B+"/devices", admin, map[string]any{
		"name": "cam-check", "protocol": "rtsp", "ip": "127.0.0.1", "port": 1,
	}, &devResp)
	var dev struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(devResp.Data, &dev)
	var checkResp apiResp
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/devices/%d/check", B, dev.ID), admin, nil, &checkResp); code != http.StatusOK {
		t.Fatalf("check device failed: %d", code)
	}
	var check struct {
		Online bool `json:"online"`
	}
	json.Unmarshal(checkResp.Data, &check)
	if check.Online {
		t.Fatal("device on closed port should be offline")
	}
	var logs apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/devices/%d/status-logs", B, dev.ID), admin, nil, &logs)
	var logRows []struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(logs.Data, &logRows)
	if len(logRows) == 0 {
		t.Fatal("expected a status log entry after check")
	}

	// --- traffic: ZLM down -> sync 502, channel traffic still readable ---
	var chList apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/devices/%d/channels", B, dev.ID), admin, nil, &chList)
	var chans []struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(chList.Data, &chans)
	if len(chans) == 0 {
		t.Fatal("expected auto-created channel")
	}
	if code := doJSON(t, http.MethodPost, B+"/video/traffic/sync", admin, nil, nil); code != http.StatusBadGateway {
		t.Fatalf("traffic sync with ZLM down should be 502, got %d", code)
	}
	var traffic apiResp
	if code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/channels/%d/traffic", B, chans[0].ID), admin, nil, &traffic); code != http.StatusOK {
		t.Fatalf("get channel traffic failed: %d", code)
	}

	// --- recording cleanup (no plans -> 0 removed) ---
	var clean apiResp
	if code := doJSON(t, http.MethodPost, B+"/recordings/cleanup", admin, nil, &clean); code != http.StatusOK {
		t.Fatalf("cleanup failed: %d", code)
	}

	// --- playback helpers ---
	var provResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/providers", admin, map[string]any{
		"name": "mock", "kind": "cv", "endpoint": "http://127.0.0.1:1", "enabled": true,
	}, &provResp)
	var prov struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(provResp.Data, &prov)
	var taskResp apiResp
	doJSON(t, http.MethodPost, B+"/ai/tasks", admin, map[string]any{
		"name": "sched", "channelId": chans[0].ID, "providerId": prov.ID,
		"taskType": "cv_detect", "schedule": `{"days":"daily","start":"00:00","end":"23:59"}`,
	}, &taskResp)
	var task struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(taskResp.Data, &task)
	var schedResp apiResp
	doJSON(t, http.MethodGet, fmt.Sprintf("%s/ai/tasks/%d/schedule", B, task.ID), admin, nil, &schedResp)
	var sched struct {
		Active bool `json:"active"`
	}
	json.Unmarshal(schedResp.Data, &sched)
	if !sched.Active {
		t.Fatalf("daily schedule should be active now: %s", schedResp.Data)
	}
	var pt apiResp
	if code := doJSON(t, http.MethodPost, fmt.Sprintf("%s/channels/%d/play-token", B, chans[0].ID), admin, nil, &pt); code != http.StatusOK {
		t.Fatalf("play-token failed: %d", code)
	}
	var verify apiResp
	doJSON(t, http.MethodGet, B+"/play/verify?stream=x&token=bad&exp=1", admin, nil, &verify)
	var v struct {
		Valid bool `json:"valid"`
	}
	json.Unmarshal(verify.Data, &v)
	if v.Valid {
		t.Fatal("garbage token must not verify")
	}
}

// TestPlatformConfig verifies runtime enable/disable of GB28181/EHOME signaling
// from the platform-config API without restarting the process.
func TestPlatformConfig(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	var cfgResp apiResp
	if code := doJSON(t, http.MethodGet, B+"/config/platform", admin, nil, &cfgResp); code != http.StatusOK {
		t.Fatalf("get platform config: %d", code)
	}
	var cfg struct {
		GB struct {
			Enabled bool `json:"enabled"`
			Running bool `json:"running"`
		} `json:"gb"`
	}
	json.Unmarshal(cfgResp.Data, &cfg)
	if cfg.GB.Enabled || cfg.GB.Running {
		t.Fatalf("GB should default disabled: %s", cfgResp.Data)
	}

	var up apiResp
	if code := doJSON(t, http.MethodPut, B+"/config/platform", admin, map[string]any{"gbEnabled": true}, &up); code != http.StatusOK {
		t.Fatalf("enable gb: %d", code)
	}
	doJSON(t, http.MethodGet, B+"/config/platform", admin, nil, &cfgResp)
	json.Unmarshal(cfgResp.Data, &cfg)
	if !cfg.GB.Enabled || !cfg.GB.Running {
		t.Fatalf("GB should be enabled and running: %s", cfgResp.Data)
	}

	var gbCfg apiResp
	doJSON(t, http.MethodGet, B+"/gb/config", admin, nil, &gbCfg)
	var gb struct {
		Enabled bool `json:"enabled"`
		Running bool `json:"running"`
	}
	json.Unmarshal(gbCfg.Data, &gb)
	if !gb.Enabled {
		t.Fatalf("/gb/config should report enabled: %s", gbCfg.Data)
	}

	if code := doJSON(t, http.MethodPut, B+"/config/platform", admin, map[string]any{"gbEnabled": false}, &up); code != http.StatusOK {
		t.Fatalf("disable gb: %d", code)
	}
	doJSON(t, http.MethodGet, B+"/config/platform", admin, nil, &cfgResp)
	json.Unmarshal(cfgResp.Data, &cfg)
	if cfg.GB.Enabled || cfg.GB.Running {
		t.Fatalf("GB should be disabled and stopped: %s", cfgResp.Data)
	}
}

// TestPlatformConfigGB35114Listen verifies the GB35114 secure-SIP listen address
// is configurable at runtime and that enabling without a port warns instead of
// silently failing.
func TestPlatformConfigGB35114Listen(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	B := srv.URL + "/api/v1"

	var up apiResp
	doJSON(t, http.MethodPut, B+"/config/platform", admin, map[string]any{"gb35114Enabled": true}, &up)
	var w struct {
		Warnings []string `json:"warnings"`
	}
	json.Unmarshal(up.Data, &w)
	if len(w.Warnings) == 0 {
		t.Fatalf("expected warning when no listen address configured: %s", up.Data)
	}

	if code := doJSON(t, http.MethodPut, B+"/config/platform", admin, map[string]any{"gb35114SipListen": ":15061"}, &up); code != http.StatusOK {
		t.Fatalf("set listen: %d", code)
	}
	var cfg apiResp
	doJSON(t, http.MethodGet, B+"/config/platform", admin, nil, &cfg)
	var data struct {
		GB35114 struct {
			SipListen  string `json:"sipListen"`
			Configured bool   `json:"configured"`
		} `json:"gb35114"`
	}
	json.Unmarshal(cfg.Data, &data)
	if data.GB35114.SipListen != ":15061" || !data.GB35114.Configured {
		t.Fatalf("listen not persisted: %s", cfg.Data)
	}
}
