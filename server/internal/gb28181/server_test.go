package gb28181

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjfoc/gmsm/gmtls"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/gb35114"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
	"github.com/easyavr/easyavr/internal/video"
)

func TestParseMessage(t *testing.T) {
	raw := "REGISTER sip:34020000002000000001@3402000000 SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;rport;branch=z9hG4bK1\r\n" +
		"From: <sip:34020000001320000001@3402000000>;tag=1\r\n" +
		"To: <sip:34020000001320000001@3402000000>\r\n" +
		"Call-ID: abc\r\nCSeq: 1 REGISTER\r\nContent-Length: 0\r\n\r\n"
	m, err := ParseMessage([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !m.IsRequest || m.Method != "REGISTER" {
		t.Fatalf("unexpected: %+v", m)
	}
	if got := userPart(m.Get("To")); got != "34020000001320000001" {
		t.Fatalf("userPart = %q", got)
	}
}

func TestDigestVerify(t *testing.T) {
	realm, password, user, uri := "3402000000", "easyavr123", "34020000001320000001", "sip:x@3402000000"
	nonce := "123456"
	ha1 := md5s(fmt.Sprintf("%s:%s:%s", user, realm, password))
	ha2 := md5s("REGISTER:" + uri)
	response := md5s(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	auth := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, user, realm, nonce, uri, response)
	if !VerifyDigest(auth, "REGISTER", realm, password) {
		t.Fatal("valid digest rejected")
	}
	if VerifyDigest(auth, "REGISTER", realm, "wrong") {
		t.Fatal("wrong password accepted")
	}
}

func md5s(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestRegisterKeepaliveCatalog exercises the full signaling flow over UDP:
// 401 challenge -> authenticated REGISTER -> keepalive -> catalog provisioning.
func TestRegisterKeepaliveCatalog(t *testing.T) {
	cfg := config.GBConfig{
		Enabled: true, Listen: "127.0.0.1:0", ID: "34020000002000000001",
		Realm: "3402000000", Password: "easyavr123", RTPIP: "127.0.0.1",
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "gb.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	zlm := video.NewClient("http://127.0.0.1:1", "x", "127.0.0.1", 80, 80, 554, 1935)
	srv := NewServer(cfg, db, zlm)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	serverAddr, _ := net.ResolveUDPAddr("udp", srv.Addr())
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer client.Close()

	deviceID := "34020000001320000001"
	base := func(auth string) string {
		lines := []string{
			"REGISTER sip:34020000002000000001@3402000000 SIP/2.0",
			fmt.Sprintf("Via: SIP/2.0/UDP 127.0.0.1:%d;rport;branch=z9hG4bK1", client.LocalAddr().(*net.UDPAddr).Port),
			fmt.Sprintf("From: <sip:%s@3402000000>;tag=1", deviceID),
			fmt.Sprintf("To: <sip:%s@3402000000>", deviceID),
			"Call-ID: reg-1",
			"CSeq: 1 REGISTER",
			fmt.Sprintf("Contact: <sip:%s@127.0.0.1:%d>", deviceID, client.LocalAddr().(*net.UDPAddr).Port),
			"Max-Forwards: 70",
			"Expires: 3600",
		}
		if auth != "" {
			lines = append(lines, "Authorization: "+auth)
		}
		lines = append(lines, "Content-Length: 0", "", "")
		return strings.Join(lines, "\r\n")
	}

	// 1) unauthenticated -> expect 401 challenge.
	sendUDP(t, client, serverAddr, base(""))
	resp := readUDP(t, client)
	if !strings.HasPrefix(resp, "SIP/2.0 401") {
		t.Fatalf("expected 401, got: %s", firstLine(resp))
	}
	nonce := extractNonce(resp)
	if nonce == "" {
		t.Fatal("no nonce in challenge")
	}

	// 2) authenticated REGISTER -> expect 200.
	uri := "sip:34020000002000000001@3402000000"
	ha1 := md5s(fmt.Sprintf("%s:%s:%s", deviceID, cfg.Realm, cfg.Password))
	ha2 := md5s("REGISTER:" + uri)
	response := md5s(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	auth := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, deviceID, cfg.Realm, nonce, uri, response)
	sendUDP(t, client, serverAddr, base(auth))
	if got := firstLine(readUDP(t, client)); !strings.Contains(got, "200") {
		t.Fatalf("expected 200 on register, got: %s", got)
	}

	// device should be persisted as online.
	waitFor(t, func() bool {
		var dev model.GBDevice
		if err := db.Where("device_id = ?", deviceID).First(&dev).Error; err != nil {
			return false
		}
		return dev.Online
	}, "gb device online")

	// 3) keepalive MESSAGE.
	ka := xmlHeader + "<Notify><CmdType>Keepalive</CmdType><SN>2</SN><DeviceID>" + deviceID + "</DeviceID><Status>OK</Status></Notify>"
	sendUDP(t, client, serverAddr, manscdp("MESSAGE", client, deviceID, 2, ka))
	readUDP(t, client) // 200 OK

	// 4) catalog MESSAGE.
	catalog := xmlHeader + "<Response><CmdType>Catalog</CmdType><SN>3</SN><DeviceID>" + deviceID +
		"</DeviceID><SumNum>1</SumNum><DeviceList Num=\"1\"><Item><DeviceID>" + deviceID +
		"</DeviceID><Name>Camera-1</Name><Manufacturer>Hikvision</Manufacturer><Status>ON</Status></Item></DeviceList></Response>"
	sendUDP(t, client, serverAddr, manscdp("MESSAGE", client, deviceID, 3, catalog))
	readUDP(t, client)

	waitFor(t, func() bool {
		var count int64
		db.Model(&model.Channel{}).Where("gb_device_id = ?", deviceID).Count(&count)
		return count == 1
	}, "gb channel provisioned")

	var ch model.Channel
	db.Where("gb_device_id = ?", deviceID).First(&ch)
	if ch.GBChannelID != deviceID || !ch.Online || !strings.HasPrefix(ch.StreamKey, "gb_") {
		t.Fatalf("unexpected channel: %+v", ch)
	}

	// 5) mobile position report updates the channel GPS and creates a track.
	pos := xmlHeader + "<Notify><CmdType>MobilePosition</CmdType><SN>4</SN><DeviceID>" + deviceID +
		"</DeviceID><Time>2026-10-08T20:00:00</Time><Longitude>116.407400</Longitude><Latitude>39.904200</Latitude>" +
		"<Speed>12.5</Speed><Direction>90</Direction><Altitude>50</Altitude></Notify>"
	sendUDP(t, client, serverAddr, manscdp("MESSAGE", client, deviceID, 4, pos))
	readUDP(t, client)

	waitFor(t, func() bool {
		var c model.Channel
		db.Where("gb_device_id = ?", deviceID).First(&c)
		return c.Longitude != 0 && c.Latitude != 0
	}, "channel gps from mobile position")
	var located model.Channel
	db.Where("gb_device_id = ?", deviceID).First(&located)
	if located.Longitude < 116.4 || located.Longitude > 116.41 || located.Speed != 12.5 {
		t.Fatalf("unexpected mobile position: %+v", located)
	}
	var tracks int64
	db.Model(&model.Track{}).Where("channel_id = ? AND source = ?", located.ID, "gb28181").Count(&tracks)
	if tracks == 0 {
		t.Fatal("expected a gb28181 track point")
	}
}

// TestSecureRegisterOverGMSTLS drives an authenticated REGISTER over the
// GB35114 GM/T 0024 secure SIP transport with mutual certificate auth.
func TestSecureRegisterOverGMSTLS(t *testing.T) {
	cfg := config.GBConfig{
		Enabled: true, Listen: "127.0.0.1:0", ID: "34020000002000000001",
		Realm: "3402000000", Password: "easyavr123", RTPIP: "127.0.0.1",
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "gbtls.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	ca := gb35114.NewService(db, config.GB35114Config{Enabled: true, CertDir: filepath.Join(t.TempDir(), "ca")})
	serverPair, err := ca.PlatformTLS()
	if err != nil {
		t.Fatalf("platform tls cert: %v", err)
	}
	pool, err := ca.CAPool()
	if err != nil {
		t.Fatalf("ca pool: %v", err)
	}
	devicePair, err := ca.IssueDeviceTLS("device-tls-1")
	if err != nil {
		t.Fatalf("device tls cert: %v", err)
	}

	zlm := video.NewClient("http://127.0.0.1:1", "x", "127.0.0.1", 80, 80, 554, 1935)
	srv := NewServer(cfg, db, zlm)
	if err := srv.StartTLS(TLSConfig{
		Listen:      "127.0.0.1:0",
		SignCertPEM: serverPair.SignCertPEM, SignKeyPEM: serverPair.SignKeyPEM,
		EncCertPEM: serverPair.EncCertPEM, EncKeyPEM: serverPair.EncKeyPEM,
		CAPool: pool, RequireClientCert: true,
	}); err != nil {
		t.Fatalf("start tls: %v", err)
	}
	defer srv.Stop()

	sig, err := gmtls.X509KeyPair(devicePair.SignCertPEM, devicePair.SignKeyPEM)
	if err != nil {
		t.Fatalf("device sign keypair: %v", err)
	}
	enc, err := gmtls.X509KeyPair(devicePair.EncCertPEM, devicePair.EncKeyPEM)
	if err != nil {
		t.Fatalf("device enc keypair: %v", err)
	}
	conn, err := gmtls.Dial("tcp", srv.TLSAddr(), &gmtls.Config{
		GMSupport:          &gmtls.GMSupport{},
		Certificates:       []gmtls.Certificate{sig, enc},
		RootCAs:            pool,
		InsecureSkipVerify: true,
		ServerName:         "easyavr",
	})
	if err != nil {
		t.Fatalf("gm tls dial: %v", err)
	}
	defer conn.Close()

	deviceID := "34020000001320000009"
	reader := bufio.NewReader(conn)

	// 1) unauthenticated REGISTER -> 401 challenge.
	if _, err := conn.Write([]byte(tlsRegister(deviceID, ""))); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := readTLS(t, reader)
	if !strings.HasPrefix(resp, "SIP/2.0 401") {
		t.Fatalf("expected 401 over TLS, got: %s", firstLine(resp))
	}
	nonce := extractNonce(resp)

	// 2) authenticated REGISTER -> 200.
	uri := "sip:34020000002000000001@3402000000"
	ha1 := md5s(fmt.Sprintf("%s:%s:%s", deviceID, cfg.Realm, cfg.Password))
	ha2 := md5s("REGISTER:" + uri)
	response := md5s(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	auth := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, deviceID, cfg.Realm, nonce, uri, response)
	if _, err := conn.Write([]byte(tlsRegister(deviceID, auth))); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if got := firstLine(readTLS(t, reader)); !strings.Contains(got, "200") {
		t.Fatalf("expected 200 over TLS, got: %s", got)
	}

	waitFor(t, func() bool {
		var dev model.GBDevice
		if err := db.Where("device_id = ?", deviceID).First(&dev).Error; err != nil {
			return false
		}
		return dev.Online && dev.Transport == "TLS"
	}, "gb tls device online")

	// 3) a client without a certificate must be rejected (mutual auth).
	_, err = gmtls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", srv.TLSAddr(), &gmtls.Config{
		GMSupport:          &gmtls.GMSupport{},
		RootCAs:            pool,
		InsecureSkipVerify: true,
		ServerName:         "easyavr",
	})
	if err == nil {
		t.Fatal("expected GM/TLS handshake to fail without a client certificate")
	}
}

func tlsRegister(deviceID, auth string) string {
	lines := []string{
		"REGISTER sip:34020000002000000001@3402000000 SIP/2.0",
		"Via: SIP/2.0/TLS 127.0.0.1:5061;rport;branch=z9hG4bKtls",
		fmt.Sprintf("From: <sip:%s@3402000000>;tag=1", deviceID),
		fmt.Sprintf("To: <sip:%s@3402000000>", deviceID),
		"Call-ID: reg-tls-1",
		"CSeq: 1 REGISTER",
		fmt.Sprintf("Contact: <sip:%s@127.0.0.1:5061;transport=tls>", deviceID),
		"Max-Forwards: 70",
		"Expires: 3600",
	}
	if auth != "" {
		lines = append(lines, "Authorization: "+auth)
	}
	lines = append(lines, "Content-Length: 0", "", "")
	return strings.Join(lines, "\r\n")
}

func readTLS(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	data, err := readSIPMessage(r)
	if err != nil {
		t.Fatalf("read tls: %v", err)
	}
	return string(data)
}

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`

func manscdp(method string, client *net.UDPConn, deviceID string, cseq int, body string) string {
	port := client.LocalAddr().(*net.UDPAddr).Port
	return strings.Join([]string{
		"MESSAGE sip:34020000002000000001@3402000000 SIP/2.0",
		fmt.Sprintf("Via: SIP/2.0/UDP 127.0.0.1:%d;rport;branch=z9hG4bK%d", port, cseq),
		fmt.Sprintf("From: <sip:%s@3402000000>;tag=1", deviceID),
		"To: <sip:34020000002000000001@3402000000>",
		fmt.Sprintf("Call-ID: msg-%d", cseq),
		fmt.Sprintf("CSeq: %d %s", cseq, method),
		"Content-Type: Application/MANSCDP+xml",
		fmt.Sprintf("Content-Length: %d", len(body)),
		"", body, "",
	}, "\r\n")
}

func sendUDP(t *testing.T, c *net.UDPConn, addr *net.UDPAddr, msg string) {
	t.Helper()
	if _, err := c.WriteToUDP([]byte(msg), addr); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readUDP(t *testing.T, c *net.UDPConn) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64*1024)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf[:n])
}

func firstLine(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func extractNonce(resp string) string {
	i := strings.Index(strings.ToLower(resp), "nonce=")
	if i < 0 {
		return ""
	}
	rest := resp[i+len("nonce="):]
	rest = strings.TrimPrefix(rest, "\"")
	if j := strings.IndexAny(rest, "\""); j >= 0 {
		return rest[:j]
	}
	return strings.TrimSpace(strings.Split(rest, "\r\n")[0])
}

func waitFor(t *testing.T, cond func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", desc)
}
