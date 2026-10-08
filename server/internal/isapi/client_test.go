package isapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const deviceInfoXML = `<?xml version="1.0" encoding="UTF-8"?>
<DeviceInfo><deviceName>Front Door</deviceName><deviceID>dev-1</deviceID>
<model>DS-2CD2042</model><serialNumber>SN1</serialNumber>
<firmwareVersion>V5.6.0</firmwareVersion><macAddress>aa:bb:cc:dd:ee:ff</macAddress></DeviceInfo>`

const channelsXML = `<?xml version="1.0" encoding="UTF-8"?>
<StreamingChannelList>
<StreamingChannel><id>101</id><channelName>Front Door</channelName><enabled>true</enabled>
<Transport><rtspPortNo>554</rtspPortNo></Transport></StreamingChannel>
<StreamingChannel><id>102</id><channelName>Front Door</channelName><enabled>true</enabled>
<Transport><rtspPortNo>554</rtspPortNo></Transport></StreamingChannel>
</StreamingChannelList>`

func TestProbeWithDigest(t *testing.T) {
	var challenged bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			challenged = true
			w.Header().Set("WWW-Authenticate", `Digest realm="IPCamera", nonce="abc", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/ISAPI/System/deviceInfo":
			io.WriteString(w, deviceInfoXML)
		case "/ISAPI/Streaming/channels":
			io.WriteString(w, channelsXML)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "pass")
	res, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !challenged {
		t.Fatal("server never issued a digest challenge")
	}
	if res.DeviceInfo.Model != "DS-2CD2042" || res.DeviceInfo.Serial != "SN1" {
		t.Fatalf("unexpected device info: %+v", res.DeviceInfo)
	}
	if len(res.Channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(res.Channels))
	}
	if res.Channels[0].StreamType != "main" || res.Channels[1].StreamType != "sub" {
		t.Fatalf("unexpected stream types: %+v", res.Channels)
	}
	if !strings.HasPrefix(res.Channels[0].RTSPURL, "rtsp://admin:pass@127.0.0.1:554/Streaming/Channels/101") {
		t.Fatalf("unexpected rtsp url: %s", res.Channels[0].RTSPURL)
	}
}

func TestStreamTypeAndChallenge(t *testing.T) {
	if streamTypeOf("201") != "main" || streamTypeOf("202") != "sub" {
		t.Fatal("streamTypeOf mapping wrong")
	}
	ch := parseChallenge(`Digest realm="r", nonce="n", qop="auth", opaque="o"`)
	if ch.realm != "r" || ch.nonce != "n" || ch.qop != "auth" || ch.opaque != "o" {
		t.Fatalf("challenge parse wrong: %+v", ch)
	}
}
