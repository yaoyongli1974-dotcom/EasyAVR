package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- notification channels ----

func (a *App) listNotifyChannels(c *gin.Context) {
	var items []model.NotificationChannel
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createNotifyChannel(c *gin.Context) {
	var ch model.NotificationChannel
	if err := c.ShouldBindJSON(&ch); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if ch.Name == "" || ch.Type == "" {
		fail(c, http.StatusBadRequest, "name and type are required")
		return
	}
	if err := a.db.Create(&ch).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, ch)
}

func (a *App) updateNotifyChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ch model.NotificationChannel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	var req model.NotificationChannel
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{
		"name": req.Name, "type": req.Type, "enabled": req.Enabled, "url": req.URL,
		"smtp_host": req.SMTPHost, "smtp_port": req.SMTPPort, "smtp_user": req.SMTPUser,
		"from": req.From, "to": req.To, "use_tls": req.UseTLS,
	}
	if req.Secret != "" {
		updates["secret"] = req.Secret
	}
	if req.SMTPPassword != "" {
		updates["smtp_password"] = req.SMTPPassword
	}
	a.db.Model(&ch).Updates(updates)
	a.db.First(&ch, id)
	ok(c, ch)
}

func (a *App) deleteNotifyChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.NotificationChannel{}, id)
	ok(c, gin.H{"id": id})
}

func (a *App) testNotifyChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.notify.Test(id); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "sent": true})
}

// ---- notification rules ----

func (a *App) listNotifyRules(c *gin.Context) {
	var items []model.NotificationRule
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createNotifyRule(c *gin.Context) {
	var r model.NotificationRule
	if err := c.ShouldBindJSON(&r); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if r.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if err := a.db.Create(&r).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, r)
}

func (a *App) updateNotifyRule(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var r model.NotificationRule
	if err := a.db.First(&r, id).Error; err != nil {
		fail(c, http.StatusNotFound, "rule not found")
		return
	}
	var req model.NotificationRule
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	a.db.Model(&r).Updates(map[string]any{
		"name": req.Name, "enabled": req.Enabled, "target_ids": req.TargetIDs,
		"min_level": req.MinLevel, "kind": req.Kind, "event_type": req.EventType,
	})
	a.db.First(&r, id)
	ok(c, r)
}

func (a *App) deleteNotifyRule(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.NotificationRule{}, id)
	ok(c, gin.H{"id": id})
}
