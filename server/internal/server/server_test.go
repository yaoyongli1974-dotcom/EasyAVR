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
		ID       uint `json:"id"`
		Name     string `json:"name"`
		ParentID uint `json:"parentId"`
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
		ID       uint `json:"id"`
		Name     string `json:"name"`
		ParentID uint `json:"parentId"`
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
