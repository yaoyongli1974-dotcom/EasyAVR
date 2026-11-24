package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

// newRunsTestServer wires only the task-run handlers under JWT auth. It does
// not depend on router.go so the handler contract is tested independently of
// route registration.
func newRunsTestServer(t *testing.T) (*httptest.Server, *App) {
	t.Helper()
	cfg := &config.Config{
		Listen: ":0", DBPath: filepath.Join(t.TempDir(), "runs.db"),
		JWTSecret: "test-secret", JWTExpireH: 1,
		AdminUser: "easyavr", AdminPass: "easyavr",
	}
	cfg.ZLM.APIBase = "http://127.0.0.1:1"
	cfg.ZLM.Secret = "test"
	cfg.GB35114.CertDir = filepath.Join(t.TempDir(), "certs")
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := store.Seed(db, cfg.AdminUser, cfg.AdminPass); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := New(cfg, db)

	r := gin.New()
	api := r.Group("/api/v1")
	api.POST("/auth/login", a.login)
	authed := api.Group("")
	authed.Use(a.authRequired())
	authed.GET("/ai/tasks/:id/runs", a.listTaskRuns)
	authed.GET("/ai/tasks/:id/runs/:runId", a.getTaskRun)
	authed.POST("/ai/tasks/:id/recover", a.recoverTask)
	authed.POST("/ai/tasks/:id/run", a.runTaskOnce)

	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		a.runner.Close()
	})
	return srv, a
}

func seedRunsTask(t *testing.T, a *App, status, configJSON string) (taskID, channelID, providerID uint) {
	t.Helper()
	dev := model.Device{Name: "runs-device", Protocol: "rtsp"}
	if err := a.db.Create(&dev).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	ch := model.Channel{
		DeviceID: dev.ID, Name: "cam", SourceURL: "rtsp://test/source",
		StreamKey: fmt.Sprintf("runs-test-%d", time.Now().UnixNano()),
	}
	if err := a.db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	p := model.AIProvider{Name: "cv", Kind: "cv", Endpoint: "http://127.0.0.1:1/detect", Enabled: true}
	if err := a.db.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	task := model.AITask{
		Name: "task", ChannelID: ch.ID, ProviderID: p.ID, TaskType: "cv_detect",
		Config: configJSON, Enabled: true, Status: status,
	}
	if err := a.db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task.ID, ch.ID, p.ID
}

func TestAITaskRunsAPI(t *testing.T) {
	srv, a := newRunsTestServer(t)
	token := login(t, srv.URL)
	taskID, _, _ := seedRunsTask(t, a, "stopped", `{"intervalSec":0}`)
	run := model.AITaskRun{
		TaskID: taskID, Status: "failed", ErrorCode: "provider_timeout",
		ErrorMsg: "provider request timed out", RetryCount: 2, WorkerID: "worker-1",
	}
	if err := a.db.Create(&run).Error; err != nil {
		t.Fatalf("create run: %v", err)
	}

	var list apiResp
	if status := doJSON(t, http.MethodGet, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/runs", token, nil, &list); status != 200 || list.Code != 0 {
		t.Fatalf("list runs: status=%d resp=%+v", status, list)
	}
	var page struct {
		Items    []model.AITaskRun `json:"items"`
		Total    int64             `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"pageSize"`
	}
	json.Unmarshal(list.Data, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != run.ID || page.Page != 1 || page.PageSize != 20 {
		t.Fatalf("unexpected page: %+v", page)
	}

	var filtered apiResp
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/runs?status=succeeded", token, nil, &filtered)
	json.Unmarshal(filtered.Data, &page)
	if page.Total != 0 {
		t.Fatalf("status filter ignored: %+v", page)
	}

	var one apiResp
	if status := doJSON(t, http.MethodGet, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/runs/"+uintStr(run.ID), token, nil, &one); status != 200 || one.Code != 0 {
		t.Fatalf("get run: status=%d resp=%+v", status, one)
	}
	var got model.AITaskRun
	json.Unmarshal(one.Data, &got)
	if got.ID != run.ID || got.ErrorCode != "provider_timeout" || got.RetryCount != 2 {
		t.Fatalf("unexpected run: %+v", got)
	}

	var missing apiResp
	if status := doJSON(t, http.MethodGet, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/runs/99999", token, nil, &missing); status != 404 || missing.Code == 0 {
		t.Fatalf("missing run: status=%d resp=%+v", status, missing)
	}
	if status := doJSON(t, http.MethodGet, srv.URL+"/api/v1/ai/tasks/99999/runs", token, nil, &missing); status != 404 || missing.Code == 0 {
		t.Fatalf("missing task: status=%d resp=%+v", status, missing)
	}
}

func TestAITaskRecoverAPI(t *testing.T) {
	srv, a := newRunsTestServer(t)
	token := login(t, srv.URL)

	manualID, _, _ := seedRunsTask(t, a, "running", `{"intervalSec":0}`)
	lingering := model.AITaskRun{TaskID: manualID, Status: "running", WorkerID: "old"}
	a.db.Create(&lingering)

	var manualResp apiResp
	if status := doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks/"+uintStr(manualID)+"/recover", token, nil, &manualResp); status != 200 || manualResp.Code != 0 {
		t.Fatalf("recover manual: status=%d resp=%+v", status, manualResp)
	}
	var manual struct {
		Status      string `json:"status"`
		AutoRecover bool   `json:"autoRecover"`
	}
	json.Unmarshal(manualResp.Data, &manual)
	if manual.Status != "stopped" || manual.AutoRecover {
		t.Fatalf("manual recover result: %+v", manual)
	}
	var closed model.AITaskRun
	a.db.First(&closed, lingering.ID)
	if closed.Status != "failed" || closed.ErrorCode != "internal_error" || closed.FinishedAt == nil {
		t.Fatalf("lingering run not closed: %+v", closed)
	}

	// autoRecover=true restarts the task; provider 0 makes the first attempt
	// fail fast without any network access.
	autoID, _, _ := seedRunsTask(t, a, "running", `{"intervalSec":0,"autoRecover":true}`)
	a.db.Model(&model.AITask{}).Where("id = ?", autoID).Update("provider_id", 0)
	var autoResp apiResp
	if status := doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks/"+uintStr(autoID)+"/recover", token, nil, &autoResp); status != 200 || autoResp.Code != 0 {
		t.Fatalf("recover auto: status=%d resp=%+v", status, autoResp)
	}
	var auto struct {
		Status      string `json:"status"`
		AutoRecover bool   `json:"autoRecover"`
	}
	json.Unmarshal(autoResp.Data, &auto)
	if auto.Status != "running" || !auto.AutoRecover {
		t.Fatalf("auto recover result: %+v", auto)
	}

	var missing apiResp
	if status := doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks/99999/recover", token, nil, &missing); status != 404 || missing.Code == 0 {
		t.Fatalf("recover missing: status=%d resp=%+v", status, missing)
	}
}

// The existing /run response shape must not change: {events, count}.
func TestOldRunOnceResponseUnchanged(t *testing.T) {
	srv, a := newRunsTestServer(t)
	token := login(t, srv.URL)

	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"eventType":"person","level":"warning","confidence":0.9,"summary":"found","payload":{"n":1}}]`))
	}))
	defer worker.Close()

	taskID, channelID, providerID := seedRunsTask(t, a, "stopped", `{"intervalSec":0,"grabFrame":false}`)
	a.db.Model(&model.AIProvider{}).Where("id = ?", providerID).Update("endpoint", worker.URL)
	a.db.Model(&model.Channel{}).Where("id = ?", channelID).Update("source_url", "rtsp://test/source")

	var resp apiResp
	status := doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/run", token, nil, &resp)
	if status != 200 || resp.Code != 0 {
		t.Fatalf("run once: status=%d resp=%+v", status, resp)
	}
	var data struct {
		Events []model.AIEvent `json:"events"`
		Count  int             `json:"count"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.Count != 1 || len(data.Events) != 1 || data.Events[0].EventType != "person" {
		t.Fatalf("unexpected /run response: %+v", data)
	}
	// Provider outages keep the historical 502 + {code,message} contract.
	a.db.Model(&model.AIProvider{}).Where("id = ?", providerID).Update("endpoint", "http://127.0.0.1:1/detect")
	var failed apiResp
	if status := doJSON(t, http.MethodPost, srv.URL+"/api/v1/ai/tasks/"+uintStr(taskID)+"/run", token, nil, &failed); status != http.StatusBadGateway || failed.Code == 0 {
		t.Fatalf("failed run once: status=%d resp=%+v", status, failed)
	}
}

func uintStr(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
