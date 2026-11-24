package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/security"
)

func withCredentialEncryptor(t *testing.T) {
	t.Helper()
	e, err := security.New("handler-write-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })
}

func openCredentialDB(t *testing.T, cfg *config.Config) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(cfg.DBPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

// credentialApp builds a second App over the same test database so handlers
// that are not yet wired into router.go can be exercised directly.
func credentialApp(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	return New(cfg, openCredentialDB(t, cfg))
}

func callCredentialHandler(t *testing.T, app *App, method, route, target string, body any, h gin.HandlerFunc) (int, apiResp) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Handle(method, route, h)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	var out apiResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s: %v (%s)", method, target, err, w.Body.String())
	}
	return w.Code, out
}

func requireNoSecret(t *testing.T, raw []byte, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s == "" {
			continue
		}
		if strings.Contains(string(raw), s) {
			t.Fatalf("response echoed secret %q: %s", s, raw)
		}
	}
}

func rawCredential(t *testing.T, db *gorm.DB, table string, id uint, column string) string {
	t.Helper()
	var raw string
	if err := db.Table(table).Where("id = ?", id).Pluck(column, &raw).Error; err != nil {
		t.Fatal(err)
	}
	return raw
}

// ---- AI provider ----

func TestAIProviderSecretWriteDTO(t *testing.T) {
	withCredentialEncryptor(t)
	srv, cfg := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	const key = "sk-provider-write-secret"

	var created apiResp
	if code := doJSON(t, http.MethodPost, base+"/ai/providers", admin, map[string]any{
		"name": "secure-cv", "kind": "cv", "endpoint": "http://127.0.0.1:1", "apiKey": key,
	}, &created); code != http.StatusOK || created.Code != 0 {
		t.Fatalf("create provider: status=%d resp=%+v", code, created)
	}
	requireNoSecret(t, created.Data, key)
	var item struct {
		ID         uint `json:"id"`
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(created.Data, &item); err != nil {
		t.Fatal(err)
	}
	if item.ID == 0 || !item.Configured {
		t.Fatalf("create response: %s", created.Data)
	}

	var listed apiResp
	doJSON(t, http.MethodGet, base+"/ai/providers", admin, nil, &listed)
	requireNoSecret(t, listed.Data, key)
	var items []struct {
		ID         uint `json:"id"`
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(listed.Data, &items); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range items {
		if v.ID == item.ID {
			found = v.Configured
		}
	}
	if !found {
		t.Fatalf("list does not report configured=true: %s", listed.Data)
	}

	db := openCredentialDB(t, cfg)
	stored := rawCredential(t, db, "a_iproviders", item.ID, "api_key")
	if !strings.HasPrefix(stored, "enc:v1:") || strings.Contains(stored, key) {
		t.Fatalf("api key stored in plaintext: %q", stored)
	}
	var read model.AIProvider
	if err := db.First(&read, item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if read.APIKey != key {
		t.Fatalf("model hook did not decrypt api key: %q", read.APIKey)
	}

	url := base + "/ai/providers/" + uintToString(item.ID)
	var kept apiResp
	if code := doJSON(t, http.MethodPut, url, admin, map[string]any{"name": "renamed", "apiKey": ""}, &kept); code != http.StatusOK || kept.Code != 0 {
		t.Fatalf("empty apiKey update: status=%d resp=%+v", code, kept)
	}
	requireNoSecret(t, kept.Data, key)
	var keptView struct {
		Name       string `json:"name"`
		Configured bool   `json:"configured"`
	}
	json.Unmarshal(kept.Data, &keptView)
	if keptView.Name != "renamed" || !keptView.Configured {
		t.Fatalf("empty apiKey update response: %s", kept.Data)
	}
	var afterKeep model.AIProvider
	db.First(&afterKeep, item.ID)
	if afterKeep.APIKey != key {
		t.Fatalf("empty apiKey cleared the stored key: %q", afterKeep.APIKey)
	}

	var cleared apiResp
	if code := doJSON(t, http.MethodPut, url, admin, map[string]any{"clearApiKey": true}, &cleared); code != http.StatusOK || cleared.Code != 0 {
		t.Fatalf("clearApiKey update: status=%d resp=%+v", code, cleared)
	}
	var clearedView struct {
		Configured bool `json:"configured"`
	}
	json.Unmarshal(cleared.Data, &clearedView)
	if clearedView.Configured {
		t.Fatalf("clearApiKey still reports configured: %s", cleared.Data)
	}
	var afterClear model.AIProvider
	db.First(&afterClear, item.ID)
	if afterClear.APIKey != "" {
		t.Fatalf("clearApiKey did not clear the key: %q", afterClear.APIKey)
	}
}

func TestAIProviderDecryptedKeyReachesWorker(t *testing.T) {
	withCredentialEncryptor(t)
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	const key = "cv-worker-decrypted-key"

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("bad key"))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"eventType": "decrypted_key_ok", "level": "info", "summary": "ok",
		})
	}))
	defer worker.Close()

	var devResp apiResp
	doJSON(t, http.MethodPost, base+"/devices", admin, map[string]any{
		"name": "dto-cam", "protocol": "rtsp", "ip": "10.0.0.99",
	}, &devResp)
	var dev struct {
		ID       uint `json:"id"`
		Channels []struct {
			ID uint `json:"id"`
		} `json:"channels"`
	}
	json.Unmarshal(devResp.Data, &dev)
	if dev.ID == 0 || len(dev.Channels) == 0 {
		t.Fatalf("device setup: %s", devResp.Data)
	}
	doJSON(t, http.MethodPut, base+"/channels/"+uintToString(dev.Channels[0].ID), admin,
		map[string]any{"sourceUrl": "http://example.invalid/live"}, nil)

	var provResp apiResp
	doJSON(t, http.MethodPost, base+"/ai/providers", admin, map[string]any{
		"name": "keyed-cv", "kind": "cv", "endpoint": worker.URL, "apiKey": key,
	}, &provResp)
	var prov struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(provResp.Data, &prov)

	var taskResp apiResp
	doJSON(t, http.MethodPost, base+"/ai/tasks", admin, map[string]any{
		"name": "dto-task", "channelId": dev.Channels[0].ID, "providerId": prov.ID,
		"taskType": "cv_detect", "config": `{"intervalSec":30,"grabFrame":false}`,
	}, &taskResp)
	var task struct {
		ID uint `json:"id"`
	}
	json.Unmarshal(taskResp.Data, &task)

	var run apiResp
	if code := doJSON(t, http.MethodPost, base+"/ai/tasks/"+uintToString(task.ID)+"/run", admin, nil, &run); code != http.StatusOK || run.Code != 0 {
		t.Fatalf("run task with decrypted key: status=%d resp=%+v", code, run)
	}
	var result struct {
		Count int `json:"count"`
	}
	json.Unmarshal(run.Data, &result)
	if result.Count != 1 {
		t.Fatalf("worker did not receive the decrypted key: %s", run.Data)
	}
}

// ---- notification channels ----

func TestNotifyChannelSecretWriteDTO(t *testing.T) {
	withCredentialEncryptor(t)
	srv, cfg := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	const secret = "webhook-secret-123"
	const smtpPass = "smtp-password-456"

	var created apiResp
	if code := doJSON(t, http.MethodPost, base+"/notify/channels", admin, map[string]any{
		"name": "hook", "type": "webhook", "url": "http://127.0.0.1:1",
		"secret": secret, "smtpPassword": smtpPass,
	}, &created); code != http.StatusOK || created.Code != 0 {
		t.Fatalf("create channel: status=%d resp=%+v", code, created)
	}
	requireNoSecret(t, created.Data, secret, smtpPass)
	var item struct {
		ID                     uint `json:"id"`
		SecretConfigured       bool `json:"secretConfigured"`
		SMTPPasswordConfigured bool `json:"smtpPasswordConfigured"`
	}
	json.Unmarshal(created.Data, &item)
	if item.ID == 0 || !item.SecretConfigured || !item.SMTPPasswordConfigured {
		t.Fatalf("create response: %s", created.Data)
	}

	var listed apiResp
	doJSON(t, http.MethodGet, base+"/notify/channels", admin, nil, &listed)
	requireNoSecret(t, listed.Data, secret, smtpPass)
	var items []struct {
		ID                     uint `json:"id"`
		SecretConfigured       bool `json:"secretConfigured"`
		SMTPPasswordConfigured bool `json:"smtpPasswordConfigured"`
	}
	json.Unmarshal(listed.Data, &items)
	found := false
	for _, v := range items {
		if v.ID == item.ID {
			found = v.SecretConfigured && v.SMTPPasswordConfigured
		}
	}
	if !found {
		t.Fatalf("list flags: %s", listed.Data)
	}

	db := openCredentialDB(t, cfg)
	if raw := rawCredential(t, db, "notification_channels", item.ID, "secret"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, secret) {
		t.Fatalf("webhook secret stored in plaintext: %q", raw)
	}
	if raw := rawCredential(t, db, "notification_channels", item.ID, "smtp_password"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, smtpPass) {
		t.Fatalf("smtp password stored in plaintext: %q", raw)
	}

	url := base + "/notify/channels/" + uintToString(item.ID)
	var kept apiResp
	doJSON(t, http.MethodPut, url, admin, map[string]any{"secret": "", "smtpPassword": ""}, &kept)
	requireNoSecret(t, kept.Data, secret, smtpPass)
	var afterKeep model.NotificationChannel
	db.First(&afterKeep, item.ID)
	if afterKeep.Secret != secret || afterKeep.SMTPPassword != smtpPass {
		t.Fatalf("empty secrets cleared the stored ones: %q / %q", afterKeep.Secret, afterKeep.SMTPPassword)
	}

	var cleared apiResp
	doJSON(t, http.MethodPut, url, admin, map[string]any{"clearSecret": true}, &cleared)
	var clearedView struct {
		SecretConfigured       bool `json:"secretConfigured"`
		SMTPPasswordConfigured bool `json:"smtpPasswordConfigured"`
	}
	json.Unmarshal(cleared.Data, &clearedView)
	if clearedView.SecretConfigured || !clearedView.SMTPPasswordConfigured {
		t.Fatalf("clearSecret flags: %s", cleared.Data)
	}
	var afterClear model.NotificationChannel
	db.First(&afterClear, item.ID)
	if afterClear.Secret != "" || afterClear.SMTPPassword != smtpPass {
		t.Fatalf("clearSecret result: %q / %q", afterClear.Secret, afterClear.SMTPPassword)
	}

	doJSON(t, http.MethodPut, url, admin, map[string]any{"clearSmtpPassword": true}, &cleared)
	var final model.NotificationChannel
	db.First(&final, item.ID)
	if final.SMTPPassword != "" {
		t.Fatalf("clearSmtpPassword did not clear: %q", final.SMTPPassword)
	}
}

// ---- GB28181 cascade / white list ----

func TestGBSecretWriteDTO(t *testing.T) {
	withCredentialEncryptor(t)
	srv, cfg := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	app := credentialApp(t, cfg)
	const casPass = "gb-cascade-password"
	const whitePass = "gb-whitelist-password"

	var created apiResp
	if code := doJSON(t, http.MethodPost, base+"/gb/cascades", admin, map[string]any{
		"targetId": "34020000002000000001", "targetIp": "10.0.0.2", "password": casPass, "enabled": false,
	}, &created); code != http.StatusOK || created.Code != 0 {
		t.Fatalf("create cascade: status=%d resp=%+v", code, created)
	}
	requireNoSecret(t, created.Data, casPass)
	var cas struct {
		ID                 uint `json:"id"`
		PasswordConfigured bool `json:"passwordConfigured"`
	}
	json.Unmarshal(created.Data, &cas)
	if !cas.PasswordConfigured {
		t.Fatalf("cascade response: %s", created.Data)
	}

	var listed apiResp
	doJSON(t, http.MethodGet, base+"/gb/cascades", admin, nil, &listed)
	requireNoSecret(t, listed.Data, casPass)
	if !strings.Contains(string(listed.Data), `"passwordConfigured":true`) {
		t.Fatalf("cascade list flags: %s", listed.Data)
	}

	db := openCredentialDB(t, cfg)
	if raw := rawCredential(t, db, "gb_cascades", cas.ID, "password"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, casPass) {
		t.Fatalf("cascade password stored in plaintext: %q", raw)
	}

	route := "/gb/cascades/:id"
	target := "/gb/cascades/" + uintToString(cas.ID)
	code, kept := callCredentialHandler(t, app, http.MethodPut, route, target,
		map[string]any{"password": "", "enabled": false}, app.updateCascade)
	if code != http.StatusOK || kept.Code != 0 {
		t.Fatalf("empty cascade password update: status=%d resp=%+v", code, kept)
	}
	requireNoSecret(t, kept.Data, casPass)
	var afterKeep model.GBCascade
	db.First(&afterKeep, cas.ID)
	if afterKeep.Password != casPass {
		t.Fatalf("empty cascade password cleared it: %q", afterKeep.Password)
	}
	code, cleared := callCredentialHandler(t, app, http.MethodPut, route, target,
		map[string]any{"clearPassword": true, "enabled": false}, app.updateCascade)
	if code != http.StatusOK || cleared.Code != 0 {
		t.Fatalf("clear cascade password: status=%d resp=%+v", code, cleared)
	}
	if strings.Contains(string(cleared.Data), `"passwordConfigured":true`) {
		t.Fatalf("clearPassword flags: %s", cleared.Data)
	}
	var afterClear model.GBCascade
	db.First(&afterClear, cas.ID)
	if afterClear.Password != "" {
		t.Fatalf("clearPassword did not clear: %q", afterClear.Password)
	}

	var whiteCreated apiResp
	if code := doJSON(t, http.MethodPost, base+"/gb/whitelist", admin, map[string]any{
		"deviceId": "34020000001320000001", "password": whitePass,
	}, &whiteCreated); code != http.StatusOK || whiteCreated.Code != 0 {
		t.Fatalf("create whitelist: status=%d resp=%+v", code, whiteCreated)
	}
	requireNoSecret(t, whiteCreated.Data, whitePass)
	var white struct {
		ID                 uint `json:"id"`
		PasswordConfigured bool `json:"passwordConfigured"`
	}
	json.Unmarshal(whiteCreated.Data, &white)
	if !white.PasswordConfigured {
		t.Fatalf("whitelist response: %s", whiteCreated.Data)
	}
	var whiteListed apiResp
	doJSON(t, http.MethodGet, base+"/gb/whitelist", admin, nil, &whiteListed)
	requireNoSecret(t, whiteListed.Data, whitePass)
	if !strings.Contains(string(whiteListed.Data), `"passwordConfigured":true`) {
		t.Fatalf("whitelist list flags: %s", whiteListed.Data)
	}
	if raw := rawCredential(t, db, "gb_white_lists", white.ID, "password"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, whitePass) {
		t.Fatalf("whitelist password stored in plaintext: %q", raw)
	}

	whiteRoute := "/gb/whitelist/:id"
	whiteTarget := "/gb/whitelist/" + uintToString(white.ID)
	code, keptWhite := callCredentialHandler(t, app, http.MethodPut, whiteRoute, whiteTarget,
		map[string]any{"password": ""}, app.updateWhiteList)
	if code != http.StatusOK || keptWhite.Code != 0 {
		t.Fatalf("empty whitelist password update: status=%d resp=%+v", code, keptWhite)
	}
	var afterKeepWhite model.GBWhiteList
	db.First(&afterKeepWhite, white.ID)
	if afterKeepWhite.Password != whitePass {
		t.Fatalf("empty whitelist password cleared it: %q", afterKeepWhite.Password)
	}
	code, clearedWhite := callCredentialHandler(t, app, http.MethodPut, whiteRoute, whiteTarget,
		map[string]any{"clearPassword": true}, app.updateWhiteList)
	if code != http.StatusOK || clearedWhite.Code != 0 {
		t.Fatalf("clear whitelist password: status=%d resp=%+v", code, clearedWhite)
	}
	var afterClearWhite model.GBWhiteList
	db.First(&afterClearWhite, white.ID)
	if afterClearWhite.Password != "" {
		t.Fatalf("clearPassword did not clear whitelist: %q", afterClearWhite.Password)
	}
}

// ---- GA/T1400 cascade ----

func TestGACascadeSecretWriteDTO(t *testing.T) {
	withCredentialEncryptor(t)
	srv, cfg := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	app := credentialApp(t, cfg)
	const password = "ga-cascade-password"

	var created apiResp
	if code := doJSON(t, http.MethodPost, base+"/ga1400/cascades", admin, map[string]any{
		"name": "upper", "url": "http://127.0.0.1:1", "username": "ga-user",
		"password": password, "enabled": false,
	}, &created); code != http.StatusOK || created.Code != 0 {
		t.Fatalf("create ga cascade: status=%d resp=%+v", code, created)
	}
	requireNoSecret(t, created.Data, password)
	var item struct {
		ID                 uint `json:"id"`
		PasswordConfigured bool `json:"passwordConfigured"`
	}
	json.Unmarshal(created.Data, &item)
	if !item.PasswordConfigured {
		t.Fatalf("create response: %s", created.Data)
	}

	var listed apiResp
	doJSON(t, http.MethodGet, base+"/ga1400/cascades", admin, nil, &listed)
	requireNoSecret(t, listed.Data, password)
	if !strings.Contains(string(listed.Data), `"passwordConfigured":true`) {
		t.Fatalf("ga cascade list flags: %s", listed.Data)
	}

	db := openCredentialDB(t, cfg)
	if raw := rawCredential(t, db, "ga1400_cascades", item.ID, "password"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, password) {
		t.Fatalf("ga cascade password stored in plaintext: %q", raw)
	}

	route := "/ga1400/cascades/:id"
	target := "/ga1400/cascades/" + uintToString(item.ID)
	code, kept := callCredentialHandler(t, app, http.MethodPut, route, target,
		map[string]any{"password": "", "enabled": false}, app.updateGACascade)
	if code != http.StatusOK || kept.Code != 0 {
		t.Fatalf("empty ga password update: status=%d resp=%+v", code, kept)
	}
	requireNoSecret(t, kept.Data, password)
	var afterKeep model.GA1400Cascade
	db.First(&afterKeep, item.ID)
	if afterKeep.Password != password {
		t.Fatalf("empty ga password cleared it: %q", afterKeep.Password)
	}
	code, cleared := callCredentialHandler(t, app, http.MethodPut, route, target,
		map[string]any{"clearPassword": true, "enabled": false}, app.updateGACascade)
	if code != http.StatusOK || cleared.Code != 0 {
		t.Fatalf("clear ga password: status=%d resp=%+v", code, cleared)
	}
	if strings.Contains(string(cleared.Data), `"passwordConfigured":true`) {
		t.Fatalf("clearPassword flags: %s", cleared.Data)
	}
	var afterClear model.GA1400Cascade
	db.First(&afterClear, item.ID)
	if afterClear.Password != "" {
		t.Fatalf("clearPassword did not clear: %q", afterClear.Password)
	}
}

// ---- cloud storage ----

func TestCloudStoreCredentialsAreNotEchoed(t *testing.T) {
	withCredentialEncryptor(t)
	srv, cfg := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	const accessKey = "AKIA-inline-access"
	const secretKey = "cloud-secret-key-789"

	var created apiResp
	if code := doJSON(t, http.MethodPost, base+"/storages", admin, map[string]any{
		"name": "s3", "endpoint": "http://127.0.0.1:1", "bucket": "testbucket",
		"accessKey": accessKey, "secretKey": secretKey,
	}, &created); code != http.StatusOK || created.Code != 0 {
		t.Fatalf("create store: status=%d resp=%+v", code, created)
	}
	requireNoSecret(t, created.Data, accessKey, secretKey)
	var item struct {
		ID           uint `json:"id"`
		HasAccessKey bool `json:"hasAccessKey"`
		HasSecret    bool `json:"hasSecret"`
	}
	json.Unmarshal(created.Data, &item)
	if !item.HasAccessKey || !item.HasSecret {
		t.Fatalf("create flags: %s", created.Data)
	}
	var rawMap map[string]any
	json.Unmarshal(created.Data, &rawMap)
	if _, ok := rawMap["accessKey"]; ok {
		t.Fatalf("create response still has accessKey: %s", created.Data)
	}
	if _, ok := rawMap["secretKey"]; ok {
		t.Fatalf("create response still has secretKey: %s", created.Data)
	}

	var listed apiResp
	doJSON(t, http.MethodGet, base+"/storages", admin, nil, &listed)
	requireNoSecret(t, listed.Data, accessKey, secretKey)
	var items []map[string]any
	json.Unmarshal(listed.Data, &items)
	for _, v := range items {
		if _, ok := v["accessKey"]; ok {
			t.Fatalf("list response still has accessKey: %s", listed.Data)
		}
		if _, ok := v["secretKey"]; ok {
			t.Fatalf("list response still has secretKey: %s", listed.Data)
		}
	}

	db := openCredentialDB(t, cfg)
	if raw := rawCredential(t, db, "cloud_stores", item.ID, "access_key"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, accessKey) {
		t.Fatalf("access key stored in plaintext: %q", raw)
	}
	if raw := rawCredential(t, db, "cloud_stores", item.ID, "secret_key"); !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, secretKey) {
		t.Fatalf("secret key stored in plaintext: %q", raw)
	}

	url := base + "/storages/" + uintToString(item.ID)
	var kept apiResp
	doJSON(t, http.MethodPut, url, admin, map[string]any{"accessKey": "", "secretKey": ""}, &kept)
	requireNoSecret(t, kept.Data, accessKey, secretKey)
	var afterKeep model.CloudStore
	db.First(&afterKeep, item.ID)
	if afterKeep.AccessKey != accessKey || afterKeep.SecretKey != secretKey {
		t.Fatalf("empty credentials cleared the stored ones: %q / %q", afterKeep.AccessKey, afterKeep.SecretKey)
	}
	var cleared apiResp
	doJSON(t, http.MethodPut, url, admin, map[string]any{"clearAccessKey": true}, &cleared)
	var clearedView struct {
		HasAccessKey bool `json:"hasAccessKey"`
		HasSecret    bool `json:"hasSecret"`
	}
	json.Unmarshal(cleared.Data, &clearedView)
	if clearedView.HasAccessKey || !clearedView.HasSecret {
		t.Fatalf("clearAccessKey flags: %s", cleared.Data)
	}
	var afterClear model.CloudStore
	db.First(&afterClear, item.ID)
	if afterClear.AccessKey != "" || afterClear.SecretKey != secretKey {
		t.Fatalf("clearAccessKey result: %q / %q", afterClear.AccessKey, afterClear.SecretKey)
	}

	// The test endpoint still accepts inline credentials without saving them.
	var authHeaders []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>testbucket</Name><IsTruncated>false</IsTruncated></ListBucketResult>`))
	}))
	defer fake.Close()
	var tested apiResp
	if code := doJSON(t, http.MethodPost, url+"/test", admin, map[string]any{
		"endpoint": fake.URL, "bucket": "testbucket", "pathStyle": true,
		"accessKey": accessKey, "secretKey": secretKey,
	}, &tested); code != http.StatusOK || tested.Code != 0 {
		t.Fatalf("inline test: status=%d resp=%+v", code, tested)
	}
	usedInline := false
	for _, h := range authHeaders {
		if strings.Contains(h, accessKey) {
			usedInline = true
		}
	}
	if !usedInline {
		t.Fatalf("inline access key was not used by the test endpoint: %v", authHeaders)
	}
	var still model.CloudStore
	db.First(&still, item.ID)
	if still.AccessKey != "" {
		t.Fatalf("test endpoint saved inline credentials: %q", still.AccessKey)
	}
}

// ---- permissions ----

func TestCredentialWritePermissions(t *testing.T) {
	srv, _ := newTestServer(t)
	admin := login(t, srv.URL)
	base := srv.URL + "/api/v1"
	for _, u := range []struct{ name, role string }{
		{"dto-viewer", "viewer"}, {"dto-operator", "operator"},
	} {
		var r apiResp
		doJSON(t, http.MethodPost, base+"/users", admin, map[string]any{
			"username": u.name, "password": "dto-pass-123", "role": u.role,
		}, &r)
		if r.Code != 0 {
			t.Fatalf("create %s: %+v", u.name, r)
		}
	}
	viewer := loginAs(t, srv.URL, "dto-viewer", "dto-pass-123")
	operator := loginAs(t, srv.URL, "dto-operator", "dto-pass-123")

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		token  string
	}{
		{"viewerProvider", http.MethodPost, base + "/ai/providers", map[string]any{"name": "x", "kind": "cv", "apiKey": "leak"}, viewer},
		{"viewerNotify", http.MethodPost, base + "/notify/channels", map[string]any{"name": "x", "type": "webhook", "secret": "leak"}, viewer},
		{"operatorNotify", http.MethodPost, base + "/notify/channels", map[string]any{"name": "x", "type": "webhook", "secret": "leak"}, operator},
		{"viewerStorage", http.MethodPost, base + "/storages", map[string]any{"name": "x", "endpoint": "http://x", "bucket": "b", "accessKey": "leak"}, viewer},
		{"operatorStorage", http.MethodPost, base + "/storages", map[string]any{"name": "x", "endpoint": "http://x", "bucket": "b", "accessKey": "leak"}, operator},
		{"viewerGBCascade", http.MethodPost, base + "/gb/cascades", map[string]any{"targetId": "x", "targetIp": "10.0.0.1", "password": "leak"}, viewer},
		{"viewerGBWhite", http.MethodPost, base + "/gb/whitelist", map[string]any{"deviceId": "x", "password": "leak"}, viewer},
		{"viewerGACascade", http.MethodPost, base + "/ga1400/cascades", map[string]any{"url": "http://x", "password": "leak"}, viewer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r apiResp
			code := doJSON(t, tc.method, tc.path, tc.token, tc.body, &r)
			if code != http.StatusForbidden || r.Code != http.StatusForbidden {
				t.Fatalf("status=%d code=%d, want 403: %+v", code, r.Code, r)
			}
			requireNoSecret(t, []byte(r.Message), "leak")
		})
	}
}
