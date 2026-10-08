package server

import (
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

// currentClaims returns the authenticated user's claims, or nil.
func currentClaims(c *gin.Context) *auth.Claims {
	if v, ok := c.Get("claims"); ok {
		if claims, ok := v.(*auth.Claims); ok {
			return claims
		}
	}
	return nil
}
