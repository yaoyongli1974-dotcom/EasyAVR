package server

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/transcode"
)

// TranscodeJob handlers: derived streams (转码/OSD 水印) live on the media
// core as live/<streamKey>_t; processes are owned by tr Service.

func deriveStreamKey(base string) string {
	if base == "" || strings.Contains(base, "_t") {
		return ""
	}
	return base + "_t"
}

func (a *App) validateJobPayload(job *model.TranscodeJob) string {
	if err := transcode.Validate(job); err != nil {
		return err.Error()
	}
	if utf8.RuneCountInString(job.Text) > 100 {
		return "水印文本过长（≤100 字）"
	}
	if job.Name == "" {
		return "任务名称不能为空"
	}
	return ""
}

func (a *App) listTranscodeJobs(c *gin.Context) {
	var jobs []model.TranscodeJob
	a.db.Order("id DESC").Find(&jobs)
	var chs []model.Channel
	a.db.Find(&chs)
	names := map[uint]string{}
	for _, ch := range chs {
		names[ch.ID] = ch.Name
	}
	out := make([]gin.H, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, a.transcodeView(j, names[j.ChannelID], false))
	}
	ok(c, gin.H{"list": out})
}

func (a *App) transcodeView(j model.TranscodeJob, chName string, playable bool) gin.H {
	v := gin.H{
		"job":       j,
		"channel":   chName,
		"running":   a.tr.Running(j.ID),
		"fontFound": transcode.DrawFontFile() != "",
	}
	if playable {
		v["playUrls"] = a.zlm.PlayURLs(j.StreamKey)
	}
	return v
}

func (a *App) createTranscodeJob(c *gin.Context) {
	var job model.TranscodeJob
	if err := c.ShouldBindJSON(&job); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, job.ChannelID).Error; err != nil {
		fail(c, http.StatusBadRequest, "通道不存在")
		return
	}
	job.StreamKey = deriveStreamKey(ch.StreamKey)
	if job.StreamKey == "" {
		fail(c, http.StatusBadRequest, "通道缺少流标识")
		return
	}
	if msg := a.validateJobPayload(&job); msg != "" {
		fail(c, http.StatusBadRequest, msg)
		return
	}
	job.Status = "stopped"
	if err := a.db.Create(&job).Error; err != nil {
		fail(c, http.StatusInternalServerError, fmt.Sprintf("创建失败: %v", err))
		return
	}
	ok(c, a.transcodeView(job, ch.Name, false))
}

func (a *App) getTranscodeJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var job model.TranscodeJob
	if err := a.db.First(&job, id).Error; err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	var ch model.Channel
	chName := ""
	if a.db.First(&ch, job.ChannelID).Error == nil {
		chName = ch.Name
	}
	ok(c, a.transcodeView(job, chName, true))
}

func (a *App) updateTranscodeJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var job model.TranscodeJob
	if err := a.db.First(&job, id).Error; err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	if a.tr.Running(job.ID) {
		fail(c, http.StatusConflict, "请先停止任务再修改")
		return
	}
	var in model.TranscodeJob
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	// Immutable: channel and derived stream key.
	in.ID, in.ChannelID = job.ID, job.ChannelID
	in.StreamKey = job.StreamKey
	in.Status = job.Status
	if msg := a.validateJobPayload(&in); msg != "" {
		fail(c, http.StatusBadRequest, msg)
		return
	}
	if err := a.db.Model(&job).Updates(map[string]any{
		"name": in.Name, "mode": in.Mode, "text": in.Text,
		"position": in.Position, "font_size": in.FontSize,
		"width": in.Width, "height": in.Height, "bitrate": in.Bitrate,
		"enabled": in.Enabled,
	}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.db.First(&job, id)
	ok(c, a.transcodeView(job, "", false))
}

func (a *App) deleteTranscodeJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	_ = a.tr.Stop(id)
	if err := a.db.Delete(&model.TranscodeJob{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, nil)
}

func (a *App) startTranscodeJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if a.tr.Running(id) {
		fail(c, http.StatusConflict, "任务已在运行")
		return
	}
	if err := a.tr.Start(id); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var job model.TranscodeJob
	a.db.First(&job, id)
	ok(c, a.transcodeView(job, "", true))
}

func (a *App) stopTranscodeJob(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.tr.Stop(id); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var job model.TranscodeJob
	a.db.First(&job, id)
	ok(c, a.transcodeView(job, "", true))
}
