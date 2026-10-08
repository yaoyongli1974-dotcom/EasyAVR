package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- AI model registry ----

type modelWithCounts struct {
	model.AIModel
	VersionCount int    `json:"versionCount"`
	LatestVer    string `json:"latestVersion"`
}

func (a *App) listModels(c *gin.Context) {
	tx := a.db.Order("id DESC")
	if kind := c.Query("kind"); kind != "" {
		tx = tx.Where("kind = ?", kind)
	}
	if task := c.Query("task"); task != "" {
		tx = tx.Where("task = ?", task)
	}
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		tx = tx.Where("name LIKE ? OR description LIKE ? OR tags LIKE ?", like, like, like)
	}
	var models []model.AIModel
	tx.Find(&models)

	out := make([]modelWithCounts, 0, len(models))
	for _, m := range models {
		var count int64
		a.db.Model(&model.AIModelVersion{}).Where("model_id = ?", m.ID).Count(&count)
		var latest model.AIModelVersion
		a.db.Where("model_id = ?", m.ID).Order("id DESC").First(&latest)
		out = append(out, modelWithCounts{AIModel: m, VersionCount: int(count), LatestVer: latest.Version})
	}
	ok(c, out)
}

func (a *App) createModel(c *gin.Context) {
	var m model.AIModel
	if err := c.ShouldBindJSON(&m); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || m.Kind == "" {
		fail(c, http.StatusBadRequest, "name and kind are required")
		return
	}
	if m.Source == "" {
		m.Source = "local"
	}
	if err := a.db.Create(&m).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, m)
}

func (a *App) getModel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var m model.AIModel
	if err := a.db.First(&m, id).Error; err != nil {
		fail(c, http.StatusNotFound, "model not found")
		return
	}
	var versions []model.AIModelVersion
	a.db.Where("model_id = ?", m.ID).Order("id DESC").Find(&versions)
	var deployments []model.AIModelDeployment
	a.db.Where("model_id = ?", m.ID).Order("id DESC").Find(&deployments)
	m.Versions = versions
	ok(c, gin.H{"model": m, "versions": versions, "deployments": deployments})
}

func (a *App) updateModel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var m model.AIModel
	if err := a.db.First(&m, id).Error; err != nil {
		fail(c, http.StatusNotFound, "model not found")
		return
	}
	var req model.AIModel
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{"enabled": req.Enabled}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Kind != "" {
		updates["kind"] = req.Kind
	}
	if req.Task != "" {
		updates["task"] = req.Task
	}
	if req.Framework != "" {
		updates["framework"] = req.Framework
	}
	if req.Source != "" {
		updates["source"] = req.Source
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Tags != "" {
		updates["tags"] = req.Tags
	}
	a.db.Model(&m).Updates(updates)
	a.db.First(&m, id)
	ok(c, m)
}

func (a *App) deleteModel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("model_id = ?", id).Delete(&model.AIModelDeployment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("model_id = ?", id).Delete(&model.AIModelVersion{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.AIModel{}, id).Error
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

// ---- model versions ----

func (a *App) listModelVersions(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var versions []model.AIModelVersion
	a.db.Where("model_id = ?", id).Order("id DESC").Find(&versions)
	ok(c, versions)
}

func (a *App) createModelVersion(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var m model.AIModel
	if err := a.db.First(&m, id).Error; err != nil {
		fail(c, http.StatusNotFound, "model not found")
		return
	}
	var v model.AIModelVersion
	if err := c.ShouldBindJSON(&v); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	v.Version = strings.TrimSpace(v.Version)
	if v.Version == "" {
		fail(c, http.StatusBadRequest, "version is required")
		return
	}
	v.ModelID = m.ID
	if v.Status == "" {
		v.Status = "registered"
	}
	var existing int64
	a.db.Model(&model.AIModelVersion{}).Where("model_id = ? AND version = ?", m.ID, v.Version).Count(&existing)
	if existing > 0 {
		fail(c, http.StatusConflict, "version already exists")
		return
	}
	if err := a.db.Create(&v).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, v)
}

func (a *App) getModelVersionByPath(c *gin.Context, modelID uint) (model.AIModelVersion, bool) {
	vid, valid := parseUintParam(c, "versionId")
	if !valid {
		return model.AIModelVersion{}, false
	}
	var v model.AIModelVersion
	if err := a.db.Where("id = ? AND model_id = ?", vid, modelID).First(&v).Error; err != nil {
		fail(c, http.StatusNotFound, "version not found")
		return model.AIModelVersion{}, false
	}
	return v, true
}

func (a *App) updateModelVersion(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	v, ok2 := a.getModelVersionByPath(c, id)
	if !ok2 {
		return
	}
	var req model.AIModelVersion
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Format != "" {
		updates["format"] = req.Format
	}
	if req.SizeBytes != 0 {
		updates["size_bytes"] = req.SizeBytes
	}
	if req.Checksum != "" {
		updates["checksum"] = req.Checksum
	}
	if req.Path != "" {
		updates["path"] = req.Path
	}
	if req.URL != "" {
		updates["url"] = req.URL
	}
	if req.Metrics != "" {
		updates["metrics"] = req.Metrics
	}
	if req.Labels != "" {
		updates["labels"] = req.Labels
	}
	if req.Params != "" {
		updates["params"] = req.Params
	}
	if req.Notes != "" {
		updates["notes"] = req.Notes
	}
	a.db.Model(&v).Updates(updates)
	a.db.First(&v, v.ID)
	ok(c, v)
}

func (a *App) archiveModelVersion(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	v, ok2 := a.getModelVersionByPath(c, id)
	if !ok2 {
		return
	}
	a.db.Model(&v).Update("status", "archived")
	ok(c, gin.H{"id": v.ID, "status": "archived"})
}

func (a *App) deleteModelVersion(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	v, ok2 := a.getModelVersionByPath(c, id)
	if !ok2 {
		return
	}
	err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("version_id = ?", v.ID).Delete(&model.AIModelDeployment{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.AIModelVersion{}, v.ID).Error
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": v.ID})
}

// ---- model deployments ----

type deploymentView struct {
	model.AIModelDeployment
	ModelName    string `json:"modelName"`
	Version      string `json:"version"`
	ProviderName string `json:"providerName"`
}

func (a *App) listDeployments(c *gin.Context) {
	tx := a.db.Order("id DESC")
	if mid := c.Query("modelId"); mid != "" {
		tx = tx.Where("model_id = ?", mid)
	}
	if pid := c.Query("providerId"); pid != "" {
		tx = tx.Where("provider_id = ?", pid)
	}
	if st := c.Query("status"); st != "" {
		tx = tx.Where("status = ?", st)
	}
	var ds []model.AIModelDeployment
	tx.Find(&ds)

	out := make([]deploymentView, 0, len(ds))
	for _, d := range ds {
		v := deploymentView{AIModelDeployment: d}
		var m model.AIModel
		if a.db.First(&m, d.ModelID).Error == nil {
			v.ModelName = m.Name
		}
		var mv model.AIModelVersion
		if a.db.First(&mv, d.VersionID).Error == nil {
			v.Version = mv.Version
		}
		var p model.AIProvider
		if a.db.First(&p, d.ProviderID).Error == nil {
			v.ProviderName = p.Name
		}
		out = append(out, v)
	}
	ok(c, out)
}

func (a *App) createDeployment(c *gin.Context) {
	var d model.AIModelDeployment
	if err := c.ShouldBindJSON(&d); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if d.ModelID == 0 || d.VersionID == 0 || d.ProviderID == 0 {
		fail(c, http.StatusBadRequest, "modelId, versionId and providerId are required")
		return
	}
	var mv model.AIModelVersion
	if err := a.db.Where("id = ? AND model_id = ?", d.VersionID, d.ModelID).First(&mv).Error; err != nil {
		fail(c, http.StatusBadRequest, "version does not belong to model")
		return
	}
	var p model.AIProvider
	if err := a.db.First(&p, d.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, "provider not found")
		return
	}
	if d.Name == "" {
		d.Name = p.Name + "-" + mv.Version
	}
	if d.Replicas == 0 {
		d.Replicas = 1
	}
	if d.Status == "" {
		d.Status = "active"
	}
	if d.Health == "" {
		d.Health = "healthy"
	}
	now := time.Now()
	d.DeployedAt = &now
	d.LastHealthAt = &now
	if err := a.db.Create(&d).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if d.Status == "active" {
		a.db.Model(&model.AIModelVersion{}).Where("id = ?", mv.ID).Update("status", "deployed")
	}
	ok(c, d)
}

func (a *App) updateDeployment(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var d model.AIModelDeployment
	if err := a.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "deployment not found")
		return
	}
	var req model.AIModelDeployment
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.ProviderID != 0 {
		updates["provider_id"] = req.ProviderID
	}
	if req.Replicas != 0 {
		updates["replicas"] = req.Replicas
	}
	if req.Config != "" {
		updates["config"] = req.Config
	}
	if req.Health != "" {
		updates["health"] = req.Health
	}
	a.db.Model(&d).Updates(updates)
	a.db.First(&d, id)
	ok(c, d)
}

func (a *App) setDeploymentStatus(c *gin.Context, status string) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var d model.AIModelDeployment
	if err := a.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "deployment not found")
		return
	}
	updates := map[string]any{"status": status}
	if status == "active" {
		now := time.Now()
		updates["deployed_at"] = &now
	}
	a.db.Model(&d).Updates(updates)

	versionStatus := "available"
	if status == "active" {
		versionStatus = "deployed"
	}
	a.db.Model(&model.AIModelVersion{}).Where("id = ?", d.VersionID).Update("status", versionStatus)
	ok(c, gin.H{"id": id, "status": status})
}

func (a *App) activateDeployment(c *gin.Context) { a.setDeploymentStatus(c, "active") }
func (a *App) stopDeployment(c *gin.Context)     { a.setDeploymentStatus(c, "stopped") }

func (a *App) deleteDeployment(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.db.Delete(&model.AIModelDeployment{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (a *App) modelStats(c *gin.Context) {
	var models, versions, deployments, active int64
	a.db.Model(&model.AIModel{}).Count(&models)
	a.db.Model(&model.AIModelVersion{}).Count(&versions)
	a.db.Model(&model.AIModelDeployment{}).Count(&deployments)
	a.db.Model(&model.AIModelDeployment{}).Where("status = ?", "active").Count(&active)
	ok(c, gin.H{
		"models":      models,
		"versions":    versions,
		"deployments": deployments,
		"active":      active,
	})
}
