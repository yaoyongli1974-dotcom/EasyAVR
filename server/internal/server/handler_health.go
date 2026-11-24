package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/storage"
)

// healthAlert is a sanitized capacity alert for the public readiness probe.
// The directory path is intentionally omitted: a detailed, authenticated view
// of the full storage snapshot is available through healthzDetailed.
type healthAlert struct {
	Level       string  `json:"level"`
	Message     string  `json:"message"`
	UsedPercent float64 `json:"usedPercent,omitempty"`
}

// healthReport is the shared payload of the readiness probe and the detailed
// endpoint. It contains only booleans, aggregate counters and short status
// strings: no configuration values, DSNs, credentials or environment values.
type healthReport struct {
	Status  string        `json:"status"`
	Time    string        `json:"time"`
	Reason  string        `json:"reason,omitempty"`
	Checks  gin.H         `json:"checks"`
	Alerts  []healthAlert `json:"alerts"`
	Storage any           `json:"storage,omitempty"`
}

// healthAlertWarning is advisory: AI/storage alerts never fail readiness.
const healthAlertWarning = "warning"

// formatHealthTime renders an optional timestamp for the detailed probe. A
// nil pointer yields an empty string rather than a zero time or config value.
func formatHealthTime(ts *time.Time) string {
	if ts == nil {
		return ""
	}
	return ts.Format(time.RFC3339)
}

// aiHealthCheck maps a sanitized ai.HealthStats into the checks.ai object. The
// public probe carries only aggregate state; timestamps, counters and recovery
// detail are added to the authenticated detailed view. HealthStats never
// exposes prompts, provider keys, DSNs or configuration values.
func aiHealthCheck(stats ai.HealthStats, detailed bool) gin.H {
	status := stats.Status
	if status == "" {
		status = ai.HealthUnavailable
	}
	check := gin.H{
		"status":    status,
		"available": stats.Available,
		"closed":    stats.Closed,
		"healthy":   stats.Healthy(),
	}
	if detailed {
		check["runningTasks"] = stats.RunningTasks
		check["waitingTasks"] = stats.WaitingTasks
		check["activeRuns"] = stats.ActiveRuns
		check["queuedRuns"] = stats.QueuedRuns
		check["consecutiveFailures"] = stats.ConsecutiveFailures
		check["longRunningRuns"] = stats.LongRunningRuns
		check["staleHeartbeats"] = stats.StaleHeartbeats
		check["lastRunAt"] = formatHealthTime(stats.LastRunAt)
		check["lastHeartbeatAt"] = formatHealthTime(stats.LastHeartbeatAt)
		check["lastRecoverAt"] = formatHealthTime(stats.LastRecoverAt)
		check["recoverAttempted"] = stats.RecoverAttempted
		check["lastRecoverOK"] = stats.LastRecoverOK
		check["lastRecoverError"] = stats.LastRecoverError
		check["dbAvailable"] = stats.DBAvailable
		check["runCreateFailures"] = stats.RunCreateFailures
		check["runUpdateFailures"] = stats.RunUpdateFailures
		check["taskStatusFailures"] = stats.TaskStatusFailures
	}
	return check
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

// checkTiming adds a per-check duration to the detailed view. The map is
// owned by collectHealth and is not shared with other requests.
func checkTiming(check gin.H, detailed bool, start time.Time) gin.H {
	if detailed {
		check["durationMs"] = elapsedMS(start)
	}
	return check
}

func checkStatus(ok bool, good, bad string) string {
	if ok {
		return good
	}
	return bad
}

// collectHealth performs one pass of all probes. Only the database is a hard
// dependency; every other plane (media, AI, GB, storage capacity) is reported
// without affecting readiness. detailed adds timings and the complete storage
// health snapshot.
func (a *App) collectHealth(detailed bool) (healthReport, bool) {
	report := healthReport{
		Status: "ready",
		Time:   time.Now().Format(time.RFC3339),
		Checks: gin.H{},
		Alerts: []healthAlert{},
	}
	ready := true

	// Database: the only probe that can make the platform unready. The raw
	// driver error is dropped because it may embed a DSN.
	start := time.Now()
	dbCheck := gin.H{"status": "up", "ok": true}
	if a.db == nil {
		dbCheck = gin.H{"status": "down", "ok": false, "message": "database unavailable"}
		ready = false
	} else if err := a.db.Exec("SELECT 1").Error; err != nil {
		dbCheck = gin.H{"status": "down", "ok": false, "message": "database unavailable"}
		ready = false
	}
	report.Checks["db"] = checkTiming(dbCheck, detailed, start)
	if !ready {
		report.Reason = "database unavailable"
	}

	// Storage capacity is advisory: surface warning/critical alerts but never
	// fail readiness because a disk is getting full.
	start = time.Now()
	storageCheck := gin.H{"status": "skipped", "healthy": false}
	if a.storage != nil {
		h := a.storage.Health()
		storageCheck = gin.H{"status": "ok", "healthy": true}
		for _, alert := range h.Alerts {
			if alert.Level == storage.LevelCritical || alert.Level == storage.LevelWarning {
				report.Alerts = append(report.Alerts, healthAlert{
					Level:       alert.Level,
					Message:     alert.Message,
					UsedPercent: alert.UsedPercent,
				})
			}
		}
		if detailed {
			report.Storage = h
			storageCheck["usedPercent"] = h.UsedPercent
		}
	}
	report.Checks["storage"] = checkTiming(storageCheck, detailed, start)

	// ZLM: report only. A missing media daemon must not fail readiness.
	start = time.Now()
	zlmCheck := gin.H{"status": "disabled", "healthy": false}
	if a.zlm != nil {
		healthy := a.zlm.Healthy()
		zlmCheck = gin.H{
			"status":  checkStatus(healthy, "up", "down"),
			"healthy": healthy,
		}
	}
	report.Checks["zlm"] = checkTiming(zlmCheck, detailed, start)

	// AI worker/scheduler health is advisory: scheduling anomalies are surfaced
	// as alerts but never fail readiness (only the database can do that).
	start = time.Now()
	aiStats := ai.HealthStats{Status: ai.HealthUnavailable, Closed: true}
	if a.runner != nil {
		aiStats = a.runner.HealthStats()
		for _, message := range aiStats.Alerts {
			report.Alerts = append(report.Alerts, healthAlert{Level: healthAlertWarning, Message: message})
		}
	}
	report.Checks["ai"] = checkTiming(aiHealthCheck(aiStats, detailed), detailed, start)

	// GB28181 / GB35114: only whether the plane is configured and running.
	start = time.Now()
	gbEnabled := a.cfg != nil && a.cfg.GB.Enabled
	report.Checks["gb"] = checkTiming(gin.H{
		"status":  checkStatus(gbEnabled, "enabled", "disabled"),
		"enabled": gbEnabled,
		"running": a.gb != nil,
	}, detailed, start)

	start = time.Now()
	gb35114Enabled := a.cfg != nil && a.cfg.GB35114.Enabled
	report.Checks["gb35114"] = checkTiming(gin.H{
		"status":  checkStatus(gb35114Enabled, "enabled", "disabled"),
		"enabled": gb35114Enabled,
		"running": a.gb35114 != nil,
	}, detailed, start)

	// Background workers: presence only, no status strings copied from config.
	start = time.Now()
	tasksCheck := gin.H{
		"status":    checkStatus(a.rec != nil || a.snaps != nil || a.runner != nil, "ok", "none"),
		"recording": a.rec != nil,
		"snapshots": a.snaps != nil,
		"runner":    a.runner != nil,
	}
	report.Checks["tasks"] = checkTiming(tasksCheck, detailed, start)

	if !ready {
		report.Status = "degraded"
	}
	return report, ready
}

// readyz is a public liveness/readiness probe. Only an unavailable database
// produces a 503; every other plane is reported without failing the probe.
func (a *App) readyz(c *gin.Context) {
	report, ready := a.collectHealth(false)
	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    http.StatusServiceUnavailable,
			"message": "degraded",
			"data":    report,
		})
		return
	}
	ok(c, report)
}

// healthzDetailed is the authenticated diagnostic view. It reuses the same
// probes, adds per-check timings and the full storage.Health snapshot.
func (a *App) healthzDetailed(c *gin.Context) {
	report, ready := a.collectHealth(true)
	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    http.StatusServiceUnavailable,
			"message": "degraded",
			"data":    report,
		})
		return
	}
	ok(c, report)
}
