package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/playauth"
)

func (a *App) playbackConfig(c *gin.Context) {
	ok(c, gin.H{
		"auth":        a.cfg.Playback.Auth,
		"tokenTtlMin": a.cfg.Playback.TokenTTLMin,
		"whitelist":   a.cfg.Playback.Whitelist,
	})
}

func (a *App) playTTL() time.Duration {
	min := a.cfg.Playback.TokenTTLMin
	if min <= 0 {
		min = 60
	}
	return time.Duration(min) * time.Minute
}

// signPlayURLs returns the distribution URLs, appending signed tokens when
// playback auth is enabled.
func (a *App) signPlayURLs(streamKey string) (map[string]string, int64) {
	urls := a.zlm.PlayURLs(streamKey)
	if !a.cfg.Playback.Auth {
		return urls, 0
	}
	tok, exp := playauth.Sign(a.cfg.JWTSecret, streamKey, a.playTTL())
	signed := make(map[string]string, len(urls))
	for proto, u := range urls {
		signed[proto] = playauth.AppendQuery(u, tok, exp)
	}
	return signed, exp
}

// channelPlayToken issues a time-limited share URL for a channel.
func (a *App) channelPlayToken(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if len(a.cfg.Playback.Whitelist) > 0 && !a.refererAllowed(c) {
		fail(c, http.StatusForbidden, "来源不在播放白名单内")
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	urls, exp := a.signPlayURLs(ch.StreamKey)
	resp := gin.H{"channelId": ch.ID, "streamKey": ch.StreamKey, "auth": a.cfg.Playback.Auth, "playUrls": urls}
	if exp > 0 {
		resp["expiresAt"] = time.Unix(exp, 0)
	}
	ok(c, resp)
}

func (a *App) refererAllowed(c *gin.Context) bool {
	if len(a.cfg.Playback.Whitelist) == 0 {
		return true
	}
	ref := c.GetHeader("Referer")
	origin := c.GetHeader("Origin")
	for _, allow := range a.cfg.Playback.Whitelist {
		for _, v := range []string{origin, ref} {
			if v != "" && (v == allow || hasPrefixFold(v, allow)) {
				return true
			}
		}
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return equalFoldASCII(s[:len(prefix)], prefix)
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// verifyPlay validates a playback token (public, for edge/validator use).
func (a *App) verifyPlay(c *gin.Context) {
	stream := c.Query("stream")
	tok := c.Query("token")
	exp, _ := strconv.ParseInt(c.Query("exp"), 10, 64)
	ok(c, gin.H{"valid": playauth.Verify(a.cfg.JWTSecret, stream, tok, exp)})
}

// zlmOnPlay is a ZLMediaKit on_play hook. It denies playback of a stream unless
// a valid token is presented when playback auth is enabled.
func (a *App) zlmOnPlay(c *gin.Context) {
	if !a.cfg.Playback.Auth {
		ok(c, gin.H{"code": 0, "msg": "ok"})
		return
	}
	stream := c.Query("stream")
	if stream == "" {
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err == nil {
			if p, ok := body["params"].(string); ok {
				stream = p
			}
		}
	}
	if stream == "" {
		stream = c.Query("stream")
	}
	tok := c.Query("token")
	exp, _ := strconv.ParseInt(c.Query("exp"), 10, 64)
	if playauth.Verify(a.cfg.JWTSecret, stream, tok, exp) {
		ok(c, gin.H{"code": 0, "msg": "ok"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": -1, "msg": "播放鉴权失败或已过期"})
}
