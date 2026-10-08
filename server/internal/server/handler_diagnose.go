package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/media"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/vqd"
)

// ---- 播放诊断 (playback diagnostics) ----

func (a *App) diagnoseChannel(c *gin.Context) {
	ch, _, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	if strings.TrimSpace(ch.SourceURL) == "" {
		fail(c, http.StatusBadRequest, "通道没有源地址，无法诊断")
		return
	}
	timeout := 12 * time.Second
	if v := c.Query("timeoutSec"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 60 {
			timeout = time.Duration(n) * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+2*time.Second)
	defer cancel()

	resp := gin.H{
		"channelId": ch.ID,
		"name":      ch.Name,
		"sourceUrl": ch.SourceURL,
		"online":    ch.Online,
		"status":    "ok",
	}
	probe, err := media.Probe(ctx, ch.SourceURL, timeout)
	if err != nil {
		resp["status"] = "failed"
		resp["error"] = err.Error()
		ok(c, resp)
		return
	}
	resp["probe"] = probe
	ok(c, resp)
}

// ---- 视频质量诊断 VQD (video quality diagnosis) ----

func (a *App) diagnoseVQD(c *gin.Context) {
	ch, _, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	if strings.TrimSpace(ch.SourceURL) == "" {
		fail(c, http.StatusBadRequest, "通道没有源地址，无法诊断")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()

	resp := gin.H{"channelId": ch.ID, "name": ch.Name, "status": "ok"}
	frames, err := media.GrabFrames(ctx, ch.SourceURL, 3)
	if err != nil {
		resp["status"] = "failed"
		resp["error"] = err.Error()
		ok(c, resp)
		return
	}
	imgs := make([]image.Image, 0, len(frames))
	for _, fr := range frames {
		img, _, derr := image.Decode(bytes.NewReader(fr))
		if derr != nil {
			resp["status"] = "failed"
			resp["error"] = "decode frame: " + derr.Error()
			ok(c, resp)
			return
		}
		imgs = append(imgs, img)
	}
	m := vqd.Analyze(imgs[0])
	issues := vqd.Evaluate(m)
	tm := vqd.Compare(imgs)
	issues = append(issues, vqd.EvaluateTemporal(tm)...)
	level := vqd.WorstLevel(issues)
	resp["metrics"] = m
	resp["temporal"] = tm
	resp["issues"] = issues
	resp["level"] = level

	// Optionally record a quality event so it flows into the event center,
	// notifications and alert policies.
	if c.Query("record") == "1" && (level == "warning" || level == "critical") {
		var msgs []string
		for _, i := range issues {
			msgs = append(msgs, i.Message)
		}
		payload, _ := json.Marshal(map[string]any{"metrics": m, "temporal": tm, "issues": issues})
		ev := model.AIEvent{
			ChannelID: ch.ID, Kind: "vqd", EventType: "vqd_quality", Level: level,
			Summary: "视频质量异常：" + strings.Join(msgs, "；"),
			Payload: string(payload), OccurredAt: time.Now(),
		}
		if err := a.db.Create(&ev).Error; err == nil {
			resp["eventId"] = ev.ID
			if a.sink != nil {
				a.sink.OnEvent(ev)
			}
		}
	}
	ok(c, resp)
}
