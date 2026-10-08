package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
)

func (a *App) listGBDevices(c *gin.Context) {
	var devices []model.GBDevice
	a.db.Order("id DESC").Find(&devices)
	ok(c, devices)
}

// gbConfig exposes the platform SIP parameters needed to configure devices.
func (a *App) gbConfig(c *gin.Context) {
	ok(c, gin.H{
		"enabled":    a.settingBool("gb_enabled", a.cfg.GB.Enabled),
		"running":    a.gb.Running(),
		"id":         a.cfg.GB.ID,
		"realm":      a.cfg.GB.Realm,
		"listen":     a.cfg.GB.Listen,
		"password":   a.cfg.GB.Password,
		"rtpIp":      a.cfg.GB.RTPIP,
		"registered": a.gb.RegisteredCount(),
	})
}

func (a *App) refreshGBCatalog(c *gin.Context) {
	if !a.gb.Running() {
		fail(c, http.StatusBadRequest, "GB28181 signaling is disabled")
		return
	}
	gbid := c.Param("gbid")
	if gbid == "" {
		fail(c, http.StatusBadRequest, "device id required")
		return
	}
	if err := a.gb.RefreshCatalog(gbid); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"deviceId": gbid, "refreshed": true})
}

// queryGBMobilePosition asks a GB28181 mobile device to report its position.
func (a *App) queryGBMobilePosition(c *gin.Context) {
	if !a.gb.Running() {
		fail(c, http.StatusBadRequest, "GB28181 signaling is disabled")
		return
	}
	gbid := c.Param("gbid")
	if gbid == "" {
		fail(c, http.StatusBadRequest, "device id required")
		return
	}
	if err := a.gb.QueryMobilePosition(gbid); err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, gin.H{"deviceId": gbid, "requested": true})
}

// ---- GB28181 cascade (upper platforms) ----

func (a *App) listCascades(c *gin.Context) {
	var items []model.GBCascade
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createCascade(c *gin.Context) {
	var item model.GBCascade
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if item.TargetID == "" || item.TargetIP == "" {
		fail(c, http.StatusBadRequest, "targetId and targetIp are required")
		return
	}
	if item.LocalID == "" {
		item.LocalID = a.cfg.GB.ID
	}
	if item.TargetPort == 0 {
		item.TargetPort = 5060
	}
	if err := a.db.Create(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	if a.gb != nil && item.Enabled {
		a.gb.EnsureCascade(item)
	}
	ok(c, item)
}

func (a *App) deleteCascade(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if a.gb != nil {
		a.gb.RemoveCascade(id)
	}
	a.db.Delete(&model.GBCascade{}, id)
	ok(c, gin.H{"id": id})
}

func (a *App) refreshCascade(c *gin.Context) {
	if !a.gb.Running() {
		fail(c, http.StatusBadRequest, "GB28181 signaling is disabled")
		return
	}
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var item model.GBCascade
	if err := a.db.First(&item, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	a.gb.EnsureCascade(item)
	ok(c, gin.H{"id": id, "refreshed": true})
}

// ---- GB white list ----

func (a *App) listWhiteList(c *gin.Context) {
	var items []model.GBWhiteList
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createWhiteList(c *gin.Context) {
	var item model.GBWhiteList
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if item.DeviceID == "" {
		fail(c, http.StatusBadRequest, "deviceId is required")
		return
	}
	if item.Protocol == "" {
		item.Protocol = "GB28181"
	}
	if err := a.db.Create(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, item)
}

func (a *App) deleteWhiteList(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.GBWhiteList{}, id)
	ok(c, gin.H{"id": id})
}

// ---- GB black list ----

func (a *App) listBlackList(c *gin.Context) {
	var items []model.GBBlackList
	a.db.Order("id DESC").Find(&items)
	ok(c, items)
}

func (a *App) createBlackList(c *gin.Context) {
	var item model.GBBlackList
	if err := c.ShouldBindJSON(&item); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if item.DeviceID == "" && item.UA == "" && item.IP == "" && item.Port == 0 {
		fail(c, http.StatusBadRequest, "至少填写一个匹配条件")
		return
	}
	if item.Protocol == "" {
		item.Protocol = "GB28181"
	}
	if err := a.db.Create(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, item)
}

func (a *App) deleteBlackList(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Delete(&model.GBBlackList{}, id)
	ok(c, gin.H{"id": id})
}
