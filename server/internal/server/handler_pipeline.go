package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- datasets ----

func (a *App) listDatasets(c *gin.Context) {
	var items []model.Dataset
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createDataset(c *gin.Context) {
	var d model.Dataset
	if err := c.ShouldBindJSON(&d); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if d.Source == "" {
		d.Source = "manual"
	}
	if d.Status == "" {
		d.Status = "draft"
	}
	if claims := currentClaims(c); claims != nil {
		d.CreatedBy = claims.Username
	}
	if err := a.db.Create(&d).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, d)
}

func (a *App) getDataset(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var d model.Dataset
	if err := a.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "dataset not found")
		return
	}
	a.recomputeDataset(&d)
	var samples []model.DatasetSample
	a.db.Where("dataset_id = ?", id).Order("id DESC").Limit(200).Find(&samples)
	ok(c, gin.H{"dataset": d, "samples": samples})
}

func (a *App) updateDataset(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var d model.Dataset
	if err := a.db.First(&d, id).Error; err != nil {
		fail(c, http.StatusNotFound, "dataset not found")
		return
	}
	var req model.Dataset
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Kind != "" {
		updates["kind"] = req.Kind
	}
	if req.Source != "" {
		updates["source"] = req.Source
	}
	if req.Labels != "" {
		updates["labels"] = req.Labels
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	a.db.Model(&d).Updates(updates)
	a.db.First(&d, id)
	ok(c, d)
}

func (a *App) deleteDataset(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	err := a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("dataset_id = ?", id).Delete(&model.DatasetSample{}).Error; err != nil {
			return err
		}
		if err := tx.Where("dataset_id = ?", id).Delete(&model.AnnotationTask{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Dataset{}, id).Error
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (a *App) recomputeDataset(d *model.Dataset) {
	var total, labeled int64
	a.db.Model(&model.DatasetSample{}).Where("dataset_id = ?", d.ID).Count(&total)
	a.db.Model(&model.DatasetSample{}).Where("dataset_id = ? AND status != ?", d.ID, "unlabeled").Count(&labeled)
	d.SampleCount = int(total)
	d.LabeledCount = int(labeled)
	a.db.Model(d).Updates(map[string]any{"sample_count": d.SampleCount, "labeled_count": d.LabeledCount})
}

// ---- dataset samples ----

func (a *App) listDatasetSamples(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	tx := a.db.Where("dataset_id = ?", id)
	if st := c.Query("status"); st != "" {
		tx = tx.Where("status = ?", st)
	}
	if sp := c.Query("split"); sp != "" {
		tx = tx.Where("split = ?", sp)
	}
	var samples []model.DatasetSample
	tx.Order("id DESC").Find(&samples)
	ok(c, samples)
}

func (a *App) requireDataset(id uint) (model.Dataset, bool) {
	var d model.Dataset
	if err := a.db.First(&d, id).Error; err != nil {
		return d, false
	}
	return d, true
}

func (a *App) createDatasetSample(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	d, found := a.requireDataset(id)
	if !found {
		fail(c, http.StatusNotFound, "dataset not found")
		return
	}
	var s model.DatasetSample
	if err := c.ShouldBindJSON(&s); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	s.ID = 0
	s.DatasetID = d.ID
	if s.Split == "" {
		s.Split = "train"
	}
	if s.Status == "" {
		s.Status = "unlabeled"
	}
	if err := a.db.Create(&s).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.recomputeDataset(&d)
	ok(c, s)
}

func (a *App) updateDatasetSample(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	sid, valid := parseUintParam(c, "sampleId")
	if !valid {
		return
	}
	var s model.DatasetSample
	if err := a.db.Where("id = ? AND dataset_id = ?", sid, id).First(&s).Error; err != nil {
		fail(c, http.StatusNotFound, "sample not found")
		return
	}
	var req model.DatasetSample
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Labels != "" {
		updates["labels"] = req.Labels
	}
	if req.Split != "" {
		updates["split"] = req.Split
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Note != "" {
		updates["note"] = req.Note
	}
	if req.ImageURL != "" {
		updates["image_url"] = req.ImageURL
	}
	a.db.Model(&s).Updates(updates)
	a.db.First(&s, s.ID)
	if d, found := a.requireDataset(id); found {
		a.recomputeDataset(&d)
	}
	ok(c, s)
}

func (a *App) deleteDatasetSample(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	sid, valid := parseUintParam(c, "sampleId")
	if !valid {
		return
	}
	a.db.Where("id = ? AND dataset_id = ?", sid, id).Delete(&model.DatasetSample{})
	if d, found := a.requireDataset(id); found {
		a.recomputeDataset(&d)
	}
	ok(c, gin.H{"id": sid})
}

// importDatasetSamples copies AI events (snapshot + suggested label) into a
// dataset as unlabeled samples.
func (a *App) importDatasetSamples(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	d, found := a.requireDataset(id)
	if !found {
		fail(c, http.StatusNotFound, "dataset not found")
		return
	}
	var req struct {
		EventIDs []uint `json:"eventIds"`
		Split    string `json:"split"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.EventIDs) == 0 {
		fail(c, http.StatusBadRequest, "eventIds is required")
		return
	}
	if req.Split == "" {
		req.Split = "train"
	}
	var events []model.AIEvent
	a.db.Where("id IN ?", req.EventIDs).Find(&events)
	created := 0
	for _, e := range events {
		labels := fmt.Sprintf(`{"suggested":%q}`, e.EventType)
		s := model.DatasetSample{
			DatasetID: d.ID, EventID: e.ID, ChannelID: e.ChannelID,
			ImageURL: e.Snapshot, Labels: labels, Split: req.Split, Status: "unlabeled",
		}
		if err := a.db.Create(&s).Error; err == nil {
			created++
		}
	}
	a.recomputeDataset(&d)
	ok(c, gin.H{"created": created, "dataset": d})
}

// ---- annotation tasks ----

func (a *App) listAnnotationTasks(c *gin.Context) {
	var items []model.AnnotationTask
	tx := a.db.Order("id DESC")
	if did := c.Query("datasetId"); did != "" {
		tx = tx.Where("dataset_id = ?", did)
	}
	tx.Find(&items)
	for i := range items {
		a.refreshAnnotationProgress(&items[i])
	}
	ok(c, items)
}

func (a *App) refreshAnnotationProgress(t *model.AnnotationTask) {
	var total, labeled int64
	a.db.Model(&model.DatasetSample{}).Where("dataset_id = ?", t.DatasetID).Count(&total)
	a.db.Model(&model.DatasetSample{}).Where("dataset_id = ? AND status != ?", t.DatasetID, "unlabeled").Count(&labeled)
	t.Total = int(total)
	t.Labeled = int(labeled)
	a.db.Model(t).Updates(map[string]any{"total": t.Total, "labeled": t.Labeled})
}

func (a *App) createAnnotationTask(c *gin.Context) {
	var t model.AnnotationTask
	if err := c.ShouldBindJSON(&t); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if t.Name == "" || t.DatasetID == 0 {
		fail(c, http.StatusBadRequest, "name and datasetId are required")
		return
	}
	if _, found := a.requireDataset(t.DatasetID); !found {
		fail(c, http.StatusBadRequest, "dataset not found")
		return
	}
	if t.Status == "" {
		t.Status = "pending"
	}
	if err := a.db.Create(&t).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.refreshAnnotationProgress(&t)
	ok(c, t)
}

func (a *App) updateAnnotationTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var t model.AnnotationTask
	if err := a.db.First(&t, id).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	var req model.AnnotationTask
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Assignee != "" {
		updates["assignee"] = req.Assignee
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if req.Instructions != "" {
		updates["instructions"] = req.Instructions
	}
	a.db.Model(&t).Updates(updates)
	a.db.First(&t, id)
	a.refreshAnnotationProgress(&t)
	ok(c, t)
}

func (a *App) deleteAnnotationTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.AnnotationTask{}, id)
	ok(c, gin.H{"id": id})
}

func (a *App) completeAnnotationTask(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var t model.AnnotationTask
	if err := a.db.First(&t, id).Error; err != nil {
		fail(c, http.StatusNotFound, "task not found")
		return
	}
	a.db.Model(&t).Update("status", "completed")
	// Marking the dataset ready is a natural next step once annotation is done.
	a.db.Model(&model.Dataset{}).Where("id = ?", t.DatasetID).Update("status", "ready")
	ok(c, gin.H{"id": id, "status": "completed"})
}

// ---- training jobs ----

func (a *App) listTrainingJobs(c *gin.Context) {
	var items []model.TrainingJob
	tx := a.db.Order("id DESC")
	if did := c.Query("datasetId"); did != "" {
		tx = tx.Where("dataset_id = ?", did)
	}
	if st := c.Query("status"); st != "" {
		tx = tx.Where("status = ?", st)
	}
	tx.Find(&items)
	ok(c, items)
}

func (a *App) createTrainingJob(c *gin.Context) {
	var j model.TrainingJob
	if err := c.ShouldBindJSON(&j); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if j.Name == "" || j.DatasetID == 0 {
		fail(c, http.StatusBadRequest, "name and datasetId are required")
		return
	}
	if _, found := a.requireDataset(j.DatasetID); !found {
		fail(c, http.StatusBadRequest, "dataset not found")
		return
	}
	if j.Status == "" {
		j.Status = "queued"
	}
	if err := a.db.Create(&j).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, j)
}

func (a *App) updateTrainingJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var j model.TrainingJob
	if err := a.db.First(&j, id).Error; err != nil {
		fail(c, http.StatusNotFound, "job not found")
		return
	}
	var req model.TrainingJob
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Framework != "" {
		updates["framework"] = req.Framework
	}
	if req.HyperParams != "" {
		updates["hyper_params"] = req.HyperParams
	}
	if req.Metrics != "" {
		updates["metrics"] = req.Metrics
	}
	if req.ModelID != 0 {
		updates["model_id"] = req.ModelID
	}
	if req.BaseModelID != 0 {
		updates["base_model_id"] = req.BaseModelID
	}
	a.db.Model(&j).Updates(updates)
	a.db.First(&j, id)
	ok(c, j)
}

func (a *App) deleteTrainingJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.TrainingJob{}, id)
	ok(c, gin.H{"id": id})
}

// runTrainingJob executes a training job. Since actual training is external,
// it registers the produced model/version from the job's metrics and marks the
// job succeeded. This is the seam where a real trainer would be wired in.
func (a *App) runTrainingJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var j model.TrainingJob
	if err := a.db.First(&j, id).Error; err != nil {
		fail(c, http.StatusNotFound, "job not found")
		return
	}
	d, found := a.requireDataset(j.DatasetID)
	if !found {
		fail(c, http.StatusBadRequest, "dataset not found")
		return
	}
	if j.Status == "running" {
		fail(c, http.StatusConflict, "job already running")
		return
	}

	now := time.Now()
	a.db.Model(&j).Updates(map[string]any{"status": "running", "started_at": &now})
	a.db.First(&j, id)

	// Ensure an output model registry entry exists.
	var m model.AIModel
	if j.ModelID != 0 {
		a.db.First(&m, j.ModelID)
	}
	if m.ID == 0 {
		m = model.AIModel{
			Name: j.Name, Kind: d.Kind, Task: d.Kind, Framework: j.Framework,
			Source: "trained", Description: "由训练作业 " + j.Name + " 生成", Enabled: true,
		}
		a.db.Create(&m)
	}

	metrics := j.Metrics
	if strings.TrimSpace(metrics) == "" {
		metrics = `{"note":"离线/模拟训练产出的模型版本"}`
	}
	version := model.AIModelVersion{
		ModelID: m.ID,
		Version: fmt.Sprintf("train-%d-%s", j.ID, now.Format("20060102150405")),
		Status:  "registered", Format: j.Framework,
		Metrics: metrics, Labels: d.Labels, Params: j.HyperParams,
		Notes: fmt.Sprintf("训练作业 #%d，数据集「%s」(样本 %d)", j.ID, d.Name, d.SampleCount),
	}
	if err := a.db.Create(&version).Error; err != nil {
		a.db.Model(&j).Updates(map[string]any{"status": "failed", "finished_at": &now, "log": err.Error()})
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}

	fin := time.Now()
	a.db.Model(&j).Updates(map[string]any{
		"status": "succeeded", "model_id": m.ID, "version_id": version.ID,
		"finished_at": &fin, "log": fmt.Sprintf("registered model version %s", version.Version),
	})
	a.db.First(&j, id)
	ok(c, gin.H{"job": j, "model": m, "version": version})
}

func (a *App) cancelTrainingJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var j model.TrainingJob
	if err := a.db.First(&j, id).Error; err != nil {
		fail(c, http.StatusNotFound, "job not found")
		return
	}
	a.db.Model(&j).Update("status", "canceled")
	ok(c, gin.H{"id": id, "status": "canceled"})
}

func (a *App) pipelineStats(c *gin.Context) {
	var datasets, samples, labeled, tasks, jobs, running int64
	a.db.Model(&model.Dataset{}).Count(&datasets)
	a.db.Model(&model.DatasetSample{}).Count(&samples)
	a.db.Model(&model.DatasetSample{}).Where("status != ?", "unlabeled").Count(&labeled)
	a.db.Model(&model.AnnotationTask{}).Count(&tasks)
	a.db.Model(&model.TrainingJob{}).Count(&jobs)
	a.db.Model(&model.TrainingJob{}).Where("status IN ?", []string{"queued", "running"}).Count(&running)
	ok(c, gin.H{
		"datasets": datasets, "samples": samples, "labeled": labeled,
		"annotations": tasks, "jobs": jobs, "activeJobs": running,
	})
}
