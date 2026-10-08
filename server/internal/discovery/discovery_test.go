package discovery

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestParseProbeMatches(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope"
 xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"
 xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
 <SOAP-ENV:Body>
  <d:ProbeMatches>
   <d:ProbeMatch>
    <w:EndpointReference><w:Address>urn:uuid:abc</w:Address></w:EndpointReference>
    <d:Types>dn:NetworkVideoTransmitter</d:Types>
    <d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/name/HIKVISION%20DS-2CD onvif://www.onvif.org/hardware/DS-2CD2042 onvif://www.onvif.org/location/country/china</d:Scopes>
    <d:XAddrs>http://192.168.1.64/onvif/device_service</d:XAddrs>
   </d:ProbeMatch>
  </d:ProbeMatches>
 </SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

	items := parseProbeMatches([]byte(raw), "10.0.0.1")
	if len(items) != 1 {
		t.Fatalf("expected 1 match, got %d", len(items))
	}
	d := items[0]
	if d.IP != "192.168.1.64" || d.Port != 80 || d.Protocol != "onvif" {
		t.Fatalf("unexpected endpoint: %+v", d)
	}
	if d.Name != "HIKVISION DS-2CD" || d.Model != "DS-2CD2042" {
		t.Fatalf("unexpected scopes: %+v", d)
	}
	if d.Manufacturer != "hikvision" {
		t.Fatalf("expected hikvision, got %q", d.Manufacturer)
	}
}

func TestScanSubnetLimits(t *testing.T) {
	if _, err := ScanSubnetTCP(context.Background(), "10.0.0.0/16", time.Second); err == nil {
		t.Fatal("expected /16 to be rejected")
	}
	if _, err := ScanSubnetTCP(context.Background(), "not-a-cidr", time.Second); err == nil {
		t.Fatal("expected invalid cidr to be rejected")
	}
}

func TestHostsInNet(t *testing.T) {
	_, ipnet, err := net.ParseCIDR("192.168.1.0/30")
	if err != nil {
		t.Fatal(err)
	}
	hosts := hostsInNet(*ipnet)
	if len(hosts) != 4 || hosts[0] != "192.168.1.0" || hosts[3] != "192.168.1.3" {
		t.Fatalf("unexpected hosts: %v", hosts)
	}
}
