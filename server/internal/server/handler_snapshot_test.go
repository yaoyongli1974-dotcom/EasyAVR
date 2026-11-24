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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

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

func snapshotUserID(t *testing.T, app *App, username string) uint {
	t.Helper()
	var u model.User
	if err := app.db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("find user %s: %v", username, err)
	}
	return u.ID
}

func snapshotRole(t *testing.T, srv *httptest.Server, admin, name, permissions string) {
	t.Helper()
	var r apiResp
	doJSON(t, http.MethodPost, srv.URL+"/api/v1/roles", admin, map[string]any{
		"name": name, "permissions": permissions,
	}, &r)
	if r.Code != 0 {
		t.Fatalf("create role %s: %+v", name, r)
	}
}

// snapshotSignedTokenFromAccessURL goes through the real issuance endpoint and
// returns the raw token (without the route prefix).
func snapshotSignedTokenFromAccessURL(t *testing.T, srv *httptest.Server, jwt string, snapID uint) string {
	t.Helper()
	var r apiResp
	code := doJSON(t, http.MethodGet, fmt.Sprintf("%s/api/v1/snapshots/%d/access-url", srv.URL, snapID), jwt, nil, &r)
	if code != http.StatusOK || r.Code != 0 {
		t.Fatalf("access-url %d: got %d %+v", snapID, code, r)
	}
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatalf("decode access-url: %v", err)
	}
	token := strings.TrimPrefix(data.URL, "/api/v1/snapshot-access/")
	if token == data.URL {
		t.Fatalf("unexpected access-url: %q", data.URL)
	}
	return token
}

func snapshotSignedGetWithBearer(t *testing.T, srv *httptest.Server, token, bearer string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/snapshot-access/"+token, nil)
	if err != nil {
		t.Fatalf("request signed access: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get signed access: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read signed access: %v", err)
	}
	return resp.StatusCode, string(body)
}

func snapshotSignedGet(t *testing.T, srv *httptest.Server, token string) (int, string) {
	t.Helper()
	return snapshotSignedGetWithBearer(t, srv, token, "")
}

func assertSnapshotAccessNoLocalPath(t *testing.T, app *App, body string) {
	t.Helper()
	for _, p := range []string{app.snaps.Dir(), filepath.Dir(app.snaps.Dir())} {
		if strings.Contains(body, p) {
			t.Fatalf("response leaks local path %q: %q", p, body)
		}
	}
}

// TestSnapshotSignedAccessRevalidatesUser exercises the public signed-access
// route: the token's user ID, not any request JWT, is reloaded from the DB and
// its current role/group permissions are rechecked on every request.
func TestSnapshotSignedAccessRevalidatesUser(t *testing.T) {
	t.Run("authorized", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		_, chID := newSnapshotChannel(t, app, "signed-ok")
		snapID := addSnapshotFile(t, app, chID, "ok/1.jpg")
		token := snapshotSignedTokenFromAccessURL(t, srv, admin, snapID)
		if code, body := snapshotSignedGet(t, srv, token); code != http.StatusOK || body != "JPEG:ok/1.jpg" {
			t.Fatalf("authorized signed access: got %d %q, want 200 JPEG", code, body)
		}
	})

	t.Run("tampered token", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		_, chID := newSnapshotChannel(t, app, "signed-tampered")
		snapID := addSnapshotFile(t, app, chID, "t/1.jpg")
		token := snapshotSignedTokenFromAccessURL(t, srv, admin, snapID)
		parts := strings.Split(token, ".")
		payload := []byte(parts[0])
		if payload[3] == 'A' {
			payload[3] = 'B'
		} else {
			payload[3] = 'A'
		}
		code, body := snapshotSignedGet(t, srv, string(payload)+"."+parts[1])
		if code != http.StatusUnauthorized {
			t.Fatalf("tampered token: got %d, want 401", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
	})

	t.Run("expired token", func(t *testing.T) {
		srv, cfg, app := newSnapshotTestServer(t)
		_, chID := newSnapshotChannel(t, app, "signed-expired")
		snapID := addSnapshotFile(t, app, chID, "e/1.jpg")
		adminID := snapshotUserID(t, app, cfg.AdminUser)
		expired, _, err := snapshot.SnapshotAccessToken(snapID, adminID, -time.Minute, cfg.JWTSecret)
		if err != nil {
			t.Fatalf("issue expired token: %v", err)
		}
		code, body := snapshotSignedGet(t, srv, expired)
		if code != http.StatusUnauthorized {
			t.Fatalf("expired token: got %d, want 401", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
	})

	t.Run("user deleted", func(t *testing.T) {
		srv, cfg, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		userID, userJWT := snapshotUser(t, srv, admin, "signed-deleted", "admin")
		_, chID := newSnapshotChannel(t, app, "signed-deleted-ch")
		snapID := addSnapshotFile(t, app, chID, "d/1.jpg")
		token := snapshotSignedTokenFromAccessURL(t, srv, userJWT, snapID)
		if err := app.db.Delete(&model.User{}, userID).Error; err != nil {
			t.Fatalf("delete user: %v", err)
		}
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Fatalf("deleted user: got %d, want 401 or 403", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
		// A token with a user ID that never existed is equally rejected.
		orphan, _, err := snapshot.SnapshotAccessToken(snapID, 999999, time.Minute, cfg.JWTSecret)
		if err != nil {
			t.Fatalf("issue orphan token: %v", err)
		}
		if code, body := snapshotSignedGet(t, srv, orphan); code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Fatalf("unknown user: got %d, want 401 or 403 (%s)", code, body)
		}
	})

	t.Run("user disabled", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		snapshotRole(t, srv, admin, "signed-video", "video")
		userID, userJWT := snapshotUser(t, srv, admin, "signed-disabled", "signed-video")
		_, chID := newSnapshotChannel(t, app, "signed-disabled-ch")
		snapID := addSnapshotFile(t, app, chID, "disabled/1.jpg")
		token := snapshotSignedTokenFromAccessURL(t, srv, userJWT, snapID)
		if code, _ := snapshotSignedGet(t, srv, token); code != http.StatusOK {
			t.Fatalf("precondition: token should work while enabled, got %d", code)
		}
		if err := app.db.Model(&model.User{}).Where("id = ?", userID).Update("enabled", false).Error; err != nil {
			t.Fatalf("disable user: %v", err)
		}
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusForbidden {
			t.Fatalf("disabled user: got %d, want 403", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
	})

	t.Run("user removed from device group", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		snapshotRole(t, srv, admin, "signed-limited", "event")
		userID, userJWT := snapshotUser(t, srv, admin, "signed-grouped", "signed-limited")
		devID, chID := newSnapshotChannel(t, app, "signed-grouped-ch")
		gID := snapshotGroup(t, srv, admin, "signed-grouped-group")
		var r apiResp
		doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin,
			map[string]any{"groupId": gID, "channelIds": []uint{chID}}, &r)
		doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/devices/bind", admin,
			map[string]any{"groupId": gID, "deviceIds": []uint{devID}}, &r)
		doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/user-groups", admin,
			map[string]any{"userId": userID, "groupId": gID, "permissions": "video"}, &r)
		snapID := addSnapshotFile(t, app, chID, "grouped/1.jpg")
		token := snapshotSignedTokenFromAccessURL(t, srv, userJWT, snapID)
		if code, _ := snapshotSignedGet(t, srv, token); code != http.StatusOK {
			t.Fatalf("precondition: group grant should allow access, got %d", code)
		}
		if err := app.db.Where("user_id = ? AND group_id = ?", userID, gID).Delete(&model.UserGroup{}).Error; err != nil {
			t.Fatalf("remove user group: %v", err)
		}
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusForbidden {
			t.Fatalf("user removed from group: got %d, want 403", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
	})

	t.Run("video permission revoked", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		snapshotRole(t, srv, admin, "signed-revoked", "video")
		snapshotRole(t, srv, admin, "signed-no-video", "event")
		userID, userJWT := snapshotUser(t, srv, admin, "signed-revoke-user", "signed-revoked")
		_, chID := newSnapshotChannel(t, app, "signed-revoke-ch")
		snapID := addSnapshotFile(t, app, chID, "revoke/1.jpg")
		token := snapshotSignedTokenFromAccessURL(t, srv, userJWT, snapID)
		if code, _ := snapshotSignedGet(t, srv, token); code != http.StatusOK {
			t.Fatalf("precondition: role video permission should allow access, got %d", code)
		}
		// Role permissions are read from the DB on every signed request.
		app.db.Model(&model.Role{}).Where("name = ?", "signed-revoked").Update("permissions", "event")
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusForbidden {
			t.Fatalf("role lost video permission: got %d, want 403", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
		// Restore the role, then change the user's role: the current DB role
		// must win over the role embedded in the caller's JWT at issuance.
		app.db.Model(&model.Role{}).Where("name = ?", "signed-revoked").Update("permissions", "video")
		app.db.Model(&model.User{}).Where("id = ?", userID).Update("role", "signed-no-video")
		if code, body := snapshotSignedGet(t, srv, token); code != http.StatusForbidden {
			t.Fatalf("user changed to role without video: got %d, want 403 (%s)", code, body)
		}
	})

	t.Run("another user's token", func(t *testing.T) {
		srv, cfg, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		snapshotRole(t, srv, admin, "signed-other-limited", "event")
		userAID, userAJWT := snapshotUser(t, srv, admin, "signed-user-a", "signed-other-limited")
		userBID, userBJWT := snapshotUser(t, srv, admin, "signed-user-b", "signed-other-limited")
		devID, chID := newSnapshotChannel(t, app, "signed-other-ch")
		gID := snapshotGroup(t, srv, admin, "signed-other-group")
		var r apiResp
		doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/channels/bind", admin,
			map[string]any{"groupId": gID, "channelIds": []uint{chID}}, &r)
		doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/devices/bind", admin,
			map[string]any{"groupId": gID, "deviceIds": []uint{devID}}, &r)
		doJSON(t, http.MethodPost, srv.URL+"/api/v1/groups/user-groups", admin,
			map[string]any{"userId": userAID, "groupId": gID, "permissions": "video"}, &r)
		snapID := addSnapshotFile(t, app, chID, "other/1.jpg")

		// The token is bound to A (the user who obtained it by signing in).
		tokenA := snapshotSignedTokenFromAccessURL(t, srv, userAJWT, snapID)
		if code, _ := snapshotSignedGet(t, srv, tokenA); code != http.StatusOK {
			t.Fatalf("token bound to authorised user: got %d, want 200", code)
		}
		// A token bound to B, who has no grant on this channel, is refused.
		tokenB, _, err := snapshot.SnapshotAccessToken(snapID, userBID, time.Minute, cfg.JWTSecret)
		if err != nil {
			t.Fatalf("issue token for B: %v", err)
		}
		code, body := snapshotSignedGet(t, srv, tokenB)
		if code != http.StatusForbidden {
			t.Fatalf("token bound to unauthorised user: got %d, want 403", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
		// The public route ignores the request's own JWT: presenting B's JWT
		// alongside A's token must not change the decision (A is authorised).
		if code, body := snapshotSignedGetWithBearer(t, srv, tokenA, userBJWT); code != http.StatusOK {
			t.Fatalf("request JWT must not override token user: got %d (%s)", code, body)
		}
	})

	t.Run("snapshot missing", func(t *testing.T) {
		srv, cfg, app := newSnapshotTestServer(t)
		adminID := snapshotUserID(t, app, cfg.AdminUser)
		token, _, err := snapshot.SnapshotAccessToken(999999, adminID, time.Minute, cfg.JWTSecret)
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusNotFound {
			t.Fatalf("missing snapshot: got %d, want 404", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
	})

	t.Run("snapshot file missing", func(t *testing.T) {
		srv, _, app := newSnapshotTestServer(t)
		admin := login(t, srv.URL)
		_, chID := newSnapshotChannel(t, app, "signed-no-file")
		snap := model.Snapshot{
			ChannelID: chID,
			Path:      "gone/1.jpg",
			URL:       "/snapshots/gone/1.jpg",
			TakenAt:   time.Now(),
		}
		if err := app.db.Create(&snap).Error; err != nil {
			t.Fatalf("create snapshot row: %v", err)
		}
		token := snapshotSignedTokenFromAccessURL(t, srv, admin, snap.ID)
		code, body := snapshotSignedGet(t, srv, token)
		if code != http.StatusNotFound {
			t.Fatalf("missing snapshot file: got %d, want 404", code)
		}
		assertSnapshotAccessNoLocalPath(t, app, body)
	})
}

// addSnapshotSymlink catalogs a symlink placed in the snapshot directory, so
// the public access route is exercised with a valid token for an escaping
// path.
func addSnapshotSymlink(t *testing.T, app *App, channelID uint, rel, target string) uint {
	t.Helper()
	full := filepath.Join(app.snaps.Dir(), rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, full); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
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

func TestSnapshotSignedAccessRejectsSymlinkEscape(t *testing.T) {
	srv, cfg, app := newSnapshotTestServer(t)
	adminID := snapshotUserID(t, app, cfg.AdminUser)
	_, chID := newSnapshotChannel(t, app, "escape-channel")

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.jpg")
	if err := os.WriteFile(outside, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	cases := []struct {
		name string
		rel  string
		link string
	}{
		{"direct symlink", "direct.jpg", outside},
		{"multi-level symlink", "multi.jpg", filepath.Join(app.snaps.Dir(), "hop.jpg")},
		{"symlink to outside directory", "outdir/secret.jpg", outsideDir},
	}
	// The intermediate hop for the multi-level case.
	if err := os.Symlink(outside, filepath.Join(app.snaps.Dir(), "hop.jpg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := addSnapshotSymlink(t, app, chID, tc.rel, tc.link)
			token, _, err := snapshot.SnapshotAccessToken(id, adminID, time.Minute, cfg.JWTSecret)
			if err != nil {
				t.Fatalf("issue token: %v", err)
			}
			code, body := snapshotSignedGet(t, srv, token)
			if code != http.StatusForbidden {
				t.Fatalf("symlink escape: got %d %q, want 403", code, body)
			}
			assertSnapshotAccessNoLocalPath(t, app, body)
			if strings.Contains(body, "TOP-SECRET") {
				t.Fatalf("response leaked outside file content: %q", body)
			}
		})
	}
}

func TestSnapshotSignedAccessConcurrentDelete(t *testing.T) {
	srv, cfg, app := newSnapshotTestServer(t)
	adminID := snapshotUserID(t, app, cfg.AdminUser)
	_, chID := newSnapshotChannel(t, app, "concurrent-channel")

	const files, readers = 6, 6
	paths := make([]string, files)
	tokens := make([]string, files)
	for i := 0; i < files; i++ {
		rel := fmt.Sprintf("c/%d.jpg", i)
		id := addSnapshotFile(t, app, chID, rel)
		paths[i] = filepath.Join(app.snaps.Dir(), rel)
		tok, _, err := snapshot.SnapshotAccessToken(id, adminID, time.Minute, cfg.JWTSecret)
		if err != nil {
			t.Fatalf("issue token: %v", err)
		}
		tokens[i] = tok
		// Precondition: a normal snapshot is served before any deletion.
		if code, body := snapshotSignedGet(t, srv, tok); code != http.StatusOK || body != "JPEG:"+rel {
			t.Fatalf("precondition: got %d %q, want 200 %q", code, body, "JPEG:"+rel)
		}
	}

	var bad, served atomic.Int64
	var wg, firstWG sync.WaitGroup
	start := make(chan struct{})
	firstWG.Add(readers)
	for w := 0; w < readers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 40; i++ {
				idx := (i + w) % files
				resp, err := http.Get(srv.URL + "/api/v1/snapshot-access/" + tokens[idx])
				if err != nil {
					bad.Add(1)
					if i == 0 {
						firstWG.Done()
					}
					continue
				}
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					bad.Add(1)
				} else {
					switch resp.StatusCode {
					case http.StatusOK:
						if string(body) == fmt.Sprintf("JPEG:c/%d.jpg", idx) {
							served.Add(1)
						} else {
							bad.Add(1)
						}
					case http.StatusNotFound:
						// The file was removed mid-request: allowed.
					default:
						bad.Add(1)
					}
					if strings.Contains(string(body), app.snaps.Dir()) {
						bad.Add(1)
					}
				}
				if i == 0 {
					firstWG.Done()
				}
			}
		}(w)
	}

	close(start)
	firstWG.Wait()
	for _, p := range paths {
		_ = os.Remove(p)
	}
	wg.Wait()

	if served.Load() == 0 {
		t.Fatal("no snapshot was served while deletions were in flight")
	}
	if bad.Load() != 0 {
		t.Fatalf("unexpected responses during concurrent access: %d", bad.Load())
	}
}

// TestSnapshotHTTPUsesOpenedHandle proves the HTTP layer serves bytes from the
// handle returned by OpenSnapshot: after the catalog path is replaced with a
// different file, responding with that handle still yields the original data.
func TestSnapshotHTTPUsesOpenedHandle(t *testing.T) {
	srv, _, app := newSnapshotTestServer(t)
	admin := login(t, srv.URL)
	_, chID := newSnapshotChannel(t, app, "opened-handle")
	snapID := addSnapshotFile(t, app, chID, "h/1.jpg")
	token := snapshotSignedTokenFromAccessURL(t, srv, admin, snapID)
	if code, body := snapshotSignedGet(t, srv, token); code != http.StatusOK || body != "JPEG:h/1.jpg" {
		t.Fatalf("precondition: got %d %q, want 200 JPEG:h/1.jpg", code, body)
	}

	f, err := app.snaps.OpenSnapshot("h/1.jpg")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	full := filepath.Join(app.snaps.Dir(), "h", "1.jpg")
	if err := os.Remove(full); err != nil {
		t.Fatalf("remove original: %v", err)
	}
	if err := os.WriteFile(full, []byte("REPLACED"), 0o644); err != nil {
		t.Fatalf("write replacement: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/snapshot-access/"+token, nil)
	app.serveSnapshotFile(c, "h/1.jpg", f)

	if rec.Code != http.StatusOK {
		t.Fatalf("serve from opened handle: got %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "JPEG:h/1.jpg" {
		t.Fatalf("served %q, want bytes from the already-opened handle", body)
	}
}
