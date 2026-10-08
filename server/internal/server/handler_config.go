package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

// ---- persisted platform settings (手册 3.7 平台配置) ----

func (a *App) settingBool(key string, def bool) bool {
	var s model.PlatformSetting
	if err := a.db.Where("key = ?", key).First(&s).Error; err == nil {
		return s.Value == "true" || s.Value == "1"
	}
	return def
}

func (a *App) setSetting(key string, v bool) {
	val := strconv.FormatBool(v)
	var s model.PlatformSetting
	if err := a.db.Where("key = ?", key).First(&s).Error; err == nil {
		a.db.Model(&s).Update("value", val)
		return
	}
	a.db.Create(&model.PlatformSetting{Key: key, Value: val})
}

func (a *App) settingString(key, def string) string {
	var s model.PlatformSetting
	if err := a.db.Where("key = ?", key).First(&s).Error; err == nil {
		return s.Value
	}
	return def
}

func (a *App) setString(key, val string) {
	var s model.PlatformSetting
	if err := a.db.Where("key = ?", key).First(&s).Error; err == nil {
		a.db.Model(&s).Update("value", val)
		return
	}
	a.db.Create(&model.PlatformSetting{Key: key, Value: val})
}

// gb35114Listen returns the effective GB35114 secure SIP listen address
// (runtime setting overriding the env default).
func (a *App) gb35114Listen() string {
	return a.settingString("gb35114_sip_listen", a.cfg.GB35114.SIPListen)
}

func (a *App) platformConfig(c *gin.Context) {
	ok(c, gin.H{
		"gb": gin.H{
			"enabled":  a.settingBool("gb_enabled", a.cfg.GB.Enabled),
			"running":  a.gb.Running(),
			"listen":   a.cfg.GB.Listen,
			"id":       a.cfg.GB.ID,
			"realm":    a.cfg.GB.Realm,
			"password": a.cfg.GB.Password,
			"rtpIp":    a.cfg.GB.RTPIP,
		},
		"ehome": gin.H{
			"enabled":   a.settingBool("ehome_enabled", a.cfg.EHOME.Enabled),
			"running":   a.ehome.Running(),
			"cmsListen": a.cfg.EHOME.CMSListen,
			"smsListen": a.cfg.EHOME.SMSListen,
			"publicIp":  a.cfg.EHOME.PublicIP,
		},
		"gb35114": gin.H{
			"enabled":       a.settingBool("gb35114_enabled", a.cfg.GB35114.Enabled),
			"running":       a.gb.TLSAddr() != "",
			"sipListen":     a.gb35114Listen(),
			"configured":    a.gb35114Listen() != "",
			"requireClient": a.cfg.GB35114.SIPRequireClient,
			"certDir":       a.cfg.GB35114.CertDir,
		},
		"playback": gin.H{
			"auth":        a.cfg.Playback.Auth,
			"tokenTtlMin": a.cfg.Playback.TokenTTLMin,
			"whitelist":   a.cfg.Playback.Whitelist,
		},
		"monitorSec": a.cfg.MonitorSec,
	})
}

type platformConfigRequest struct {
	GBEnabled        *bool   `json:"gbEnabled"`
	EHomeEnabled     *bool   `json:"ehomeEnabled"`
	GB35114Enabled   *bool   `json:"gb35114Enabled"`
	GB35114SipListen *string `json:"gb35114SipListen"`
}

func (a *App) updatePlatformConfig(c *gin.Context) {
	var req platformConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	warnings := []string{}

	if req.GBEnabled != nil {
		a.setSetting("gb_enabled", *req.GBEnabled)
		if *req.GBEnabled {
			if err := a.gb.Start(); err != nil {
				warnings = append(warnings, "GB28181 启动失败: "+err.Error())
			}
		} else {
			a.gb.Stop()
		}
	}
	if req.EHomeEnabled != nil {
		a.setSetting("ehome_enabled", *req.EHomeEnabled)
		if *req.EHomeEnabled {
			if err := a.ehome.Start(); err != nil {
				warnings = append(warnings, "EHOME 启动失败: "+err.Error())
			}
		} else {
			a.ehome.Stop()
		}
	}

	listenChanged := false
	if req.GB35114SipListen != nil {
		a.setString("gb35114_sip_listen", strings.TrimSpace(*req.GB35114SipListen))
		listenChanged = true
	}
	if req.GB35114Enabled != nil {
		a.setSetting("gb35114_enabled", *req.GB35114Enabled)
	}
	if req.GB35114Enabled != nil || listenChanged {
		enabled := a.settingBool("gb35114_enabled", a.cfg.GB35114.Enabled)
		a.gb.StopTLS()
		switch {
		case !enabled:
			// disabled: nothing to start
		case a.gb35114Listen() == "":
			warnings = append(warnings, "未配置 GB35114 安全 SIP 监听地址（如 :5061），安全 SIP 监听未启动")
		default:
			if err := a.startSecureSIP(a.gb35114Listen()); err != nil {
				warnings = append(warnings, "GB35114 安全 SIP 启动失败: "+err.Error())
			}
		}
	}

	resp := gin.H{"warnings": warnings}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
}
