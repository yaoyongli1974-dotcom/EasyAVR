package server

import (
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

var startedAt = time.Now()

func (a *App) healthz(c *gin.Context) {
	ok(c, gin.H{"status": "ok", "time": time.Now().Format(time.RFC3339)})
}

// systemInfo reports platform-level status across all planes.
func (a *App) systemInfo(c *gin.Context) {
	var devices, channels, providers, tasks, events int64
	a.db.Model(&model.Device{}).Count(&devices)
	a.db.Model(&model.Channel{}).Count(&channels)
	a.db.Model(&model.AIProvider{}).Count(&providers)
	a.db.Model(&model.AITask{}).Count(&tasks)
	a.db.Model(&model.AIEvent{}).Count(&events)

	ok(c, gin.H{
		"name":      "EasyAVR",
		"version":   "0.1.0",
		"uptime":    time.Since(startedAt).Round(time.Second).String(),
		"goVersion": runtime.Version(),
		"planes": gin.H{
			"deviceAccess": gin.H{"devices": devices, "channels": channels},
			"video":        gin.H{"zlmHealthy": a.zlm.Healthy(), "zlmApi": a.cfg.ZLM.APIBase},
			"ai":           gin.H{"providers": providers, "tasks": tasks, "events": events},
		},
	})
}
