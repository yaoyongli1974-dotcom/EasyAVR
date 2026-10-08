package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/isapi"
	"github.com/easyavr/easyavr/internal/model"
)

type isapiRequest struct {
	BaseURL  string `json:"baseUrl"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r isapiRequest) base() (string, error) {
	if r.BaseURL != "" {
		return r.BaseURL, nil
	}
	if r.Host == "" {
		return "", fmt.Errorf("baseUrl 或 host 必填")
	}
	port := r.Port
	if port == 0 {
		port = 80
	}
	return "http://" + net.JoinHostPort(r.Host, strconv.Itoa(port)), nil
}

// isapiProbe connects to a Hikvision device via ISAPI and returns device info
// and streaming channels (with RTSP URLs) without persisting anything.
func (a *App) isapiProbe(c *gin.Context) {
	var req isapiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	base, err := req.base()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	res, err := isapi.NewClient(base, req.Username, req.Password).Probe(ctx)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, res)
}

type isapiImportRequest struct {
	isapiRequest
	Name   string `json:"name"`
	NodeID string `json:"nodeId"`
}

// isapiImport probes a device and creates it with one channel per ISAPI
// streaming channel, using each channel's RTSP URL as the source.
func (a *App) isapiImport(c *gin.Context) {
	var req isapiImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	base, err := req.base()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	res, err := isapi.NewClient(base, req.Username, req.Password).Probe(ctx)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}

	host := res.Host
	httpPort := portOfBase(base)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(res.DeviceInfo.Name)
	}
	if name == "" {
		name = host
	}
	dev := model.Device{
		Name: name, Protocol: "rtsp", AccessMode: "pull", Manufacturer: "hikvision",
		IP: host, Port: httpPort, Username: req.Username, Password: req.Password, Status: "offline",
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
	for _, ch := range res.Channels {
		chName := strings.TrimSpace(ch.Name)
		if chName == "" {
			chName = "channel-" + ch.ID
		}
		a.db.Create(&model.Channel{
			DeviceID: dev.ID, Name: chName, StreamType: ch.StreamType,
			SourceURL: ch.RTSPURL, StreamKey: newStreamKey(), Enabled: true,
		})
	}
	a.db.Preload("Channels").First(&dev, dev.ID)
	ok(c, gin.H{"device": dev, "probe": res})
}

func portOfBase(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 80
	}
	if _, portStr, err := net.SplitHostPort(u.Host); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil {
			return p
		}
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}
