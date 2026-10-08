// Package onvif implements a minimal ONVIF SOAP client: device information,
// media profiles and RTSP stream URIs, with WS-Security UsernameToken and HTTP
// Digest authentication fallback.
package onvif

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const envelopeNS = `xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
	` xmlns:tds="http://www.onvif.org/ver10/device/wsdl"` +
	` xmlns:trt="http://www.onvif.org/ver10/media/wsdl"` +
	` xmlns:tt="http://www.onvif.org/ver10/schema"` +
	` xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"` +
	` xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"`

const (
	passwordDigestType = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest"
	nonceBase64Type    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary"
)

// Client talks to one ONVIF endpoint.
type Client struct {
	XAddr    string
	Username string
	Password string
	HTTP     *http.Client
}

// DeviceInfo is the subset of ONVIF device information we surface.
type DeviceInfo struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Firmware     string `json:"firmware"`
	Serial       string `json:"serial"`
	Hardware     string `json:"hardware"`
}

// Profile is an ONVIF media profile with its resolved RTSP URI.
type Profile struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	StreamURI string `json:"streamUri"`
}

// Result is the outcome of probing a device.
type Result struct {
	XAddr      string     `json:"xaddr"`
	MediaXAddr string     `json:"mediaXaddr"`
	DeviceInfo DeviceInfo `json:"deviceInfo"`
	Profiles   []Profile  `json:"profiles"`
}

// NewClient builds a client for an ONVIF device service URL.
func NewClient(xaddr, username, password string) *Client {
	return &Client{XAddr: xaddr, Username: username, Password: password}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 8 * time.Second}
}

// Probe collects device information, media profiles and their RTSP URIs.
func (c *Client) Probe(ctx context.Context) (*Result, error) {
	res := &Result{XAddr: c.XAddr, MediaXAddr: c.XAddr}
	if info, err := c.getDeviceInformation(ctx, c.XAddr); err == nil {
		res.DeviceInfo = *info
	}
	if media, err := c.getCapabilities(ctx, c.XAddr); err == nil && media != "" {
		res.MediaXAddr = media
	}
	profiles, err := c.getProfiles(ctx, res.MediaXAddr)
	if err != nil {
		return res, err
	}
	for i := range profiles {
		if uri, err := c.getStreamURI(ctx, res.MediaXAddr, profiles[i].Token); err == nil {
			profiles[i].StreamURI = uri
		}
	}
	res.Profiles = profiles
	return res, nil
}

func (c *Client) getDeviceInformation(ctx context.Context, addr string) (*DeviceInfo, error) {
	var r deviceInformationResponse
	if err := c.do(ctx, addr, `<tds:GetDeviceInformation/>`, &r); err != nil {
		return nil, err
	}
	return &DeviceInfo{
		Manufacturer: r.Manufacturer, Model: r.Model, Firmware: r.Firmware,
		Serial: r.Serial, Hardware: r.Hardware,
	}, nil
}

func (c *Client) getCapabilities(ctx context.Context, addr string) (string, error) {
	var r capabilitiesResponse
	if err := c.do(ctx, addr, `<tds:GetCapabilities><tds:Category>Media</tds:Category></tds:GetCapabilities>`, &r); err != nil {
		return "", err
	}
	return r.MediaXAddr, nil
}

func (c *Client) getProfiles(ctx context.Context, addr string) ([]Profile, error) {
	var r profilesResponse
	if err := c.do(ctx, addr, `<trt:GetProfiles/>`, &r); err != nil {
		return nil, err
	}
	var out []Profile
	for _, p := range r.Profiles {
		out = append(out, Profile{Token: p.Token, Name: p.Name})
	}
	if len(out) == 0 {
		return nil, errors.New("device returned no media profiles")
	}
	return out, nil
}

func (c *Client) getStreamURI(ctx context.Context, addr, token string) (string, error) {
	body := `<trt:GetStreamUri><trt:StreamSetup>` +
		`<tt:Stream>RTP-Unicast</tt:Stream>` +
		`<tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport>` +
		`</trt:StreamSetup><trt:ProfileToken>` + xmlEscape(token) + `</trt:ProfileToken></trt:GetStreamUri>`
	var r streamURIResponse
	if err := c.do(ctx, addr, body, &r); err != nil {
		return "", err
	}
	if r.URI == "" {
		return "", errors.New("device returned an empty stream URI")
	}
	return r.URI, nil
}

// do posts a SOAP body, retrying once with HTTP Digest on a 401 challenge.
func (c *Client) do(ctx context.Context, addr, body string, out any) error {
	env := c.envelope(body)
	resp, err := c.post(ctx, addr, env, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		realm, nonce := parseChallenge(challenge)
		if realm == "" {
			return errors.New("device requires authentication (unauthorized)")
		}
		auth := digestAuthorization(c.Username, c.Password, realm, nonce, http.MethodPost, requestURI(addr))
		resp, err = c.post(ctx, addr, env, auth)
		if err != nil {
			return err
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if fault := parseFault(data); fault != "" {
		return errors.New(fault)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("device returned HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := xml.Unmarshal(data, out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
	}
	return nil
}

func (c *Client) post(ctx context.Context, addr, body, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8`)
	req.Header.Set("User-Agent", "EasyAVR")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return c.httpClient().Do(req)
}

func (c *Client) envelope(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><s:Envelope ` + envelopeNS + `>` +
		`<s:Header>` + c.securityHeader() + `</s:Header>` +
		`<s:Body>` + body + `</s:Body></s:Envelope>`
}

// securityHeader builds a WS-Security UsernameToken with a password digest.
func (c *Client) securityHeader() string {
	if c.Username == "" {
		return ""
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(c.Password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	nonceB64 := base64.StdEncoding.EncodeToString(nonce)
	return `<wsse:Security s:mustUnderstand="1">` +
		`<wsse:UsernameToken>` +
		`<wsse:Username>` + xmlEscape(c.Username) + `</wsse:Username>` +
		`<wsse:Password Type="` + passwordDigestType + `">` + digest + `</wsse:Password>` +
		`<wsse:Nonce EncodingType="` + nonceBase64Type + `">` + nonceB64 + `</wsse:Nonce>` +
		`<wsu:Created>` + created + `</wsu:Created>` +
		`</wsse:UsernameToken></wsse:Security>`
}

// HostPort extracts host and port from an ONVIF XAddr, defaulting to port 80.
func HostPort(xaddr string) (string, int) {
	u, err := url.Parse(xaddr)
	if err != nil || u.Host == "" {
		return xaddr, 80
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

func requestURI(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		if u.RawQuery != "" {
			return u.Path + "?" + u.RawQuery
		}
		return u.Path
	}
	return "/"
}

func parseChallenge(header string) (realm, nonce string) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "digest") {
		return "", ""
	}
	for _, kv := range strings.Split(header[len("Digest"):], ",") {
		kv = strings.TrimSpace(kv)
		i := strings.Index(kv, "=")
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[:i]))
		val := strings.Trim(strings.TrimSpace(kv[i+1:]), "\"")
		switch key {
		case "realm":
			realm = val
		case "nonce":
			nonce = val
		}
	}
	return realm, nonce
}

func digestAuthorization(user, pass, realm, nonce, method, uri string) string {
	ha1 := md5hex(user + ":" + realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	response := md5hex(ha1 + ":" + nonce + ":" + ha2)
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		user, realm, nonce, uri, response)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
