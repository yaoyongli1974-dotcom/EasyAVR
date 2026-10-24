package server

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

func (a *App) startRecording(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.rec.Start(id); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "recording": true})
}

func (a *App) stopRecording(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.rec.Stop(id); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"id": id, "recording": false})
}

func (a *App) listChannelRecordings(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	items, err := a.rec.List(id, c.Query("date"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, items)
}

func (a *App) syncChannelRecordings(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	items, err := a.rec.Sync(id, c.Query("date"))
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "count": len(items)})
}

func (a *App) listRecordings(c *gin.Context) {
	var channelID uint
	if v := c.Query("channelId"); v != "" {
		channelID = parseChannelID(v)
	}
	items, err := a.rec.List(channelID, c.Query("date"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, items)
}

func (a *App) deleteRecording(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if err := a.rec.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

// markRecording sets/clears the emergency mark on a recording (手册 3.3.3 紧急标记).
func (a *App) markRecording(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var req struct {
		Marked bool   `json:"marked"`
		Mark   string `json:"mark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if err := a.db.Model(&model.Recording{}).Where("id = ?", id).
		Updates(map[string]any{"marked": req.Marked, "mark": req.Mark}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var rec model.Recording
	a.db.First(&rec, id)
	ok(c, rec)
}

// cleanupRecordings enforces the retention policy (手册 3.7.2.1 录像存储阈值).
// days>0 applies a window to every channel, otherwise each channel plan's
// RetentionDays decides. files=1 (default) also deletes the day folders on the
// media core; files=0 only prunes the catalog.
func (a *App) cleanupRecordings(c *gin.Context) {
	days, _ := strconv.Atoi(c.Query("days"))
	deleteFiles := c.Query("files") != "0"
	removed, err := a.rec.Cleanup(days, deleteFiles)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"removed": removed, "filesDeleted": deleteFiles})
}

func (a *App) getRecordingPlan(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	plan, err := a.rec.GetPlan(id)
	if err != nil {
		// No plan yet: return a disabled default rather than 404.
		ok(c, model.RecordingPlan{ChannelID: id, Days: "daily", StartTime: "00:00", EndTime: "23:59", RetentionDays: 7, StreamType: "main"})
		return
	}
	ok(c, plan)
}

type recordingPlanRequest struct {
	Enabled       bool   `json:"enabled"`
	Days          string `json:"days"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	RetentionDays int    `json:"retentionDays"`
	StreamType    string `json:"streamType"`
}

func (a *App) upsertRecordingPlan(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var req recordingPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	plan, err := a.rec.UpsertPlan(model.RecordingPlan{
		ChannelID: id, Enabled: req.Enabled, Days: req.Days,
		StartTime: req.StartTime, EndTime: req.EndTime,
		RetentionDays: req.RetentionDays, StreamType: req.StreamType,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, plan)
}
