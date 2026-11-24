package server

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

func (a *App) listGBDevices(c *gin.Context) {
	var devices []model.GBDevice
	a.db.Order("id DESC").Find(&devices)
	ok(c, devices)
}

// gbPasswordConfigured reports whether a non-empty SIP digest password is in
// effect. The password itself is never exposed over the API.
func (a *App) gbPasswordConfigured() bool {
	return strings.TrimSpace(a.gb.Password()) != ""
}

// gbConfig exposes the platform SIP parameters needed to configure devices.
func (a *App) gbConfig(c *gin.Context) {
	ok(c, gin.H{
		"enabled":            a.settingBool("gb_enabled", a.cfg.GB.Enabled),
		"running":            a.gb.Running(),
		"id":                 a.cfg.GB.ID,
		"realm":              a.cfg.GB.Realm,
		"listen":             a.cfg.GB.Listen,
		"passwordConfigured": a.gbPasswordConfigured(),
		"rtpIp":              a.cfg.GB.RTPIP,
		"registered":         a.gb.RegisteredCount(),
	})
}

// updateGBPassword rotates the SIP digest password for subsequent REGISTERs
// and persists it encrypted so the change survives a service restart.
func (a *App) updateGBPassword(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "请求体格式错误")
		return
	}
	pw := strings.TrimSpace(req.Password)
	if pw == "" {
		fail(c, http.StatusBadRequest, "密码不能为空")
		return
	}
	if utf8.RuneCountInString(pw) > 128 {
		fail(c, http.StatusBadRequest, "密码长度不能超过 128 个字符")
		return
	}
	if err := store.SaveGBPassword(a.db, pw); err != nil {
		fail(c, http.StatusInternalServerError, "密钥保存失败")
		return
	}
	a.gb.SetPassword(pw)
	ok(c, gin.H{
		"updated": true,
		"note":    "密码已即时生效并加密持久化，服务重启后继续有效",
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

// gbCascadeReq is the write payload for a cascade. password is write-only: an
// empty value keeps the stored password, clearPassword removes it.
type gbCascadeReq struct {
	Name          string `json:"name"`
	TargetID      string `json:"targetId"`
	LocalID       string `json:"localId"`
	TargetIP      string `json:"targetIp"`
	TargetPort    int    `json:"targetPort"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clearPassword"`
	Transport     string `json:"transport"`
	Enabled       bool   `json:"enabled"`
}

type gbCascadeView struct {
	model.GBCascade
	PasswordConfigured bool `json:"passwordConfigured"`
}

func cascadeView(item model.GBCascade) gbCascadeView {
	return gbCascadeView{GBCascade: item, PasswordConfigured: item.Password != ""}
}

func (a *App) listCascades(c *gin.Context) {
	var items []model.GBCascade
	a.db.Order("id DESC").Find(&items)
	views := make([]gbCascadeView, 0, len(items))
	for _, item := range items {
		views = append(views, cascadeView(item))
	}
	ok(c, views)
}

func (a *App) createCascade(c *gin.Context) {
	var req gbCascadeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.TargetID == "" || req.TargetIP == "" {
		fail(c, http.StatusBadRequest, "targetId and targetIp are required")
		return
	}
	item := model.GBCascade{
		Name: req.Name, TargetID: req.TargetID, LocalID: req.LocalID,
		TargetIP: req.TargetIP, TargetPort: req.TargetPort, Password: req.Password,
		Transport: req.Transport, Enabled: req.Enabled,
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
	// Reload so outbound registration uses the decrypted password.
	var fresh model.GBCascade
	if err := a.db.First(&fresh, item.ID).Error; err != nil {
		fresh = item
	} else if a.gb != nil && fresh.Enabled {
		a.gb.EnsureCascade(fresh)
	}
	ok(c, cascadeView(fresh))
}

func (a *App) updateCascade(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var item model.GBCascade
	if err := a.db.First(&item, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	var req gbCascadeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.Name != "" {
		item.Name = req.Name
	}
	if req.TargetID != "" {
		item.TargetID = req.TargetID
	}
	if req.LocalID != "" {
		item.LocalID = req.LocalID
	}
	if req.TargetIP != "" {
		item.TargetIP = req.TargetIP
	}
	if req.TargetPort != 0 {
		item.TargetPort = req.TargetPort
	}
	if req.Transport != "" {
		item.Transport = req.Transport
	}
	switch {
	case req.Password != "":
		item.Password = req.Password
	case req.ClearPassword:
		item.Password = ""
	}
	item.Enabled = req.Enabled
	if err := a.db.Save(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var fresh model.GBCascade
	if err := a.db.First(&fresh, id).Error; err != nil {
		fail(c, http.StatusNotFound, "cascade not found")
		return
	}
	if a.gb != nil {
		if fresh.Enabled {
			a.gb.EnsureCascade(fresh)
		} else {
			a.gb.RemoveCascade(id)
		}
	}
	ok(c, cascadeView(fresh))
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

type gbWhiteListReq struct {
	DeviceID      string `json:"deviceId"`
	IP            string `json:"ip"`
	Port          int    `json:"port"`
	Protocol      string `json:"protocol"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clearPassword"`
	Enabled       bool   `json:"enabled"`
	Description   string `json:"description"`
}

type gbWhiteListView struct {
	model.GBWhiteList
	PasswordConfigured bool `json:"passwordConfigured"`
}

func whiteListView(item model.GBWhiteList) gbWhiteListView {
	return gbWhiteListView{GBWhiteList: item, PasswordConfigured: item.Password != ""}
}

func (a *App) listWhiteList(c *gin.Context) {
	var items []model.GBWhiteList
	a.db.Order("id DESC").Find(&items)
	views := make([]gbWhiteListView, 0, len(items))
	for _, item := range items {
		views = append(views, whiteListView(item))
	}
	ok(c, views)
}

func (a *App) createWhiteList(c *gin.Context) {
	var req gbWhiteListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.DeviceID == "" {
		fail(c, http.StatusBadRequest, "deviceId is required")
		return
	}
	item := model.GBWhiteList{
		DeviceID: req.DeviceID, IP: req.IP, Port: req.Port, Protocol: req.Protocol,
		Password: req.Password, Enabled: req.Enabled, Description: req.Description,
	}
	if item.Protocol == "" {
		item.Protocol = "GB28181"
	}
	if err := a.db.Create(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, whiteListView(item))
}

func (a *App) updateWhiteList(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var item model.GBWhiteList
	if err := a.db.First(&item, id).Error; err != nil {
		fail(c, http.StatusNotFound, "whitelist entry not found")
		return
	}
	var req gbWhiteListReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.DeviceID != "" {
		item.DeviceID = req.DeviceID
	}
	if req.IP != "" {
		item.IP = req.IP
	}
	if req.Port != 0 {
		item.Port = req.Port
	}
	if req.Protocol != "" {
		item.Protocol = req.Protocol
	}
	if req.Description != "" {
		item.Description = req.Description
	}
	switch {
	case req.Password != "":
		item.Password = req.Password
	case req.ClearPassword:
		item.Password = ""
	}
	item.Enabled = req.Enabled
	if err := a.db.Save(&item).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	var fresh model.GBWhiteList
	if err := a.db.First(&fresh, id).Error; err != nil {
		fail(c, http.StatusNotFound, "whitelist entry not found")
		return
	}
	ok(c, whiteListView(fresh))
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
