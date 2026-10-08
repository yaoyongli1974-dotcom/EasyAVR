package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/isapi"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/ptz"
)

// ---- PTZ (云台控制) ----

// ptzKind classifies how a device's PTZ is driven.
func ptzKind(dev model.Device) string {
	switch strings.ToLower(strings.TrimSpace(dev.Protocol)) {
	case "gb28181":
		return "gb28181"
	case "hikvision", "isapi":
		return "isapi"
	case "onvif":
		return "onvif"
	default:
		return ""
	}
}

func (a *App) loadChannelDevice(c *gin.Context) (model.Channel, model.Device, bool) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return model.Channel{}, model.Device{}, false
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return model.Channel{}, model.Device{}, false
	}
	var dev model.Device
	if err := a.db.First(&dev, ch.DeviceID).Error; err != nil {
		fail(c, http.StatusNotFound, "device not found")
		return model.Channel{}, model.Device{}, false
	}
	return ch, dev, true
}

func isapiBase(dev model.Device) string {
	port := dev.Port
	if port == 0 {
		port = 80
	}
	host := dev.IP
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + strconv.Itoa(port)
}

// isapiPTZChannel maps a streaming channel id (e.g. 101) to the ISAPI PTZ
// channel (camera) number (1). Parsed from the RTSP source URL when present.
func isapiPTZChannel(ch model.Channel) int {
	if i := strings.Index(ch.SourceURL, "/Streaming/Channels/"); i >= 0 {
		rest := ch.SourceURL[i+len("/Streaming/Channels/"):]
		rest = strings.TrimRight(rest, "/")
		if n, err := strconv.Atoi(rest); err == nil && n > 0 {
			if n >= 100 {
				return n / 100
			}
			return n
		}
	}
	return 1
}

// channelPTZ performs one PTZ action on a channel.
func (a *App) channelPTZ(c *gin.Context) {
	ch, dev, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	var req struct {
		Cmd   string `json:"cmd"`
		Speed int    `json:"speed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !ptz.Supported(req.Cmd) {
		fail(c, http.StatusBadRequest, "invalid cmd (up/down/left/right/up_left/.../zoom_in/zoom_out/stop)")
		return
	}

	switch ptzKind(dev) {
	case "gb28181":
		if !a.gb.Running() {
			fail(c, http.StatusBadRequest, "GB28181 signaling is disabled")
			return
		}
		_, hex, err := ptz.BuildGB28181Cmd(req.Cmd, req.Speed)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.gb.SendPTZ(ch.GBDeviceID, ch.GBChannelID, hex); err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
	case "isapi":
		pan, tilt, zoom, err := ptz.PanTiltZoom(req.Cmd, req.Speed)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		client := isapi.NewClient(isapiBase(dev), dev.Username, dev.Password)
		if err := client.PTZContinuous(ctx, isapiPTZChannel(ch), pan, tilt, zoom); err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
	default:
		fail(c, http.StatusBadRequest, "该设备协议暂不支持云台控制")
		return
	}
	ok(c, gin.H{"channelId": ch.ID, "cmd": req.Cmd})
}

func (a *App) listPTZPresets(c *gin.Context) {
	ch, _, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	var presets []model.PTZPreset
	a.db.Where("channel_id = ?", ch.ID).Order("preset ASC").Find(&presets)
	ok(c, presets)
}

func (a *App) savePTZPreset(c *gin.Context) {
	ch, dev, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	var req struct {
		Preset int    `json:"preset"`
		Name   string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Preset < 1 || req.Preset > 255 {
		fail(c, http.StatusBadRequest, "preset (1-255) is required")
		return
	}
	var existing model.PTZPreset
	if err := a.db.Where("channel_id = ? AND preset = ?", ch.ID, req.Preset).First(&existing).Error; err == nil {
		a.db.Model(&existing).Update("name", req.Name)
	} else {
		a.db.Create(&model.PTZPreset{ChannelID: ch.ID, Preset: req.Preset, Name: req.Name})
	}

	warning := ""
	switch ptzKind(dev) {
	case "gb28181":
		if a.gb != nil {
			if _, hex, err := ptz.BuildPresetCmd(ptz.PresetSet, req.Preset); err == nil {
				if err := a.gb.SendPTZ(ch.GBDeviceID, ch.GBChannelID, hex); err != nil {
					warning = err.Error()
				}
			}
		}
	case "isapi":
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		client := isapi.NewClient(isapiBase(dev), dev.Username, dev.Password)
		if err := client.PTZSetPreset(ctx, isapiPTZChannel(ch), req.Preset, req.Name); err != nil {
			warning = err.Error()
		}
	}
	ok(c, gin.H{"channelId": ch.ID, "preset": req.Preset, "name": req.Name, "warning": warning})
}

func (a *App) gotoPTZPreset(c *gin.Context) {
	ch, dev, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	preset := parsePresetParam(c)
	if preset == 0 {
		return
	}
	switch ptzKind(dev) {
	case "gb28181":
		if !a.gb.Running() {
			fail(c, http.StatusBadRequest, "GB28181 signaling is disabled")
			return
		}
		_, hex, err := ptz.BuildPresetCmd(ptz.PresetGoto, preset)
		if err != nil {
			fail(c, http.StatusBadRequest, err.Error())
			return
		}
		if err := a.gb.SendPTZ(ch.GBDeviceID, ch.GBChannelID, hex); err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
	case "isapi":
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		client := isapi.NewClient(isapiBase(dev), dev.Username, dev.Password)
		if err := client.PTZGotoPreset(ctx, isapiPTZChannel(ch), preset); err != nil {
			fail(c, http.StatusBadGateway, err.Error())
			return
		}
	default:
		fail(c, http.StatusBadRequest, "该设备协议暂不支持云台预置位")
		return
	}
	ok(c, gin.H{"channelId": ch.ID, "preset": preset})
}

func (a *App) deletePTZPreset(c *gin.Context) {
	ch, dev, found := a.loadChannelDevice(c)
	if !found {
		return
	}
	preset := parsePresetParam(c)
	if preset == 0 {
		return
	}
	if ptzKind(dev) == "isapi" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()
		client := isapi.NewClient(isapiBase(dev), dev.Username, dev.Password)
		_ = client.PTZDeletePreset(ctx, isapiPTZChannel(ch), preset)
	}
	a.db.Where("channel_id = ? AND preset = ?", ch.ID, preset).Delete(&model.PTZPreset{})
	ok(c, gin.H{"channelId": ch.ID, "preset": preset})
}

func parsePresetParam(c *gin.Context) int {
	n, err := strconv.Atoi(c.Param("preset"))
	if err != nil || n < 1 || n > 255 {
		fail(c, http.StatusBadRequest, "invalid preset (1-255)")
		return 0
	}
	return n
}
