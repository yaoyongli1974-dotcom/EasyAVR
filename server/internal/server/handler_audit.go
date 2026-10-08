package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- audit log ----

func (a *App) listAuditLogs(c *gin.Context) {
	query := a.db.Model(&model.AuditLog{}).Order("created_at DESC")

	// Filters
	if username := c.Query("username"); username != "" {
		query = query.Where("username LIKE ?", "%"+username+"%")
	}
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if resource := c.Query("resource"); resource != "" {
		query = query.Where("resource = ?", resource)
	}
	if result := c.Query("result"); result != "" {
		query = query.Where("result = ?", result)
	}
	if ip := c.Query("ip"); ip != "" {
		query = query.Where("ip = ?", ip)
	}
	if startStr := c.Query("start"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if endStr := c.Query("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			query = query.Where("created_at <= ?", t)
		}
	}
	if keyword := c.Query("keyword"); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("path LIKE ? OR error_msg LIKE ? OR request_body LIKE ?", like, like, like)
	}

	// Pagination
	page := 1
	pageSize := 50
	if p := c.Query("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	if ps := c.Query("pageSize"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil && v > 0 && v <= 200 {
			pageSize = v
		}
	}

	var total int64
	query.Count(&total)

	var logs []model.AuditLog
	query.Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs)

	ok(c, gin.H{
		"items":    logs,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

func (a *App) getAuditLog(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var log model.AuditLog
	if err := a.db.First(&log, id).Error; err != nil {
		fail(c, http.StatusNotFound, "audit log not found")
		return
	}
	ok(c, log)
}

func (a *App) exportAuditLogs(c *gin.Context) {
	query := a.db.Model(&model.AuditLog{}).Order("created_at DESC")

	// Same filters as listAuditLogs
	if username := c.Query("username"); username != "" {
		query = query.Where("username LIKE ?", "%"+username+"%")
	}
	if action := c.Query("action"); action != "" {
		query = query.Where("action = ?", action)
	}
	if resource := c.Query("resource"); resource != "" {
		query = query.Where("resource = ?", resource)
	}
	if result := c.Query("result"); result != "" {
		query = query.Where("result = ?", result)
	}
	if ip := c.Query("ip"); ip != "" {
		query = query.Where("ip = ?", ip)
	}
	if startStr := c.Query("start"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			query = query.Where("created_at >= ?", t)
		}
	}
	if endStr := c.Query("end"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			query = query.Where("created_at <= ?", t)
		}
	}
	if keyword := c.Query("keyword"); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("path LIKE ? OR error_msg LIKE ? OR request_body LIKE ?", like, like, like)
	}

	var logs []model.AuditLog
	query.Limit(10000).Find(&logs)

	// Generate CSV
	var sb strings.Builder
	sb.WriteString("ID,Time,User,IP,Method,Path,Action,Resource,ResourceID,Result,Error,Latency(ms)\n")
	for _, l := range logs {
		sb.WriteString(strconv.FormatUint(uint64(l.ID), 10) + ",")
		sb.WriteString(l.CreatedAt.Format(time.RFC3339) + ",")
		sb.WriteString(escapeCSV(l.Username) + ",")
		sb.WriteString(escapeCSV(l.IP) + ",")
		sb.WriteString(l.Method + ",")
		sb.WriteString(escapeCSV(l.Path) + ",")
		sb.WriteString(l.Action + ",")
		sb.WriteString(l.Resource + ",")
		sb.WriteString(l.ResourceID + ",")
		sb.WriteString(l.Result + ",")
		sb.WriteString(escapeCSV(l.ErrorMsg) + ",")
		sb.WriteString(strconv.FormatInt(l.LatencyMs, 10) + "\n")
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=audit_logs_"+time.Now().Format("20060102_150405")+".csv")
	c.String(http.StatusOK, sb.String())
}

func escapeCSV(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		s = strings.ReplaceAll(s, "\"", "\"\"")
		return "\"" + s + "\""
	}
	return s
}
