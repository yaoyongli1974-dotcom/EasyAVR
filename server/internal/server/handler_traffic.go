package server

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// ---- channel traffic & status log (手册 3.2.3 流量/消息) ----

// syncTraffic samples the media kernel and records per-channel online state and
// cumulative ingress bytes, emitting a StatusLog on every transition.
func (a *App) syncTraffic() (int, int) {
	streams, err := a.zlm.MediaList()
	if err != nil {
		return 0, -1
	}
	byStream := make(map[string]video.MediaStream, len(streams))
	for _, s := range streams {
		byStream[s.Stream] = s
	}
	var channels []model.Channel
	a.db.Find(&channels)
	updated, transitions := 0, 0
	now := time.Now()
	for _, ch := range channels {
		s, live := byStream[ch.StreamKey]
		var tr model.ChannelTraffic
		if err := a.db.Where("channel_id = ?", ch.ID).First(&tr).Error; err != nil {
			tr = model.ChannelTraffic{ChannelID: ch.ID, StreamKey: ch.StreamKey}
		}
		tr.Online = live
		tr.LastSample = now
		if live {
			if s.Bytes > 0 {
				tr.Bytes = s.Bytes
			}
		}
		if tr.ID == 0 {
			a.db.Create(&tr)
		} else {
			a.db.Save(&tr)
		}
		updated++
		if ch.Online != live {
			a.db.Model(&model.Channel{}).Where("id = ?", ch.ID).Update("online", live)
			a.db.Create(&model.StatusLog{
				ChannelID: ch.ID, DeviceID: ch.DeviceID, Target: "channel",
				Online: live, Source: "poll", LoggedAt: now,
				Message: map[bool]string{true: "通道上线", false: "通道离线"}[live],
			})
			transitions++
		}
	}
	return updated, transitions
}

func (a *App) collectTraffic(c *gin.Context) {
	updated, transitions := a.syncTraffic()
	if transitions < 0 {
		fail(c, http.StatusBadGateway, "媒体内核不可用")
		return
	}
	ok(c, gin.H{"channels": updated, "transitions": transitions})
}

func (a *App) getChannelTraffic(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var ch model.Channel
	if err := a.db.First(&ch, id).Error; err != nil {
		fail(c, http.StatusNotFound, "channel not found")
		return
	}
	var tr model.ChannelTraffic
	a.db.Where("channel_id = ?", id).First(&tr)
	media := gin.H{}
	if streams, err := a.zlm.MediaList(); err == nil {
		for _, s := range streams {
			if s.Stream == ch.StreamKey {
				media = gin.H{"readerCount": s.ReaderCount, "bytes": s.Bytes, "aliveSecond": s.AliveSecond, "schema": s.Schema}
				break
			}
		}
	}
	ok(c, gin.H{"channelId": id, "streamKey": ch.StreamKey, "online": ch.Online, "traffic": tr, "media": media})
}

func (a *App) listChannelStatusLogs(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var logs []model.StatusLog
	tx := a.db.Where("channel_id = ?", id).Order("id DESC")
	if limit := c.Query("limit"); limit != "" {
		if n, err := strconv.Atoi(limit); err == nil && n > 0 {
			tx = tx.Limit(n)
		} else {
			tx = tx.Limit(100)
		}
	} else {
		tx = tx.Limit(100)
	}
	tx.Find(&logs)
	ok(c, logs)
}

func (a *App) listDeviceStatusLogs(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var logs []model.StatusLog
	a.db.Where("device_id = ?", id).Order("id DESC").Limit(100).Find(&logs)
	ok(c, logs)
}

// ---- device health check (手册 3.2.3 设备检测) ----

func defaultDevicePort(protocol string) int {
	switch protocol {
	case "rtsp", "onvif":
		return 554
	case "rtmp":
		return 1935
	case "gb28181", "ehome":
		return 5060
	case "hikvision", "isapi":
		return 80
	case "dahua":
		return 37777
	default:
		return 554
	}
}

func probeTCP(ip string, port int, timeout time.Duration) (bool, string) {
	if ip == "" {
		return false, "缺少IP地址"
	}
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false, err.Error()
	}
	_ = conn.Close()
	return true, ""
}

func (a *App) checkDevice(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	var dev model.Device
	if err := a.db.First(&dev, id).Error; err != nil {
		fail(c, http.StatusNotFound, "device not found")
		return
	}
	port := dev.Port
	if port == 0 {
		port = defaultDevicePort(dev.Protocol)
	}
	online, msg := probeTCP(dev.IP, port, 3*time.Second)
	status := "offline"
	if online {
		status = "online"
	}
	a.db.Model(&model.Device{}).Where("id = ?", id).Update("status", status)
	a.db.Create(&model.StatusLog{
		DeviceID: id, Target: "device", Online: online, Source: "check",
		Message: msg, IP: dev.IP, LoggedAt: time.Now(),
	})
	ok(c, gin.H{"deviceId": id, "online": online, "status": status, "message": msg})
}

func (a *App) checkDevices(c *gin.Context) {
	var devs []model.Device
	a.db.Find(&devs)
	onlineCount := 0
	for _, dev := range devs {
		port := dev.Port
		if port == 0 {
			port = defaultDevicePort(dev.Protocol)
		}
		online, msg := probeTCP(dev.IP, port, 2*time.Second)
		status := "offline"
		if online {
			status = "online"
			onlineCount++
		}
		a.db.Model(&model.Device{}).Where("id = ?", dev.ID).Update("status", status)
		a.db.Create(&model.StatusLog{
			DeviceID: dev.ID, Target: "device", Online: online, Source: "check",
			Message: msg, IP: dev.IP, LoggedAt: time.Now(),
		})
	}
	ok(c, gin.H{"total": len(devs), "online": onlineCount})
}

// startMonitor periodically samples traffic/status until ctx is cancelled.
func (a *App) startMonitor(stop <-chan struct{}, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-stop:
				ticker.Stop()
				return
			case <-ticker.C:
				a.syncTraffic()
			}
		}
	}()
}
