// Package discovery actively finds surveillance devices on the local network:
// ONVIF WS-Discovery over UDP multicast and a bounded TCP port scan.
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const wsDiscoveryAddr = "239.255.255.250:3702"

// Device is a discovered network camera candidate.
type Device struct {
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	XAddr        string `json:"xaddr"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Location     string `json:"location"`
	Protocol     string `json:"protocol"` // onvif / rtsp
	Source       string `json:"source"`   // ws-discovery / port-scan
	Added        bool   `json:"added"`    // already registered in the platform
}

// ProbeONVIF sends a WS-Discovery Probe for ONVIF network video transmitters
// and collects the responders until the timeout elapses.
func ProbeONVIF(ctx context.Context, timeout time.Duration) ([]Device, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	group, _ := net.ResolveUDPAddr("udp4", wsDiscoveryAddr)
	probe := []byte(buildProbe())
	// Send twice to tolerate loss; devices often answer within milliseconds.
	_, _ = conn.WriteToUDP(probe, group)
	time.Sleep(200 * time.Millisecond)
	_, _ = conn.WriteToUDP(probe, group)

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline)
	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()

	seen := map[string]bool{}
	var out []Device
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		for _, d := range parseProbeMatches(buf[:n], addr.IP.String()) {
			key := d.IP + "|" + d.XAddr
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d)
		}
		if !time.Now().Before(deadline) {
			break
		}
	}
	sortDevices(out)
	return out, nil
}

func buildProbe() string {
	msgID := "uuid:" + uuidish()
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"` +
		` xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"` +
		` xmlns:dn="http://www.onvif.org/ver10/network/wsdl">` +
		`<e:Header>` +
		`<w:MessageID>` + msgID + `</w:MessageID>` +
		`<w:To e:mustUnderstand="true">urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>` +
		`<w:Action e:mustUnderstand="true">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action>` +
		`</e:Header>` +
		`<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body>` +
		`</e:Envelope>`
}

type probeMatches struct {
	Matches []probeMatch `xml:"Body>ProbeMatches>ProbeMatch"`
}

type probeMatch struct {
	Scopes string `xml:"Scopes"`
	XAddrs string `xml:"XAddrs"`
	Types  string `xml:"Types"`
}

// parseProbeMatches extracts device candidates from a ProbeMatches SOAP reply.
func parseProbeMatches(data []byte, fallbackIP string) []Device {
	var pm probeMatches
	if err := xml.Unmarshal(data, &pm); err != nil {
		return nil
	}
	var out []Device
	for _, match := range pm.Matches {
		xaddrs := strings.Fields(match.XAddrs)
		if len(xaddrs) == 0 {
			continue
		}
		d := Device{
			XAddr:    xaddrs[0],
			Protocol: "onvif",
			Source:   "ws-discovery",
		}
		d.IP, d.Port = hostPortFromXAddr(xaddrs[0], fallbackIP)
		applyScopes(&d, match.Scopes)
		out = append(out, d)
	}
	return out
}

func hostPortFromXAddr(xaddr, fallbackIP string) (string, int) {
	u, err := url.Parse(xaddr)
	if err != nil || u.Host == "" {
		return fallbackIP, 80
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return u.Hostname(), portOf(u.Scheme)
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}

func portOf(scheme string) int {
	if strings.EqualFold(scheme, "https") {
		return 443
	}
	return 80
}

// applyScopes maps ONVIF discovery scopes onto human fields.
func applyScopes(d *Device, scopes string) {
	for _, scope := range strings.Fields(scopes) {
		val := scope
		if i := strings.LastIndex(scope, "/"); i >= 0 {
			val = scope[i+1:]
		}
		if decoded, err := url.QueryUnescape(val); err == nil {
			val = decoded
		}
		switch {
		case strings.Contains(scope, "/name/") && d.Name == "":
			d.Name = val
		case strings.Contains(scope, "/hardware/") && d.Model == "":
			d.Model = val
		case strings.Contains(scope, "/manufacturer/") && d.Manufacturer == "":
			d.Manufacturer = val
		case strings.Contains(scope, "/location/") && d.Location == "":
			d.Location = val
		}
	}
	if d.Manufacturer == "" {
		d.Manufacturer = guessManufacturer(d.Name + " " + d.Model + " " + d.XAddr)
	}
	if d.Name == "" {
		if d.Model != "" {
			d.Name = d.Model
		} else {
			d.Name = d.IP
		}
	}
}

func guessManufacturer(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "hik"):
		return "hikvision"
	case strings.Contains(l, "dahua") || strings.Contains(l, "dav"):
		return "dahua"
	case strings.Contains(l, "uniview") || strings.Contains(l, "unv"):
		return "uniview"
	case strings.Contains(l, "axis"):
		return "axis"
	case strings.Contains(l, "bosch"):
		return "bosch"
	default:
		return "other"
	}
}

// scanPorts are the common HTTP/RTSP ports probed during a subnet scan.
var scanPorts = []struct {
	port  int
	proto string
}{
	{554, "rtsp"},
	{8000, "rtsp"},
	{80, "onvif"},
}

// ScanSubnetTCP probes a CIDR (max /24) for open camera ports and returns one
// candidate per reachable host.
func ScanSubnetTCP(ctx context.Context, cidr string, timeout time.Duration) ([]Device, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("无效网段: %w", err)
	}
	ones, bits := ipnet.Mask.Size()
	if bits-ones > 8 {
		return nil, fmt.Errorf("网段过大，请使用 /24 或更小（当前 /%d）", ones)
	}

	type result struct {
		host  string
		port  int
		proto string
	}
	var mu sync.Mutex
	var found []result
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)

	for _, host := range hostsInNet(*ipnet) {
		for _, sp := range scanPorts {
			wg.Add(1)
			sem <- struct{}{}
			go func(host string, port int, proto string) {
				defer wg.Done()
				defer func() { <-sem }()
				d := net.Dialer{Timeout: timeout}
				conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
				if err != nil {
					return
				}
				_ = conn.Close()
				mu.Lock()
				found = append(found, result{host: host, port: port, proto: proto})
				mu.Unlock()
			}(host, sp.port, sp.proto)
		}
	}
	wg.Wait()

	// Collapse to one candidate per host, preferring RTSP 554 > RTSP 8000 > ONVIF 80.
	best := map[string]result{}
	for _, r := range found {
		cur, ok := best[r.host]
		if !ok || portRank(r.port) < portRank(cur.port) {
			best[r.host] = r
		}
	}
	var out []Device
	for host, r := range best {
		out = append(out, Device{
			IP: host, Port: r.port, Protocol: r.proto, Source: "port-scan",
			Name: host, Manufacturer: "other",
		})
	}
	sortDevices(out)
	return out, nil
}

func portRank(port int) int {
	switch port {
	case 554:
		return 0
	case 8000:
		return 1
	default:
		return 2
	}
}

func hostsInNet(ipnet net.IPNet) []string {
	var out []string
	ip := ipnet.IP.Mask(ipnet.Mask)
	for ipnet.Contains(ip) {
		out = append(out, ip.String())
		next := make(net.IP, len(ip))
		copy(next, ip)
		for i := len(next) - 1; i >= 0; i-- {
			next[i]++
			if next[i] != 0 {
				break
			}
		}
		ip = next
	}
	return out
}

func sortDevices(items []Device) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].IP != items[j].IP {
			return ipLess(items[i].IP, items[j].IP)
		}
		return items[i].Port < items[j].Port
	})
}

func ipLess(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia == nil || ib == nil {
		return a < b
	}
	return bytesLess(ia.To4(), ib.To4())
}

func bytesLess(a, b []byte) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func uuidish() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
