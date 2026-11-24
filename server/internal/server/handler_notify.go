package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- notification channels ----

// notifyChannelReq is the write payload for a channel. Secrets are write-only:
// empty values keep the stored ones; the clear flags remove them.
type notifyChannelReq struct {
	Name              string `json:"name"`
	Type              string `json:"type"`
	Enabled           bool   `json:"enabled"`
	URL               string `json:"url"`
	Secret            string `json:"secret"`
	ClearSecret       bool   `json:"clearSecret"`
	SMTPHost          string `json:"smtpHost"`
	SMTPPort          int    `json:"smtpPort"`
	SMTPUser          string `json:"smtpUser"`
	SMTPPassword      string `json:"smtpPassword"`
	ClearSMTPPassword bool   `json:"clearSmtpPassword"`
	From              string `json:"from"`
	To                string `json:"to"`
	UseTLS            bool   `json:"useTls"`
}

type notifyChannelView struct {
	model.NotificationChannel
	SecretConfigured       bool `json:"secretConfigured"`
	SMTPPasswordConfigured bool `json:"smtpPasswordConfigured"`
}

func channelView(ch model.NotificationChannel) notifyChannelView {
	return notifyChannelView{
		NotificationChannel:    ch,
		SecretConfigured:       ch.Secret != "",
		SMTPPasswordConfigured: ch.SMTPPassword != "",
	}
}

func (a *App) listNotifyChannels(c *gin.Context) {
	var items []model.NotificationChannel
	a.db.Order("id DESC").Find(&items)
	views := make([]notifyChannelView, 0, len(items))
	for _, ch := range items {
		views = append(views, channelView(ch))
	}
	ok(c, views)
}

func (a *App) createNotifyChannel(c *gin.Context) {
	var req notifyChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" || req.Type == "" {
		fail(c, http.StatusBadRequest, "name and type are required")
		return
	}
	ch := model.NotificationChannel{
		Name: req.Name, Type: req.Type, Enabled: req.Enabled, URL: req.URL,
		Secret: req.Secret, SMTPHost: req.SMTPHost, SMTPPort: req.SMTPPort,
		SMTPUser: req.SMTPUser, SMTPPassword: req.SMTPPassword,
		From: req.From, To: req.To, UseTLS: req.UseTLS,
	}
	if err := a.db.Create(&ch).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, channelView(ch))
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
	var req notifyChannelReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	ch.Name = req.Name
	ch.Type = req.Type
	ch.Enabled = req.Enabled
	ch.URL = req.URL
	ch.SMTPHost = req.SMTPHost
	ch.SMTPPort = req.SMTPPort
	ch.SMTPUser = req.SMTPUser
	ch.From = req.From
	ch.To = req.To
	ch.UseTLS = req.UseTLS
	switch {
	case req.Secret != "":
		ch.Secret = req.Secret
	case req.ClearSecret:
		ch.Secret = ""
	}
	switch {
	case req.SMTPPassword != "":
		ch.SMTPPassword = req.SMTPPassword
	case req.ClearSMTPPassword:
		ch.SMTPPassword = ""
	}
	if err := a.db.Save(&ch).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var fresh model.NotificationChannel
	if err := a.db.First(&fresh, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	ok(c, channelView(fresh))
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
