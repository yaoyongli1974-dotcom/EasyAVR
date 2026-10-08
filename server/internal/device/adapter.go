// Package device implements the Device Access Plane: it normalizes heterogeneous
// sources (RTSP/RTMP/ONVIF/GB28181/EHOME/vendor SDK) into playable Channels and
// drives their lifecycle through the Video Middle Platform.
package device

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/easyavr/easyavr/internal/model"
)

// BuildSourceURL derives a pull URL from a device/channel pair when the channel
// has no explicit SourceURL. It knows the common vendor stream path templates.
func BuildSourceURL(dev model.Device, ch model.Channel) string {
	if ch.SourceURL != "" {
		return ch.SourceURL
	}
	switch strings.ToLower(dev.Protocol) {
	case "rtsp", "onvif":
		path := vendorRTSPPath(dev.Manufacturer, ch.StreamType)
		return fmt.Sprintf("rtsp://%s%s", authHost(dev.Username, dev.Password, dev.IP, portOr(dev.Port, 554)), path)
	case "rtmp":
		return fmt.Sprintf("rtmp://%s%s", authHost(dev.Username, dev.Password, dev.IP, portOr(dev.Port, 1935)), "/live/"+ch.StreamKey)
	default:
		return ""
	}
}

func vendorRTSPPath(manufacturer, streamType string) string {
	sub := streamType == "sub"
	switch strings.ToLower(manufacturer) {
	case "hikvision", "海康":
		if sub {
			return "/Streaming/Channels/102"
		}
		return "/Streaming/Channels/101"
	case "dahua", "大华":
		if sub {
			return "/cam/realmonitor?channel=1&subtype=1"
		}
		return "/cam/realmonitor?channel=1&subtype=0"
	case "uniview", "宇视":
		if sub {
			return "/media/video2"
		}
		return "/media/video1"
	default:
		return "/"
	}
}

func authHost(user, pass, host string, port int) string {
	if user == "" {
		return fmt.Sprintf("%s:%d", host, port)
	}
	return fmt.Sprintf("%s:%s@%s:%d", url.QueryEscape(user), url.QueryEscape(pass), host, port)
}

func portOr(p, def int) int {
	if p <= 0 {
		return def
	}
	return p
}
