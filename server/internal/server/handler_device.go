package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/easyavr/easyavr/internal/device"
	"github.com/easyavr/easyavr/internal/model"
)

type deviceRequest struct {
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	AccessMode   string `json:"accessMode"`
	Manufacturer string `json:"manufacturer"`
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	GroupID      uint   `json:"groupId"`
	NodeID       string `json:"nodeId"`
}

func (a *App) listDevices(c *gin.Context) {
	page, pageSize := pagination(c)
	tx := a.db.Model(&model.Device{})
	if kw := c.Query("keyword"); kw != "" {
		like := "%" + kw + "%"
		tx = tx.Where("name LIKE ? OR ip LIKE ?", like, like)
	}
	if p := c.Query("protocol"); p != "" {
		tx = tx.Where("protocol = ?", p)
	}
	if s := c.Query("status"); s != "" {
		tx = tx.Where("status = ?", s)
	}
	var total int64
	tx.Count(&total)
	var devices []model.Device
	if err := tx.Preload("Channels").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&devices).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"items": devices, "total": total, "page": page, "pageSize": pageSize})
}

func (a *App) createDevice(c *gin.Context) {
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.Name == "" || req.Protocol == "" {
		fail(c, http.StatusBadRequest, "name and protocol are required")
		return
	}
	if req.AccessMode == "" {
		if strings.EqualFold(req.Protocol, "gb28181") || strings.EqualFold(req.Protocol, "ehome") {
			req.AccessMode = "register"
		} else if strings.EqualFold(req.Protocol, "rtmp_push") {
			req.AccessMode = "push"
		} else {
			req.AccessMode = "pull"
		}
	}
	dev := model.Device{
		Name: req.Name, Protocol: req.Protocol, AccessMode: req.AccessMode,
		Manufacturer: req.Manufacturer, IP: req.IP, Port: req.Port,
		Username: req.Username, Password: req.Password, GroupID: req.GroupID,
		Status: "offline",
	}
	if req.NodeID != "" {
		dev.NodeID = req.NodeID
	} else if a.cluster != nil {
		dev.NodeID = a.cluster.AssignNode()
	}
	if err := a.db.Create(&dev).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	// Auto-create a main channel so the device is immediately streamable.
	ch := model.Channel{
		DeviceID: dev.ID, Name: req.Name + "-main", StreamType: "main",
		StreamKey: newStreamKey(), Enabled: true,
	}
	a.db.Create(&ch)
	a.db.Preload("Channels").First(&dev, dev.ID)
	ok(c, dev)
}

func (a *App) getDevice(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var dev model.Device
	if err := a.db.Preload("Channels").First(&dev, id).Error; err != nil {
		fail(c, http.StatusNotFound, "device not found")
		return
	}
	ok(c, dev)
}

func (a *App) updateDevice(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var dev model.Device
	if err := a.db.First(&dev, id).Error; err != nil {
		fail(c, http.StatusNotFound, "device not found")
		return
	}
	var req deviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Protocol != "" {
		updates["protocol"] = req.Protocol
	}
	if req.AccessMode != "" {
		updates["access_mode"] = req.AccessMode
	}
	if req.Manufacturer != "" {
		updates["manufacturer"] = req.Manufacturer
	}
	if req.IP != "" {
		updates["ip"] = req.IP
	}
	if req.Port != 0 {
		updates["port"] = req.Port
	}
	if req.Username != "" {
		updates["username"] = req.Username
	}
	if req.Password != "" {
		updates["password"] = req.Password
	}
	if req.NodeID != "" {
		updates["node_id"] = req.NodeID
	}
	if err := a.db.Model(&dev).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	a.db.Preload("Channels").First(&dev, id)
	ok(c, dev)
}

func (a *App) deleteDevice(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.db.Where("device_id = ?", id).Delete(&model.Channel{})
	if err := a.db.Delete(&model.Device{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

type channelRequest struct {
	Name             string `json:"name"`
	StreamType       string `json:"streamType"`
	SourceURL        string `json:"sourceUrl"`
	Description      string `json:"description"`
	Enabled          *bool  `json:"enabled"`
	SnapshotEnabled  *bool  `json:"snapshotEnabled"`
	SnapshotInterval *int   `json:"snapshotInterval"`
}

func (a *App) createChannel(c *gin.Context) {
	deviceID, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var dev model.Device
	if err := a.db.First(&dev, deviceID).Error; err != nil {
		fail(c, http.StatusNotFound, "device not found")
		return
	}
	var req channelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	if req.StreamType == "" {
		req.StreamType = "main"
	}
	ch := model.Channel{
		DeviceID: deviceID, Name: req.Name, StreamType: req.StreamType,
		SourceURL: req.SourceURL, StreamKey: newStreamKey(),
		Enabled: true, Description: req.Description,
	}
	if err := a.db.Create(&ch).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, ch)
}

func (a *App) listDeviceChannels(c *gin.Context) {
	deviceID, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var channels []model.Channel
	a.db.Where("device_id = ?", deviceID).Order("id").Find(&channels)
	ok(c, channels)
}

// listChannels returns all channels, optionally filtered by keyword/online,
// used by the live view and playback pickers.
func (a *App) listChannels(c *gin.Context) {
	var channels []model.Channel
	tx := a.db.Order("id DESC")
	if kw := c.Query("keyword"); kw != "" {
		tx = tx.Where("name LIKE ?", "%"+kw+"%")
	}
	if o := c.Query("online"); o == "true" {
		tx = tx.Where("online = ?", true)
	}
	tx.Find(&channels)
	ok(c, channels)
}

func (a *App) updateChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	var req channelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.StreamType != "" {
		updates["stream_type"] = req.StreamType
	}
	if req.SourceURL != "" {
		updates["source_url"] = req.SourceURL
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.SnapshotEnabled != nil {
		updates["snapshot_enabled"] = *req.SnapshotEnabled
	}
	if req.SnapshotInterval != nil {
		updates["snapshot_interval"] = *req.SnapshotInterval
	}
	a.db.Model(&ch).Updates(updates)
	a.db.First(&ch, id)
	ok(c, ch)
}

func (a *App) deleteChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	a.devices.StopChannel(id)
	if err := a.db.Delete(&model.Channel{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (a *App) startChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	var dev model.Device
	a.db.First(&dev, ch.DeviceID)

	// GB28181 registered devices are started by sending an INVITE.
	if a.gb != nil && strings.EqualFold(dev.Protocol, "gb28181") && strings.EqualFold(dev.AccessMode, "register") {
		urls, err := a.gb.InvitePlay(c.Request.Context(), id)
		if err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
		a.db.First(&ch, id)
		ok(c, gin.H{"channel": ch, "detail": map[string]string{"mode": "gb28181-invite"}, "playUrls": urls})
		return
	}

	result, err := a.devices.StartChannel(id)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	a.db.First(&ch, id)
	ok(c, gin.H{"channel": ch, "detail": result, "playUrls": a.zlm.PlayURLs(ch.StreamKey)})
}

func (a *App) stopChannel(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if a.gb != nil {
		var ch model.Channel
		if a.db.First(&ch, id).Error == nil {
			var dev model.Device
			a.db.First(&dev, ch.DeviceID)
			if strings.EqualFold(dev.Protocol, "gb28181") && strings.EqualFold(dev.AccessMode, "register") {
				_ = a.gb.StopPlay(id)
				ok(c, gin.H{"id": id})
				return
			}
		}
	}
	if err := a.devices.StopChannel(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

func (a *App) channelPlayURLs(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	var dev model.Device
	a.db.First(&dev, ch.DeviceID)
	ok(c, gin.H{
		"channelId": ch.ID,
		"streamKey": ch.StreamKey,
		"online":    ch.Online,
		"playUrls":  a.zlm.PlayURLs(ch.StreamKey),
		"pushUrl":   a.zlm.PushURL(ch.StreamKey),
		"sourceUrl": device.BuildSourceURL(dev, ch),
	})
}

func newStreamKey() string {
	return "ch_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
}
