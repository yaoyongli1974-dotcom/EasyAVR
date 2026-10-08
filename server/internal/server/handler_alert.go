package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- alert policies (AI event tiered distribution) ----

type alertPolicyReq struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Enabled     bool                     `json:"enabled"`
	Priority    int                      `json:"priority"`
	Kind        string                   `json:"kind"`
	EventType   string                   `json:"eventType"`
	MinLevel    string                   `json:"minLevel"`
	ChannelID   uint                     `json:"channelId"`
	Keywords    string                   `json:"keywords"`
	CooldownSec int                      `json:"cooldownSec"`
	AckRequired bool                     `json:"ackRequired"`
	Tiers       *[]model.AlertPolicyTier `json:"tiers"`
}

func (a *App) listAlertPolicies(c *gin.Context) {
	var items []model.AlertPolicy
	a.db.Order("priority DESC, id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createAlertPolicy(c *gin.Context) {
	var req alertPolicyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if req.MinLevel == "" {
		req.MinLevel = "info"
	}
	p := model.AlertPolicy{
		Name: req.Name, Description: req.Description, Enabled: req.Enabled, Priority: req.Priority,
		Kind: req.Kind, EventType: req.EventType, MinLevel: req.MinLevel, ChannelID: req.ChannelID,
		Keywords: req.Keywords, CooldownSec: req.CooldownSec, AckRequired: req.AckRequired,
	}
	if err := a.db.Create(&p).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Tiers != nil {
		for i := range *req.Tiers {
			(*req.Tiers)[i].ID = 0
			(*req.Tiers)[i].PolicyID = p.ID
			a.db.Create(&(*req.Tiers)[i])
		}
	}
	a.loadAlertTiers(&p)
	ok(c, p)
}

func (a *App) getAlertPolicy(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var p model.AlertPolicy
	if err := a.db.First(&p, id).Error; err != nil {
		fail(c, http.StatusNotFound, "policy not found")
		return
	}
	a.loadAlertTiers(&p)
	ok(c, p)
}

func (a *App) updateAlertPolicy(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var p model.AlertPolicy
	if err := a.db.First(&p, id).Error; err != nil {
		fail(c, http.StatusNotFound, "policy not found")
		return
	}
	var req alertPolicyReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{
		"description": req.Description, "enabled": req.Enabled, "priority": req.Priority,
		"kind": req.Kind, "event_type": req.EventType, "min_level": req.MinLevel,
		"channel_id": req.ChannelID, "keywords": req.Keywords,
		"cooldown_sec": req.CooldownSec, "ack_required": req.AckRequired,
	}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	a.db.Model(&p).Updates(updates)
	if req.Tiers != nil {
		a.db.Where("policy_id = ?", p.ID).Delete(&model.AlertPolicyTier{})
		for i := range *req.Tiers {
			(*req.Tiers)[i].ID = 0
			(*req.Tiers)[i].PolicyID = p.ID
			a.db.Create(&(*req.Tiers)[i])
		}
	}
	a.db.First(&p, id)
	a.loadAlertTiers(&p)
	ok(c, p)
}

func (a *App) deleteAlertPolicy(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Where("policy_id = ?", id).Delete(&model.AlertPolicyTier{})
	a.db.Where("policy_id = ?", id).Delete(&model.AlertDelivery{})
	a.db.Delete(&model.AlertPolicy{}, id)
	ok(c, gin.H{"id": id})
}

func (a *App) loadAlertTiers(p *model.AlertPolicy) {
	var tiers []model.AlertPolicyTier
	a.db.Where("policy_id = ?", p.ID).Order("tier ASC").Find(&tiers)
	p.Tiers = tiers
}

func (a *App) testAlertPolicy(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	n, err := a.notify.TestPolicy(id)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "sent": n})
}

// ---- policy tiers ----

func (a *App) listAlertTiers(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var tiers []model.AlertPolicyTier
	a.db.Where("policy_id = ?", id).Order("tier ASC").Find(&tiers)
	ok(c, tiers)
}

func (a *App) createAlertTier(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var p model.AlertPolicy
	if err := a.db.First(&p, id).Error; err != nil {
		fail(c, http.StatusNotFound, "policy not found")
		return
	}
	var t model.AlertPolicyTier
	if err := c.ShouldBindJSON(&t); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	t.ID = 0
	t.PolicyID = p.ID
	if err := a.db.Create(&t).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, t)
}

func (a *App) updateAlertTier(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	tid, valid := parseUintParam(c, "tierId")
	if !valid {
		return
	}
	var t model.AlertPolicyTier
	if err := a.db.Where("id = ? AND policy_id = ?", tid, id).First(&t).Error; err != nil {
		fail(c, http.StatusNotFound, "tier not found")
		return
	}
	var req model.AlertPolicyTier
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	a.db.Model(&t).Updates(map[string]any{
		"tier": req.Tier, "min_level": req.MinLevel, "delay_sec": req.DelaySec,
		"target_ids": req.TargetIDs, "template": req.Template,
	})
	a.db.First(&t, t.ID)
	ok(c, t)
}

func (a *App) deleteAlertTier(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	tid, valid := parseUintParam(c, "tierId")
	if !valid {
		return
	}
	a.db.Where("id = ? AND policy_id = ?", tid, id).Delete(&model.AlertPolicyTier{})
	ok(c, gin.H{"id": tid})
}

// ---- deliveries & stats ----

func (a *App) listAlertDeliveries(c *gin.Context) {
	page, pageSize := pagination(c)
	tx := a.db.Model(&model.AlertDelivery{})
	if v := c.Query("policyId"); v != "" {
		tx = tx.Where("policy_id = ?", v)
	}
	if v := c.Query("eventId"); v != "" {
		tx = tx.Where("event_id = ?", v)
	}
	if v := c.Query("status"); v != "" {
		tx = tx.Where("status = ?", v)
	}
	var total int64
	tx.Count(&total)
	var items []model.AlertDelivery
	tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items)
	ok(c, gin.H{"items": items, "total": total, "page": page, "pageSize": pageSize})
}

func (a *App) alertStats(c *gin.Context) {
	var policies, enabled, deliveries, failed int64
	a.db.Model(&model.AlertPolicy{}).Count(&policies)
	a.db.Model(&model.AlertPolicy{}).Where("enabled = ?", true).Count(&enabled)
	a.db.Model(&model.AlertDelivery{}).Count(&deliveries)
	a.db.Model(&model.AlertDelivery{}).Where("status = ?", "failed").Count(&failed)
	since := time.Now().Add(-24 * time.Hour)
	var last24 int64
	a.db.Model(&model.AlertDelivery{}).Where("created_at >= ?", since).Count(&last24)
	ok(c, gin.H{
		"policies": policies, "enabled": enabled,
		"deliveries": deliveries, "failed": failed, "last24h": last24,
	})
}

// ---- event acknowledgement ----

func (a *App) ackEvent(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ev model.AIEvent
	if err := a.db.First(&ev, id).Error; err != nil {
		fail(c, http.StatusNotFound, "event not found")
		return
	}
	now := time.Now()
	by := ""
	if claims := currentClaims(c); claims != nil {
		by = claims.Username
	}
	a.db.Model(&ev).Updates(map[string]any{"acked": true, "acked_at": &now, "acked_by": by})
	ok(c, gin.H{"id": ev.ID, "acked": true, "ackedBy": by})
}
