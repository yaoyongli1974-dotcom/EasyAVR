package isapi

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
)

// do performs an authenticated request with an optional XML body, retrying once
// with HTTP Digest on 401. Mirrors get() but supports any method.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	rawURL := c.BaseURL + path
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	resp, err := c.send(ctx, method, rawURL, rdr, "")
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := parseChallenge(resp.Header.Get("WWW-Authenticate"))
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if challenge.realm == "" {
			return nil, resp.StatusCode, fmt.Errorf("设备需要认证（未收到 Digest 挑战）")
		}
		auth := buildDigest(c.Username, c.Password, method, requestURI(rawURL), challenge)
		var body2 io.Reader
		if body != nil {
			body2 = bytes.NewReader(body)
		}
		resp, err = c.send(ctx, method, rawURL, body2, auth)
		if err != nil {
			return nil, 0, err
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode == http.StatusUnauthorized {
		return data, resp.StatusCode, fmt.Errorf("认证失败（用户名或密码错误）")
	}
	if resp.StatusCode >= 300 {
		return data, resp.StatusCode, fmt.Errorf("设备返回 HTTP %d", resp.StatusCode)
	}
	return data, resp.StatusCode, nil
}

func (c *Client) send(ctx context.Context, method, rawURL string, body io.Reader, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("User-Agent", "EasyAVR")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return c.httpClient().Do(req)
}

// PTZContinuous sends a continuous pan/tilt/zoom command. Values are -100..100.
func (c *Client) PTZContinuous(ctx context.Context, channelID int, pan, tilt, zoom int) error {
	body, err := xml.Marshal(ptzDataDoc{Pan: pan, Tilt: tilt, Zoom: zoom})
	if err != nil {
		return err
	}
	payload := append([]byte(xml.Header), body...)
	path := fmt.Sprintf("/ISAPI/PTZCtrl/channels/%d/continuous", channelID)
	_, _, err = c.do(ctx, http.MethodPut, path, payload)
	return err
}

// PTZGotoPreset recalls a preset.
func (c *Client) PTZGotoPreset(ctx context.Context, channelID, preset int) error {
	path := fmt.Sprintf("/ISAPI/PTZCtrl/channels/%d/presets/%d/goto", channelID, preset)
	_, _, err := c.do(ctx, http.MethodPut, path, []byte(xml.Header))
	return err
}

// PTZSetPreset stores the current position as a named preset.
func (c *Client) PTZSetPreset(ctx context.Context, channelID, preset int, name string) error {
	body, err := xml.Marshal(presetDoc{ID: preset, Name: name})
	if err != nil {
		return err
	}
	payload := append([]byte(xml.Header), body...)
	path := fmt.Sprintf("/ISAPI/PTZCtrl/channels/%d/presets/%d", channelID, preset)
	_, _, err = c.do(ctx, http.MethodPut, path, payload)
	return err
}

// PTZDeletePreset removes a preset.
func (c *Client) PTZDeletePreset(ctx context.Context, channelID, preset int) error {
	path := fmt.Sprintf("/ISAPI/PTZCtrl/channels/%d/presets/%d", channelID, preset)
	_, _, err := c.do(ctx, http.MethodDelete, path, nil)
	return err
}

type ptzDataDoc struct {
	XMLName xml.Name `xml:"PTZData"`
	Pan     int      `xml:"pan"`
	Tilt    int      `xml:"tilt"`
	Zoom    int      `xml:"zoom"`
}

type presetDoc struct {
	XMLName xml.Name `xml:"PTZPreset"`
	ID      int      `xml:"id"`
	Name    string   `xml:"presetName"`
}
