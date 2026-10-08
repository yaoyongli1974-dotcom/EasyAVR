package server

import (
	"net/http"

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
