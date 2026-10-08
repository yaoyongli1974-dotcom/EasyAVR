package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/event"
	"github.com/easyavr/easyavr/internal/model"
)

// ---- AI providers ----

func (a *App) listProviders(c *gin.Context) {
	var providers []model.AIProvider
	a.db.Order("id DESC").Find(&providers)
	ok(c, providers)
}

func (a *App) createProvider(c *gin.Context) {
	var p model.AIProvider
	if err := c.ShouldBindJSON(&p); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if p.Name == "" || p.Kind == "" {
		fail(c, http.StatusBadRequest, "name and kind are required")
		return
	}
	if err := a.db.Create(&p).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, p)
}

func (a *App) updateProvider(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var p model.AIProvider
	if err := a.db.First(&p, id).Error; err != nil {
		fail(c, http.StatusNotFound, "provider not found")
		return
	}
	var req model.AIProvider
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Kind != "" {
		updates["kind"] = req.Kind
	}
	if req.Vendor != "" {
		updates["vendor"] = req.Vendor
	}
	if req.Endpoint != "" {
		updates["endpoint"] = req.Endpoint
	}
	if req.APIKey != "" {
		updates["api_key"] = req.APIKey
	}
	if req.Model != "" {
		updates["model"] = req.Model
	}
	updates["enabled"] = req.Enabled
	a.db.Model(&p).Updates(updates)
	a.db.First(&p, id)
	ok(c, p)
}

func (a *App) deleteProvider(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.db.Delete(&model.AIProvider{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

// ---- AI tasks ----

func (a *App) listTasks(c *gin.Context) {
	var tasks []model.AITask
	tx := a.db.Order("id DESC")
	if chID := c.Query("channelId"); chID != "" {
		tx = tx.Where("channel_id = ?", chID)
	}
	tx.Find(&tasks)
	ok(c, tasks)
}

func (a *App) createTask(c *gin.Context) {
	var t model.AITask
	if err := c.ShouldBindJSON(&t); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if t.Name == "" || t.ChannelID == 0 || t.ProviderID == 0 || t.TaskType == "" {
		fail(c, http.StatusBadRequest, "name, channelId, providerId and taskType are required")
		return
	}
	if t.Status == "" {
		t.Status = "stopped"
	}
	if err := a.db.Create(&t).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, t)
}

func (a *App) updateTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var t model.AITask
	if err := a.db.First(&t, id).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	var req model.AITask
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.ChannelID != 0 {
		updates["channel_id"] = req.ChannelID
	}
	if req.ProviderID != 0 {
		updates["provider_id"] = req.ProviderID
	}
	if req.TaskType != "" {
		updates["task_type"] = req.TaskType
	}
	if req.Config != "" {
		updates["config"] = req.Config
	}
	updates["enabled"] = req.Enabled
	a.db.Model(&t).Updates(updates)
	a.db.First(&t, id)
	ok(c, t)
}

func (a *App) deleteTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.runner.Stop(id)
	if err := a.db.Delete(&model.AITask{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (a *App) startTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.runner.Start(id); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "status": "running"})
}

func (a *App) stopTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.runner.Stop(id)
	ok(c, gin.H{"id": id, "status": "stopped"})
}

// runTaskOnce executes a task synchronously, useful for testing providers.
func (a *App) runTaskOnce(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var t model.AITask
	if err := a.db.First(&t, id).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	events, err := a.runner.RunOnce(c.Request.Context(), t)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"events": events, "count": len(events)})
}

// ---- AI event center ----

func (a *App) listEvents(c *gin.Context) {
	q := event.Query{
		Kind:      c.Query("kind"),
		Level:     c.Query("level"),
		EventType: c.Query("eventType"),
		Keyword:   c.Query("keyword"),
	}
	if v := c.Query("channelId"); v != "" {
		q.ChannelID = parseChannelID(v)
	}
	q.From = parseTime(c.Query("from"))
	q.To = parseTime(c.Query("to"))
	q.Page, q.PageSize = pagination(c)
	items, total, err := a.events.List(q)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total, "page": q.Page, "pageSize": q.PageSize})
}

func (a *App) eventStats(c *gin.Context) {
	st, err := a.events.Stats()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, st)
}

// ingestEvent lets external AI workers push events into the event center.
func (a *App) ingestEvent(c *gin.Context) {
	var e model.AIEvent
	if err := c.ShouldBindJSON(&e); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}
	if e.Level == "" {
		e.Level = "info"
	}
	if err := a.db.Create(&e).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if a.sink != nil {
		a.sink.OnEvent(e)
	}
	ok(c, e)
}

func parseChannelID(s string) uint {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return uint(n)
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
