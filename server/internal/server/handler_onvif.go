package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/onvif"
)

type onvifRequest struct {
	XAddr    string `json:"xaddr"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r onvifRequest) endpoint() (string, error) {
	if r.XAddr != "" {
		return r.XAddr, nil
	}
	if r.Host == "" {
		return "", fmt.Errorf("xaddr 或 host 必填")
	}
	port := r.Port
	if port == 0 {
		port = 80
	}
	return fmt.Sprintf("http://%s/onvif/device_service", net.JoinHostPort(r.Host, strconv.Itoa(port))), nil
}

// onvifProbe connects to an ONVIF device and returns device info, media
// profiles and their RTSP URIs without persisting anything.
func (a *App) onvifProbe(c *gin.Context) {
	var req onvifRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	xaddr, err := req.endpoint()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	res, err := onvif.NewClient(xaddr, req.Username, req.Password).Probe(ctx)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}
	ok(c, res)
}

type onvifImportRequest struct {
	onvifRequest
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	NodeID       string `json:"nodeId"`
}

// onvifImport probes a device and creates it with one channel per media
// profile, using each profile's RTSP URI as the channel source.
func (a *App) onvifImport(c *gin.Context) {
	var req onvifImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	xaddr, err := req.endpoint()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	res, err := onvif.NewClient(xaddr, req.Username, req.Password).Probe(ctx)
	if err != nil {
		fail(c, http.StatusBadGateway, err.Error())
		return
	}

	host, port := onvif.HostPort(xaddr)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(res.DeviceInfo.Model)
	}
	if name == "" {
		name = host
	}
	manufacturer := req.Manufacturer
	if manufacturer == "" {
		manufacturer = normalizeManufacturer(res.DeviceInfo.Manufacturer)
	}

	dev := model.Device{
		Name: name, Protocol: "onvif", AccessMode: "pull", Manufacturer: manufacturer,
		IP: host, Port: port, Username: req.Username, Password: req.Password, Status: "offline",
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
	for i, p := range res.Profiles {
		chName := strings.TrimSpace(p.Name)
		if chName == "" {
			chName = fmt.Sprintf("profile-%d", i+1)
		}
		a.db.Create(&model.Channel{
			DeviceID: dev.ID, Name: chName, StreamType: profileStreamType(p.Name),
			SourceURL: p.StreamURI, StreamKey: newStreamKey(), Enabled: true,
		})
	}
	if len(res.Profiles) == 0 {
		a.db.Create(&model.Channel{
			DeviceID: dev.ID, Name: name + "-main", StreamType: "main",
			StreamKey: newStreamKey(), Enabled: true,
		})
	}
	a.db.Preload("Channels").First(&dev, dev.ID)
	ok(c, gin.H{"device": dev, "probe": res})
}

func profileStreamType(name string) string {
	if strings.Contains(strings.ToLower(name), "sub") {
		return "sub"
	}
	return "main"
}

func normalizeManufacturer(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(l, "hik"):
		return "hikvision"
	case strings.Contains(l, "dahua") || strings.Contains(l, "dav"):
		return "dahua"
	case strings.Contains(l, "uniview") || strings.Contains(l, "unv"):
		return "uniview"
	case l == "":
		return "other"
	default:
		return s
	}
}
