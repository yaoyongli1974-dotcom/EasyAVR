package server

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/auth"
	"github.com/easyavr/easyavr/internal/model"
)

func (a *App) cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := a.cfg.AllowOrigin
		if origin == "" {
			origin = "*"
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// authRequired validates the Bearer token and stores claims in the context.
func (a *App) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer"))
		if token == "" || token == header {
			fail(c, http.StatusUnauthorized, "missing bearer token")
			c.Abort()
			return
		}
		claims, err := a.jwt.Parse(token)
		if err != nil {
			fail(c, http.StatusUnauthorized, "invalid or expired token")
			c.Abort()
			return
		}
		c.Set("claims", claims)
		c.Next()
	}
}

// auditLog records an audit log entry for each authenticated request.
func (a *App) auditLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		// Skip health check and static files
		path := c.Request.URL.Path
		if path == "/api/v1/healthz" || strings.HasPrefix(path, "/snapshots") || strings.HasPrefix(path, "/assets") || path == "/favicon.ico" {
			return
		}
		claims := currentClaims(c)
		var userID uint
		var username string
		if claims != nil {
			userID = claims.UserID
			username = claims.Username
		}
		status := c.Writer.Status()
		result := "success"
		var errorMsg string
		if status >= 400 {
			result = "failed"
			// Try to get error message from response
			if len(c.Errors) > 0 {
				errorMsg = c.Errors.String()
			}
		}
		// Determine action from method and path
		action := inferAction(c.Request.Method, path)
		resource := inferResource(path)
		resourceID := inferResourceID(c)
		// Request body (skip for GET, limit size)
		var reqBody string
		var body []byte
		if c.Request.Method != http.MethodGet && c.Request.Body != nil {
			var err error
			body, err = io.ReadAll(c.Request.Body)
			if err == nil && len(body) > 0 {
				if len(body) > 2048 {
					reqBody = string(body[:2048]) + "..."
				} else {
					reqBody = string(body)
				}
			}
			// Restore body for downstream handlers
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
		}

		a.db.Create(&model.AuditLog{
			UserID:      userID,
			Username:    username,
			IP:          c.ClientIP(),
			Method:      c.Request.Method,
			Path:        path,
			Action:      action,
			Resource:    resource,
			ResourceID:  resourceID,
			Result:      result,
			ErrorMsg:    errorMsg,
			RequestBody: reqBody,
			LatencyMs:   time.Since(start).Milliseconds(),
		})
	}
}

// inferAction infers the action type from HTTP method and path.
func inferAction(method, path string) string {
	switch method {
	case http.MethodPost:
		if strings.Contains(path, "/login") {
			return "login"
		}
		if strings.Contains(path, "/logout") {
			return "logout"
		}
		if strings.Contains(path, "/start") || strings.Contains(path, "/run") {
			return "start"
		}
		if strings.Contains(path, "/stop") {
			return "stop"
		}
		if strings.Contains(path, "/sync") {
			return "sync"
		}
		if strings.Contains(path, "/import") {
			return "import"
		}
		if strings.Contains(path, "/bind") {
			return "bind"
		}
		if strings.Contains(path, "/unbind") {
			return "unbind"
		}
		if strings.Contains(path, "/assign") {
			return "assign"
		}
		if strings.Contains(path, "/enroll") || strings.Contains(path, "/sign-csr") || strings.Contains(path, "/generate") {
			return "issue"
		}
		if strings.Contains(path, "/revoke") {
			return "revoke"
		}
		if strings.Contains(path, "/test") {
			return "test"
		}
		if strings.Contains(path, "/gps") {
			return "update_gps"
		}
		if strings.Contains(path, "/password") {
			return "change_password"
		}
		if strings.HasSuffix(path, "/gps") {
			return "update_gps"
		}
		return "create"
	case http.MethodPut, http.MethodPatch:
		return "update"
	case http.MethodDelete:
		return "delete"
	case http.MethodGet:
		if strings.Contains(path, "/export") || strings.Contains(path, "/download") {
			return "export"
		}
		return "read"
	default:
		return method
	}
}

// inferResource extracts resource name from path.
func inferResource(path string) string {
	// /api/v1/devices -> devices
	// /api/v1/channels/1/start -> channels
	// /api/v1/ai/tasks -> ai_tasks
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/")
	if len(parts) >= 1 && parts[0] != "" {
		r := parts[0]
		// Normalize plural names
		switch r {
		case "apikeys":
			return "api_keys"
		case "notify":
			return "notification"
		case "gb35114":
			return "gb35114"
		case "ga1400":
			return "ga1400"
		}
		return r
	}
	return ""
}

// inferResourceID extracts resource ID from path params.
func inferResourceID(c *gin.Context) string {
	// Try common param names
	for _, name := range []string{"id", "deviceId", "channelId", "groupId", "keyId", "cascadeId", "providerId", "taskId"} {
		if v := c.Param(name); v != "" {
			return v
		}
	}
	return ""
}

// apiKeyRequired authenticates a third-party/APP request with an API key,
// enforces its scope, rate limit and daily quota, and audits the call. Keys are
// passed via X-API-Key or "Authorization: ApiKey <key>".
func (a *App) apiKeyRequired(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		plain := extractAPIKey(c)
		if plain == "" {
			fail(c, http.StatusUnauthorized, "missing api key")
			c.Abort()
			return
		}
		var key model.APIKey
		if err := a.db.Where("key_hash = ?", auth.HashAPIKey(plain)).First(&key).Error; err != nil {
			fail(c, http.StatusUnauthorized, "invalid api key")
			c.Abort()
			return
		}
		if !key.Enabled {
			fail(c, http.StatusUnauthorized, "api key disabled")
			c.Abort()
			return
		}
		if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
			fail(c, http.StatusUnauthorized, "api key expired")
			c.Abort()
			return
		}
		if !hasScope(key.Scopes, scope) {
			fail(c, http.StatusForbidden, "api key missing scope: "+scope)
			c.Abort()
			return
		}
		if ok, remaining := a.limiter.Allow(key.ID, key.RateLimit); !ok {
			c.Header("Retry-After", "60")
			fail(c, http.StatusTooManyRequests, "rate limit exceeded")
			a.audit(&key, c, http.StatusTooManyRequests, start)
			c.Abort()
			return
		} else if remaining >= 0 {
			c.Header("X-RateLimit-Remaining", strconv.Itoa(remaining))
		}
		if key.QuotaPerDay > 0 {
			now := time.Now()
			if key.QuotaResetAt.IsZero() || !sameDay(key.QuotaResetAt, now) {
				a.db.Model(&model.APIKey{}).Where("id = ?", key.ID).
					Updates(map[string]any{"used_today": 0, "quota_reset_at": now})
				key.UsedToday = 0
			}
			if key.UsedToday >= key.QuotaPerDay {
				fail(c, http.StatusTooManyRequests, "daily quota exceeded")
				a.audit(&key, c, http.StatusTooManyRequests, start)
				c.Abort()
				return
			}
			a.db.Model(&model.APIKey{}).Where("id = ?", key.ID).
				UpdateColumn("used_today", gorm.Expr("used_today + 1"))
		}
		now := time.Now()
		a.db.Model(&key).Updates(map[string]any{"last_used_at": now, "last_used_ip": c.ClientIP()})
		c.Set("apiKey", &key)
		c.Next()
		a.audit(&key, c, c.Writer.Status(), start)
	}
}

// audit records an open API call for accounting.
func (a *App) audit(key *model.APIKey, c *gin.Context, status int, start time.Time) {
	a.db.Create(&model.APIRequestLog{
		KeyID: key.ID, KeyName: key.Name, Method: c.Request.Method,
		Path: c.Request.URL.Path, Status: status,
		LatencyMs: time.Since(start).Milliseconds(), IP: c.ClientIP(),
	})
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func extractAPIKey(c *gin.Context) string {
	if k := strings.TrimSpace(c.GetHeader("X-API-Key")); k != "" {
		return k
	}
	h := c.GetHeader("Authorization")
	if after, ok := strings.CutPrefix(h, "ApiKey "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func hasScope(scopes, want string) bool {
	for _, s := range strings.Split(scopes, ",") {
		switch strings.TrimSpace(s) {
		case want, "*":
			return true
		}
	}
	return false
}

// adminRequired allows only the built-in admin role.
func (a *App) adminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := currentClaims(c)
		if claims == nil || claims.Role != "admin" {
			fail(c, http.StatusForbidden, "需要管理员权限")
			c.Abort()
			return
		}
		c.Next()
	}
}

// requirePerm allows the admin role or any role whose permission list contains
// the requested key ("*" means all).
func (a *App) requirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := currentClaims(c)
		if claims == nil {
			fail(c, http.StatusUnauthorized, "unauthenticated")
			c.Abort()
			return
		}
		if claims.Role == "admin" {
			c.Next()
			return
		}
		var role model.Role
		if err := a.db.Where("name = ?", claims.Role).First(&role).Error; err == nil && hasPerm(role.Permissions, perm) {
			c.Next()
			return
		}
		fail(c, http.StatusForbidden, "权限不足："+perm)
		c.Abort()
	}
}

func hasPerm(list, want string) bool {
	if strings.TrimSpace(list) == "*" {
		return true
	}
	for _, p := range strings.Split(list, ",") {
		if strings.TrimSpace(p) == want {
			return true
		}
	}
	return false
}

// currentClaims returns the authenticated user's claims, or nil.
func currentClaims(c *gin.Context) *auth.Claims {
	if v, ok := c.Get("claims"); ok {
		if claims, ok := v.(*auth.Claims); ok {
			return claims
		}
	}
	return nil
}

// requireGroupPerm checks if the user has access to a device/channel through
// group membership. It looks for deviceID/channelID in query/param/body.
// The perm argument is the permission key required (e.g. "device", "video").
// If the user is admin or has the perm via role, it passes.
// Otherwise, it checks UserGroup entries for groups containing the target
// device/channel; if the user has the perm (or empty = inherit role) for that
// group, it passes.
func (a *App) requireGroupPerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := currentClaims(c)
		if claims == nil {
			fail(c, http.StatusUnauthorized, "unauthenticated")
			c.Abort()
			return
		}
		if claims.Role == "admin" {
			c.Next()
			return
		}
		// Role-level permission check first.
		var role model.Role
		if err := a.db.Where("name = ?", claims.Role).First(&role).Error; err == nil && hasPerm(role.Permissions, perm) {
			c.Next()
			return
		}

		// Extract target device/channel ID.
		var deviceID, channelID uint
		if v := c.Query("deviceId"); v != "" {
			if id, err := strconv.ParseUint(v, 10, 64); err == nil {
				deviceID = uint(id)
			}
		} else if v := c.Query("channelId"); v != "" {
			if id, err := strconv.ParseUint(v, 10, 64); err == nil {
				channelID = uint(id)
			}
		} else if v := c.Param("deviceId"); v != "" {
			if id, err := strconv.ParseUint(v, 10, 64); err == nil {
				deviceID = uint(id)
			}
		} else if v := c.Param("channelId"); v != "" {
			if id, err := strconv.ParseUint(v, 10, 64); err == nil {
				channelID = uint(id)
			}
		} else if v := c.Param("id"); v != "" {
			// Generic :id param - try to determine if it's device or channel by checking the route path
			if id, err := strconv.ParseUint(v, 10, 64); err == nil {
				path := c.Request.URL.Path
				if strings.Contains(path, "/devices/") && !strings.Contains(path, "/channels/") {
					deviceID = uint(id)
				} else if strings.Contains(path, "/channels/") {
					channelID = uint(id)
				}
			}
		} else if c.Request.Method != http.MethodGet {
			var body map[string]any
			if err := c.ShouldBindJSON(&body); err == nil {
				if v, ok := body["deviceId"].(float64); ok {
					deviceID = uint(v)
				}
				if v, ok := body["channelId"].(float64); ok {
					channelID = uint(v)
				}
			}
		}

		if deviceID == 0 && channelID == 0 {
			// No target specified; allow (caller will 404 if needed).
			c.Next()
			return
		}

		// Build group IDs containing the target device/channel.
		var groupIDs []uint
		if deviceID > 0 {
			a.db.Model(&model.DeviceGroupDevice{}).
				Where("device_id = ?", deviceID).
				Pluck("group_id", &groupIDs)
			// Also include groups via device's direct GroupID.
			var dg uint
			a.db.Model(&model.Device{}).Where("id = ?", deviceID).Pluck("group_id", &dg)
			if dg > 0 {
				groupIDs = append(groupIDs, dg)
			}
		}
		if channelID > 0 {
			a.db.Model(&model.ChannelGroupChannel{}).
				Where("channel_id = ?", channelID).
				Pluck("group_id", &groupIDs)
			// Channel -> Device -> Group
			var did uint
			a.db.Model(&model.Channel{}).Where("id = ?", channelID).Pluck("device_id", &did)
			if did > 0 {
				a.db.Model(&model.DeviceGroupDevice{}).
					Where("device_id = ?", did).
					Pluck("group_id", &groupIDs)
				var dg uint
				a.db.Model(&model.Device{}).Where("id = ?", did).Pluck("group_id", &dg)
				if dg > 0 {
					groupIDs = append(groupIDs, dg)
				}
			}
		}
		if len(groupIDs) == 0 {
			// Target not in any group; deny unless role has perm.
			fail(c, http.StatusForbidden, "权限不足："+perm)
			c.Abort()
			return
		}

		// Check UserGroup for any of these groups.
		var ugs []model.UserGroup
		a.db.Where("user_id = ? AND group_id IN ?", claims.UserID, groupIDs).Find(&ugs)
		for _, ug := range ugs {
			if ug.Permissions == "" || hasPerm(ug.Permissions, perm) {
				c.Next()
				return
			}
		}
		fail(c, http.StatusForbidden, "权限不足："+perm)
		c.Abort()
	}
}
