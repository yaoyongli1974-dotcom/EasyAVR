// Package isapi implements a minimal Hikvision ISAPI (HTTP REST) client:
// device information and streaming channels, with HTTP Digest authentication.
// ISAPI is Hikvision's officially documented device integration API and needs
// no proprietary binary SDK, so it stays pure Go.
package isapi

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxBody = 1 << 20

// Client talks to one Hikvision ISAPI endpoint.
type Client struct {
	BaseURL  string // e.g. http://192.168.1.64
	Username string
	Password string
	HTTP     *http.Client
}

// DeviceInfo is the subset of ISAPI device information we surface.
type DeviceInfo struct {
	Name       string `json:"name"`
	DeviceID   string `json:"deviceId"`
	Model      string `json:"model"`
	Serial     string `json:"serial"`
	Firmware   string `json:"firmware"`
	MACAddress string `json:"macAddress"`
}

// StreamChannel is one ISAPI streaming channel (main/sub) with its RTSP URL.
type StreamChannel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	StreamType string `json:"streamType"` // main | sub
	RTPPort    int    `json:"rtspPort"`
	RTSPURL    string `json:"rtspUrl"`
}

// Result is the outcome of probing a device.
type Result struct {
	Host       string          `json:"host"`
	DeviceInfo DeviceInfo      `json:"deviceInfo"`
	Channels   []StreamChannel `json:"channels"`
}

// NewClient builds a client for an ISAPI host base URL.
func NewClient(baseURL, username, password string) *Client {
	return &Client{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Username: username, Password: password,
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 8 * time.Second}
}

// Probe collects device information and streaming channels and builds RTSP URLs.
func (c *Client) Probe(ctx context.Context) (*Result, error) {
	host := hostOf(c.BaseURL)
	res := &Result{Host: host}
	if info, err := c.DeviceInfo(ctx); err == nil {
		res.DeviceInfo = *info
	}
	channels, err := c.StreamingChannels(ctx)
	if err != nil {
		return res, err
	}
	res.Channels = channels
	return res, nil
}

// DeviceInfo fetches /ISAPI/System/deviceInfo.
func (c *Client) DeviceInfo(ctx context.Context) (*DeviceInfo, error) {
	body, err := c.get(ctx, "/ISAPI/System/deviceInfo")
	if err != nil {
		return nil, err
	}
	var doc deviceInfoDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse deviceInfo: %w", err)
	}
	return &DeviceInfo{
		Name: doc.Name, DeviceID: doc.DeviceID, Model: doc.Model,
		Serial: doc.Serial, Firmware: doc.Firmware, MACAddress: doc.MAC,
	}, nil
}

// StreamingChannels fetches /ISAPI/Streaming/channels and resolves RTSP URLs.
func (c *Client) StreamingChannels(ctx context.Context) ([]StreamChannel, error) {
	body, err := c.get(ctx, "/ISAPI/Streaming/channels")
	if err != nil {
		return nil, err
	}
	var doc streamingChannelList
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse streaming channels: %w", err)
	}
	host := hostOf(c.BaseURL)
	var out []StreamChannel
	for _, ch := range doc.Channels {
		id := strings.TrimSpace(ch.ID)
		if id == "" {
			continue
		}
		port := ch.Transport.RTSPPort
		if port == 0 {
			port = 554
		}
		name := strings.TrimSpace(ch.Name)
		if name == "" {
			name = "channel-" + id
		}
		out = append(out, StreamChannel{
			ID: id, Name: name, StreamType: streamTypeOf(id), RTPPort: port,
			RTSPURL: rtspURL(c.Username, c.Password, host, port, id),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("设备未返回任何码流通道")
	}
	return out, nil
}

// get performs an authenticated GET, retrying once with HTTP Digest on 401.
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	rawURL := c.BaseURL + path
	resp, err := c.request(ctx, rawURL, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := parseChallenge(resp.Header.Get("WWW-Authenticate"))
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if challenge.realm == "" {
			return nil, fmt.Errorf("设备需要认证（未收到 Digest 挑战）")
		}
		auth := buildDigest(c.Username, c.Password, http.MethodGet, requestURI(rawURL), challenge)
		resp, err = c.request(ctx, rawURL, auth)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("认证失败（用户名或密码错误）")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("设备返回 HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func (c *Client) request(ctx context.Context, rawURL, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("User-Agent", "EasyAVR")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return c.httpClient().Do(req)
}

// ---- HTTP Digest (RFC 2617, with optional qop=auth) ----

type challenge struct {
	realm, nonce, qop, opaque, algorithm string
}

func parseChallenge(header string) challenge {
	var ch challenge
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "digest") {
		return ch
	}
	for _, kv := range splitParams(header[len("Digest"):]) {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), "\"")
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "realm":
			ch.realm = val
		case "nonce":
			ch.nonce = val
		case "qop":
			ch.qop = val
		case "opaque":
			ch.opaque = val
		case "algorithm":
			ch.algorithm = val
		}
	}
	return ch
}

// splitParams splits a comma-separated header while respecting quotes.
func splitParams(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, strings.TrimSpace(cur.String()))
	}
	return parts
}

func buildDigest(user, pass, method, uri string, ch challenge) string {
	ha1 := md5hex(user + ":" + ch.realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	var response string
	fields := []string{
		fmt.Sprintf(`username="%s"`, user),
		fmt.Sprintf(`realm="%s"`, ch.realm),
		fmt.Sprintf(`nonce="%s"`, ch.nonce),
		fmt.Sprintf(`uri="%s"`, uri),
		`algorithm=MD5`,
	}
	if strings.Contains(ch.qop, "auth") {
		nc := "00000001"
		cnonce := randomHex(8)
		response = md5hex(ha1 + ":" + ch.nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
		fields = append(fields, "qop=auth", "nc="+nc, `cnonce="`+cnonce+`"`)
	} else {
		response = md5hex(ha1 + ":" + ch.nonce + ":" + ha2)
	}
	fields = append(fields, fmt.Sprintf(`response="%s"`, response))
	if ch.opaque != "" {
		fields = append(fields, fmt.Sprintf(`opaque="%s"`, ch.opaque))
	}
	return "Digest " + strings.Join(fields, ", ")
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- helpers ----

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	if host, _, err := net.SplitHostPort(u.Host); err == nil {
		return host
	}
	return u.Hostname()
}

func requestURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}

func streamTypeOf(id string) string {
	// Hikvision channel id = camera*100 + stream (1=main, 2=sub); e.g. 101/102.
	n, err := strconv.Atoi(id)
	if err == nil && n%10 == 2 {
		return "sub"
	}
	return "main"
}

func rtspURL(user, pass, host string, port int, id string) string {
	auth := ""
	if user != "" {
		auth = url.QueryEscape(user) + ":" + url.QueryEscape(pass) + "@"
	}
	return fmt.Sprintf("rtsp://%s%s:%d/Streaming/Channels/%s", auth, host, port, id)
}
