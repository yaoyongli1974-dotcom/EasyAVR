package onvif

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const deviceInfoXML = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>
<tds:GetDeviceInformationResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
<tds:Manufacturer>Hikvision</tds:Manufacturer><tds:Model>DS-2CD2042</tds:Model>
<tds:FirmwareVersion>V5.6.0</tds:FirmwareVersion><tds:SerialNumber>SN123</tds:SerialNumber>
<tds:HardwareId>88</tds:HardwareId></tds:GetDeviceInformationResponse>
</s:Body></s:Envelope>`

const profilesXML = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>
<trt:GetProfilesResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
<trt:Profiles token="Profile_1" fixed="true"><trt:Name>mainStream</trt:Name></trt:Profiles>
<trt:Profiles token="Profile_2" fixed="true"><trt:Name>subStream</trt:Name></trt:Profiles>
</trt:GetProfilesResponse></s:Body></s:Envelope>`

const streamURI = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>
<trt:GetStreamUriResponse xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:MediaUri>
<tt:Uri xmlns:tt="http://www.onvif.org/ver10/schema">rtsp://192.168.1.64:554/Streaming/Channels/101</tt:Uri>
</trt:MediaUri></trt:GetStreamUriResponse></s:Body></s:Envelope>`

func TestProbeWithDigestAuth(t *testing.T) {
	mediaURL := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("UsernameToken")) {
			t.Errorf("request missing WS-Security UsernameToken")
		}
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="IP Camera", nonce="abc123"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/soap+xml")
		switch {
		case bytes.Contains(body, []byte("GetDeviceInformation")):
			io.WriteString(w, deviceInfoXML)
		case bytes.Contains(body, []byte("GetCapabilities")):
			io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+
				`<tds:GetCapabilitiesResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl"><tds:Capabilities>`+
				`<tt:Media xmlns:tt="http://www.onvif.org/ver10/schema"><tt:XAddr>`+mediaURL+`</tt:XAddr></tt:Media>`+
				`</tds:Capabilities></tds:GetCapabilitiesResponse></s:Body></s:Envelope>`)
		case bytes.Contains(body, []byte("GetProfiles")):
			io.WriteString(w, profilesXML)
		case bytes.Contains(body, []byte("GetStreamUri")):
			io.WriteString(w, streamURI)
		default:
			io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body/></s:Envelope>`)
		}
	}))
	defer srv.Close()
	mediaURL = srv.URL + "/onvif/media"

	c := NewClient(srv.URL+"/onvif/device_service", "admin", "password")
	res, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if res.DeviceInfo.Manufacturer != "Hikvision" || res.DeviceInfo.Model != "DS-2CD2042" {
		t.Fatalf("unexpected device info: %+v", res.DeviceInfo)
	}
	if len(res.Profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(res.Profiles))
	}
	for _, p := range res.Profiles {
		if p.StreamURI != "rtsp://192.168.1.64:554/Streaming/Channels/101" {
			t.Fatalf("profile %s missing stream uri: %+v", p.Token, p)
		}
	}
}

func TestProbeFault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+
			`<s:Fault><s:Reason><s:Text>Sender not Authorized</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin", "bad")
	if _, err := c.Probe(context.Background()); err == nil {
		t.Fatal("expected a fault error")
	}
}

func TestHostPort(t *testing.T) {
	host, port := HostPort("http://192.168.1.64/onvif/device_service")
	if host != "192.168.1.64" || port != 80 {
		t.Fatalf("got %s:%d", host, port)
	}
	host, port = HostPort("https://cam.local:8443/onvif/device_service")
	if host != "cam.local" || port != 8443 {
		t.Fatalf("got %s:%d", host, port)
	}
}
