// Package gb28181 implements GB/T 28181 SIP signaling: device registration,
// keepalive, catalog, alarms, and live/playback INVITE for the Device Access
// Plane. Media is received by ZLMediaKit (openRtpServer) while this package
// only drives signaling.
package gb28181

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

// Header is a single SIP header line.
type Header struct {
	Name  string
	Value string
}

// Message is a parsed SIP request or response.
type Message struct {
	IsRequest  bool
	Method     string // request method
	URI        string // request URI
	StatusCode int    // response status
	StatusText string
	Headers    []Header
	Body       string
	Raw        []byte
}

// Get returns the first header value matching name (case-insensitive).
func (m *Message) Get(name string) string {
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// All returns every header value matching name (case-insensitive).
func (m *Message) All(name string) []string {
	var out []string
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			out = append(out, h.Value)
		}
	}
	return out
}

// Add appends a header.
func (m *Message) Add(name, value string) {
	m.Headers = append(m.Headers, Header{Name: name, Value: value})
}

// Set replaces the first header with name or appends it.
func (m *Message) Set(name, value string) {
	for i := range m.Headers {
		if strings.EqualFold(m.Headers[i].Name, name) {
			m.Headers[i].Value = value
			return
		}
	}
	m.Add(name, value)
}

// ParseMessage parses a SIP message from raw bytes.
func ParseMessage(data []byte) (*Message, error) {
	text := string(data)
	split := strings.Index(text, "\r\n\r\n")
	head := text
	body := ""
	if split >= 0 {
		head = text[:split]
		body = text[split+4:]
	}
	lines := strings.Split(head, "\r\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("empty sip message")
	}
	m := &Message{Body: body, Raw: data}
	start := strings.TrimSpace(lines[0])
	if strings.HasPrefix(strings.ToUpper(start), "SIP/2.0") {
		m.IsRequest = false
		fmt.Sscanf(start, "SIP/2.0 %d %s", &m.StatusCode, &m.StatusText)
	} else {
		m.IsRequest = true
		parts := strings.SplitN(start, " ", 3)
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid request line: %q", start)
		}
		m.Method = strings.ToUpper(parts[0])
		m.URI = parts[1]
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		m.Headers = append(m.Headers, Header{
			Name:  strings.TrimSpace(line[:idx]),
			Value: strings.TrimSpace(line[idx+1:]),
		})
	}
	return m, nil
}

// Bytes serializes the message, ensuring Via headers come first and the body
// length is correct.
func (m *Message) Bytes() []byte {
	var b strings.Builder
	if m.IsRequest {
		uri := m.URI
		if uri == "" {
			uri = "sip:unknown"
		}
		fmt.Fprintf(&b, "%s %s SIP/2.0\r\n", m.Method, uri)
	} else {
		fmt.Fprintf(&b, "SIP/2.0 %d %s\r\n", m.StatusCode, m.StatusText)
	}
	// Via headers first (RFC 3261 ordering, good enough for GB28181 devices).
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, "Via") {
			fmt.Fprintf(&b, "%s: %s\r\n", h.Name, h.Value)
		}
	}
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, "Via") || strings.EqualFold(h.Name, "Content-Length") {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\r\n", h.Name, h.Value)
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(m.Body))
	b.WriteString("\r\n")
	b.WriteString(m.Body)
	return []byte(b.String())
}

// Response builds a response to a request.
func (m *Message) Response(code int, reason string) *Message {
	resp := &Message{IsRequest: false, StatusCode: code, StatusText: reason}
	for _, h := range m.All("Via") {
		resp.Add("Via", h)
	}
	// Copy routing headers.
	for _, name := range []string{"From", "To", "Call-ID", "CSeq", "Contact"} {
		if v := m.Get(name); v != "" {
			resp.Add(name, v)
		}
	}
	return resp
}

// ---- Digest authentication (RFC 2617) ----

// AuthParams holds parsed WWW-Authenticate / Authorization fields.
type AuthParams struct {
	Username string
	Realm    string
	Nonce    string
	URI      string
	Response string
}

// ParseAuthorization extracts digest parameters from an Authorization header.
func ParseAuthorization(header string) *AuthParams {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "digest") {
		return nil
	}
	p := &AuthParams{}
	for _, kv := range strings.Split(header[len("Digest"):], ",") {
		kv = strings.TrimSpace(kv)
		idx := strings.Index(kv, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(kv[:idx])
		val := strings.Trim(strings.TrimSpace(kv[idx+1:]), "\"")
		switch strings.ToLower(key) {
		case "username":
			p.Username = val
		case "realm":
			p.Realm = val
		case "nonce":
			p.Nonce = val
		case "uri":
			p.URI = val
		case "response":
			p.Response = val
		}
	}
	return p
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// VerifyDigest checks an Authorization header against the expected password,
// using the request method and realm.
func VerifyDigest(header, method, realm, password string) bool {
	p := ParseAuthorization(header)
	if p == nil || p.Response == "" {
		return false
	}
	ha1 := md5hex(fmt.Sprintf("%s:%s:%s", p.Username, realm, password))
	ha2 := md5hex(fmt.Sprintf("%s:%s", method, p.URI))
	expected := md5hex(fmt.Sprintf("%s:%s:%s", ha1, p.Nonce, ha2))
	return strings.EqualFold(expected, p.Response)
}

// ParseChallenge extracts realm and nonce from a WWW-Authenticate header.
func ParseChallenge(header string) (realm, nonce string) {
	for _, kv := range strings.Split(header, ",") {
		kv = strings.TrimSpace(kv)
		if i := strings.Index(kv, "="); i > 0 {
			v := strings.Trim(strings.TrimSpace(kv[i+1:]), "\"")
			switch strings.ToLower(strings.TrimSpace(kv[:i])) {
			case "realm":
				realm = v
			case "nonce":
				nonce = v
			case "digest realm":
				realm = v
			}
		}
	}
	return realm, nonce
}

// BuildAuthorization generates a Digest Authorization header value.
func BuildAuthorization(username, realm, password, method, uri, nonce string) string {
	ha1 := md5hex(fmt.Sprintf("%s:%s:%s", username, realm, password))
	ha2 := md5hex(fmt.Sprintf("%s:%s", method, uri))
	response := md5hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=MD5, response="%s"`,
		username, realm, nonce, uri, response)
}
