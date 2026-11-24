package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/snapshot"
	"github.com/easyavr/easyavr/internal/store"
)

func newSnapshotTestServer(t *testing.T) (*httptest.Server, *config.Config, *App) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		Listen:      ":0",
		DBPath:      filepath.Join(dir, "test.db"),
		JWTSecret:   "snapshot-test-secret",
		JWTExpireH:  1,
		AdminUser:   "easyavr",
		AdminPass:   "easyavr",
		SnapshotDir: filepath.Join(dir, "snaps"),
	}
	cfg.ZLM.APIBase = "http://127.0.0.1:1"
	cfg.ZLM.Secret = "test"
	cfg.GB35114.CertDir = filepath.Join(dir, "certs")
	if err := os.MkdirAll(cfg.SnapshotDir, 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.Seed(db, cfg.AdminUser, cfg.AdminPass); err != nil {
		t.Fatalf("seed: %v", err)
	}
	app := New(cfg, db)
	srv := httptest.NewServer(app.Engine())
	t.Cleanup(srv.Close)
	return srv, cfg, app
}

func newSnapshotChannel(t *testing.T, app *App, name string) (deviceID, channelID uint) {
	t.Helper()
	dev := model.Device{Name: name + "-dev", Protocol: "rtsp"}
	if err := app.db.Create(&dev).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	ch := model.Channel{DeviceID: dev.ID, Name: name, StreamKey: name + "-key"}
	if err := app.db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return dev.ID, ch.ID
}

func addSnapshotFile(t *testing.T, app *App, channelID uint, rel string) uint {
	t.Helper()
	full := filepath.Join(app.snaps.Dir(), rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "JPEG:" + rel
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	snap := model.Snapshot{
		ChannelID: channelID,
		Path:      rel,
		URL:       "/snapshots/" + rel,
		TakenAt:   time.Now(),
	}
	if err := app.db.Create(&snap).Error; err != nil {
		t.Fatalf("create snapshot row: %v", err)
	}
	return snap.ID
}

func getRaw(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func snapshotUser(t *testing.T, srv *httptest.Server, admin, name, role string) (uint, string) {
	t.Helper()
	var r apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/users", admin, map[string]any{
		"username": name, "nickname": name, "password": "secret1", "role": role,
	}, &r)
	if r.Code != 0 {
		t.Fatalf("create user %s: %+v", name, r)
	}
	var u struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(r.Data, &u); err != nil || u.ID == 0 {
		t.Fatalf("decode user: %v / %+v", err, r)
	}
	return u.ID, loginAs(t, srv.URL, name, "secret1")
}

func snapshotGroup(t *testing.T, srv *httptest.Server, admin, name string) uint {
	t.Helper()
	var r apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups", admin, map[string]any{"name": name}, &r)
	if r.Code != 0 {
		t.Fatalf("create group %s: %+v", name, r)
	}
	var g struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(r.Data, &g)
	return g.ID
}

func TestSnapshotAccessURLAndSignedAccess(t *testing.T) {
	srv, cfg, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)
	_, chID := newSnapshotChannel(t, app, "access")
	snapID := addSnapshotFile(t, app, chID, "12/1000.jpg")

	var r apiResp
	code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapID), admin, nil, &r)
	if code != http.StatusOK || r.Code != 0 {
		t.Fatalf("access-url: %d %+v", code, r)
	}
	var data struct {
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatalf("decode access-url: %v", err)
	}
	if !strings.HasPrefix(data.URL, "/api/v1/snapshot-access/s") {
		t.Fatalf("unexpected url: %q", data.URL)
	}
	if d := time.Until(data.ExpiresAt); d < 4*time.Minute || d > 5*time.Minute {
		t.Fatalf("unexpected expiry: %v", d)
	}

	// The signed URL needs no JWT and serves the catalog file.
	if code, body := getRaw(t, srv.URL+data.URL); code != http.StatusOK || body != "JPEG:12/1000.jpg" {
		t.Fatalf("signed access: %d %q", code, body)
	}

	// Expired, tampered and wrongly signed tokens are rejected.
	expired, _, err := snapshot.SnapshotAccessToken(snapID, 1, -time.Minute, cfg.JWTSecret)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	if code, _ := getRaw(t, srv.URL+"/api/v1/snapshot-access/"+expired); code != http.StatusUnauthorized {
		t.Fatalf("expired token: got %d, want 401", code)
	}
	// Flip a byte in the base64url payload (changing the last signature
	// character can be a no-op: trailing bits are not decoded).
	parts := strings.Split(strings.TrimPrefix(data.URL, "/api/v1/snapshot-access/"), ".")
	payload := []byte(parts[0])
	if payload[3] == 'A' {
		payload[3] = 'B'
	} else {
		payload[3] = 'A'
	}
	tampered := "/api/v1/snapshot-access/" + string(payload) + "." + parts[1]
	if code, _ := getRaw(t, srv.URL+tampered); code != http.StatusUnauthorized {
		t.Fatalf("tampered token: got %d, want 401", code)
	}
	wrong, _, err := snapshot.SnapshotAccessToken(snapID, 1, time.Minute, "not-the-secret")
	if err != nil {
		t.Fatalf("issue wrong secret: %v", err)
	}
	if code, _ := getRaw(t, srv.URL+"/api/v1/snapshot-access/"+wrong); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret token: got %d, want 401", code)
	}

	// A catalog path escaping the snapshot directory is refused even for a
	// legitimately issued token.
	badID := addSnapshotFile(t, app, chID, "../outside.jpg")
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, badID), admin, nil, &r)
	if code != http.StatusOK {
		t.Fatalf("access-url for traversal row: %d %+v", code, r)
	}
	json.Unmarshal(r.Data, &data)
	if code, _ := getRaw(t, srv.URL+data.URL); code != http.StatusForbidden {
		t.Fatalf("path traversal: got %d, want 403", code)
	}

	code = doJSON(t, http.MethodGet, srv.URL+"/api/v1/snapshots/999999/access-url", admin, nil, &r)
	if code != http.StatusNotFound {
		t.Fatalf("unknown snapshot: got %d, want 404", code)
	}

	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapID), "", nil, &r)
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated access-url: got %d, want 401", code)
	}
}

func TestSnapshotListDeletePermissions(t *testing.T) {
	srv, _, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)

	// Restricted role has no "video" RBAC permission and no policy by default.
	var roleResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/roles", admin, map[string]any{
		"name": "snapshot-restricted", "permissions": "event",
	}, &roleResp)
	if roleResp.Code != 0 {
		t.Fatalf("create role: %+v", roleResp)
	}
	userID, restricted := snapshotUser(t, srv, admin, "snapshot-viewer", "snapshot-restricted")

	devA, chA := newSnapshotChannel(t, app, "grouped-a")
	devB, chB := newSnapshotChannel(t, app, "grouped-b")
	gA := snapshotGroup(t, srv, admin, "snap-group-a")
	gB := snapshotGroup(t, srv, admin, "snap-group-b")
	var r apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin,
		map[string]any{"groupId": gA, "channelIds": []uint{chA}}, &r)
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin,
		map[string]any{"groupId": gB, "channelIds": []uint{chB}}, &r)
	// Bind devices as well: requireGroupPerm also probes device-group bindings,
	// and an empty result there would discard the channel-group match.
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/devices/bind", admin,
		map[string]any{"groupId": gA, "deviceIds": []uint{devA}}, &r)
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/devices/bind", admin,
		map[string]any{"groupId": gB, "deviceIds": []uint{devB}}, &r)
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/user-groups", admin,
		map[string]any{"userId": userID, "groupId": gA, "permissions": "video"}, &r)

	snapA := addSnapshotFile(t, app, chA, "a/1.jpg")
	snapB := addSnapshotFile(t, app, chB, "b/2.jpg")

	code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapA), restricted, nil, &r)
	if code != http.StatusOK {
		t.Fatalf("authorised snapshot: got %d, want 200: %+v", code, r)
	}
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapB), restricted, nil, &r)
	if code != http.StatusForbidden {
		t.Fatalf("unauthorised snapshot: got %d, want 403", code)
	}

	// Legacy clients (AI events / dataset samples) only know the original URL.
	// :id=0 is accepted when ?url= is present, and an invalid :id is ignored.
	for _, pathID := range []string{"0", "999999"} {
		r = apiResp{}
		code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%s/access-url?url=%s",
			srv.URL, pathID, url.QueryEscape("/snapshots/a/1.jpg")), restricted, nil, &r)
		if code != http.StatusOK || r.Code != 0 {
			t.Fatalf("url-form access (id=%s): got %d %+v", pathID, code, r)
		}
		var byURL struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(r.Data, &byURL); err != nil {
			t.Fatalf("decode url-form access: %v", err)
		}
		if code, body := getRaw(t, srv.URL+byURL.URL); code != http.StatusOK || body != "JPEG:a/1.jpg" {
			t.Fatalf("url-form token should bind snapA: %d %q", code, body)
		}
	}

	// Cross-channel URLs and unknown URLs are still 403 / 404.
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/0/access-url?url=%s",
		srv.URL, url.QueryEscape("/snapshots/b/2.jpg")), restricted, nil, &r)
	if code != http.StatusForbidden {
		t.Fatalf("url-form unauthorised: got %d, want 403", code)
	}
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/0/access-url?url=%s",
		srv.URL, url.QueryEscape("/snapshots/missing.jpg")), restricted, nil, &r)
	if code != http.StatusNotFound {
		t.Fatalf("url-form unknown: got %d, want 404", code)
	}

	type snapItem struct {
		ID        uint `json:"id"`
		ChannelID uint `json:"channelId"`
	}
	type snapList struct {
		Items []snapItem `json:"items"`
		Total int        `json:"total"`
	}
	var list snapList
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/snapshots", restricted, nil, &r)
	json.Unmarshal(r.Data, &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != snapA {
		t.Fatalf("restricted list should contain only snapA: %+v", list)
	}
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/snapshots", admin, nil, &r)
	json.Unmarshal(r.Data, &list)
	if list.Total != 2 {
		t.Fatalf("admin list should contain both snapshots: %+v", list)
	}
	// Cross-group channelId is rejected by the route's requireGroupPerm.
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots?channelId=%d", srv.URL, chB), restricted, nil, &r)
	if code != http.StatusForbidden {
		t.Fatalf("cross-group channelId filter: got %d, want 403", code)
	}
	// An authorised channelId yields only that channel's snapshot.
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots?channelId=%d", srv.URL, chA), restricted, nil, &r)
	if code != http.StatusOK {
		t.Fatalf("authorised channelId filter: got %d, want 200", code)
	}
	json.Unmarshal(r.Data, &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != snapA {
		t.Fatalf("authorised channelId filter mismatch: %+v", list)
	}

	code = doJSON(t, http.MethodDelete, fmt.Sprintf("%s/api/v1/snapshots/%d", srv.URL, snapB), restricted, nil, &r)
	if code != http.StatusForbidden {
		t.Fatalf("delete unauthorised: got %d, want 403", code)
	}
	code = doJSON(t, http.MethodDelete, fmt.Sprintf("%s/api/v1/snapshots/%d", srv.URL, snapA), restricted, nil, &r)
	if code != http.StatusOK {
		t.Fatalf("delete authorised: got %d, want 200: %+v", code, r)
	}
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/snapshots", restricted, nil, &r)
	json.Unmarshal(r.Data, &list)
	if list.Total != 0 {
		t.Fatalf("list after delete should be empty: %+v", list)
	}
}

func TestSnapshotPolicyFallbackForUngroupedChannel(t *testing.T) {
	srv, _, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)

	// Custom role without RBAC "video", but with a policy granting video read.
	var roleResp apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/roles", admin, map[string]any{
		"name": "snapshot-policy", "permissions": "event",
	}, &roleResp)
	if roleResp.Code != 0 {
		t.Fatalf("create role: %+v", roleResp)
	}
	if _, err := app.policy.AddPolicy("role:snapshot-policy", "video", "read", "*"); err != nil {
		t.Fatalf("add policy: %v", err)
	}
	_, policyUser := snapshotUser(t, srv, admin, "snapshot-policy-user", "snapshot-policy")

	_, groupedCh := newSnapshotChannel(t, app, "policy-grouped")
	_, freeCh := newSnapshotChannel(t, app, "policy-free")
	g := snapshotGroup(t, srv, admin, "policy-group")
	var r apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin,
		map[string]any{"groupId": g, "channelIds": []uint{groupedCh}}, &r)
	groupedSnap := addSnapshotFile(t, app, groupedCh, "g/1.jpg")
	freeSnap := addSnapshotFile(t, app, freeCh, "f/2.jpg")

	code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, freeSnap), policyUser, nil, &r)
	if code != http.StatusOK {
		t.Fatalf("ungrouped channel should fall back to policy: got %d, want 200", code)
	}
	code = doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, groupedSnap), policyUser, nil, &r)
	if code != http.StatusForbidden {
		t.Fatalf("grouped channel needs group grant: got %d, want 403", code)
	}

	var list struct {
		Total int `json:"total"`
	}
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/snapshots", policyUser, nil, &r)
	json.Unmarshal(r.Data, &list)
	if list.Total != 1 {
		t.Fatalf("policy fallback list should contain only the ungrouped snapshot: %+v", list)
	}
}
