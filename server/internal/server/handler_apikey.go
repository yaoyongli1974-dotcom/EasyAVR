package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/auth"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/openapi"
)

// listAPIKeys returns open API credentials (secrets are never serialized).
func (a *App) listAPIKeys(c *gin.Context) {
	var items []model.APIKey
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

type apiKeyRequest struct {
	Name        string `json:"name"`
	Scopes      string `json:"scopes"`
	Enabled     *bool  `json:"enabled"`
	ExpiresAt   string `json:"expiresAt"`
	RateLimit   *int   `json:"rateLimit"`
	QuotaPerDay *int   `json:"quotaPerDay"`
}

// createAPIKey issues a new credential and returns the plaintext key once.
func (a *App) createAPIKey(c *gin.Context) {
	var req apiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if strings.TrimSpace(req.Scopes) == "" {
		req.Scopes = "read"
	}
	plain, prefix, hash, err := auth.GenerateAPIKey()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	rec := model.APIKey{
		Name: req.Name, Prefix: prefix, KeyHash: hash, Scopes: req.Scopes, Enabled: true,
	}
	if req.Enabled != nil {
		rec.Enabled = *req.Enabled
	}
	if req.RateLimit != nil {
		rec.RateLimit = *req.RateLimit
	}
	if req.QuotaPerDay != nil {
		rec.QuotaPerDay = *req.QuotaPerDay
	}
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			fail(c, http.StatusBadRequest, "expiresAt must be RFC3339")
			return
		}
		rec.ExpiresAt = &t
	}
	if err := a.db.Create(&rec).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"key": rec, "secret": plain})
}

// updateAPIKey toggles a credential or changes its scopes.
func (a *App) updateAPIKey(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var rec model.APIKey
	if err := a.db.First(&rec, id).Error; err != nil {
		fail(c, http.StatusNotFound, "api key not found")
		return
	}
	var req apiKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if strings.TrimSpace(req.Scopes) != "" {
		updates["scopes"] = req.Scopes
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.RateLimit != nil {
		updates["rate_limit"] = *req.RateLimit
	}
	if req.QuotaPerDay != nil {
		updates["quota_per_day"] = *req.QuotaPerDay
	}
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			fail(c, http.StatusBadRequest, "expiresAt must be RFC3339")
			return
		}
		updates["expires_at"] = t
	}
	a.db.Model(&rec).Updates(updates)
	a.db.First(&rec, id)
	ok(c, rec)
}

func (a *App) deleteAPIKey(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.db.Delete(&model.APIKey{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.limiter.Reset(id)
	ok(c, gin.H{"id": id})
}

// apiKeyLogs returns recent audited requests, globally or for one key.
func (a *App) apiKeyLogs(c *gin.Context) {
	limit := 100
	if v := c.Query("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	tx := a.db.Order("id DESC")
	if idStr := c.Param("id"); idStr != "" {
		id, valid := parseUintParam(c, "id")
		if !valid {
			return
		}
		tx = tx.Where("key_id = ?", id)
	}
	var items []model.APIRequestLog
	tx.Limit(limit).Find(&items)
	ok(c, items)
}

type keyUsage struct {
	KeyID   uint   `json:"keyId"`
	KeyName string `json:"keyName"`
	Count   int64  `json:"count"`
}

// apiKeyStats aggregates open API usage.
func (a *App) apiKeyStats(c *gin.Context) {
	var total, enabled int64
	a.db.Model(&model.APIKey{}).Count(&total)
	a.db.Model(&model.APIKey{}).Where("enabled = ?", true).Count(&enabled)

	var requestsTotal int64
	a.db.Model(&model.APIRequestLog{}).Count(&requestsTotal)

	startOfDay := time.Now().Truncate(24 * time.Hour)
	var requestsToday int64
	a.db.Model(&model.APIRequestLog{}).Where("created_at >= ?", startOfDay).Count(&requestsToday)

	var top []keyUsage
	a.db.Model(&model.APIRequestLog{}).
		Select("key_id, key_name, count(*) as count").
		Group("key_id, key_name").Order("count DESC").Limit(10).Scan(&top)

	ok(c, gin.H{
		"keys": total, "enabled": enabled,
		"requestsToday": requestsToday, "requestsTotal": requestsTotal,
		"topKeys": top,
	})
}

// openapiSpec serves the OpenAPI document for the open API.
func (a *App) openapiSpec(c *gin.Context) {
	spec, err := openapi.Spec()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", spec)
}
