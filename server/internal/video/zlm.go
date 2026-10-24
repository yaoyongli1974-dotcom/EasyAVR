// Package video implements the Video Middle Platform: it wraps the
// ZLMediaKit streaming core (pull / transcode / distribute) and produces the
// playable URLs that the Video Resource Center catalogs.
package video

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultApp is the ZLMediaKit application namespace used for all streams.
const DefaultApp = "live"

// Client is a thin ZLMediaKit HTTP API client.
type Client struct {
	apiBase string
	secret  string
	http    *http.Client
	// Public endpoint info used to build browser/player URLs.
	MediaHost string
	HTTPPort  int
	WSPort    int
	RTSPPort  int
	RTMPPort  int
}

func NewClient(apiBase, secret, mediaHost string, httpPort, wsPort, rtspPort, rtmpPort int) *Client {
	return &Client{
		apiBase:   apiBase,
		secret:    secret,
		http:      &http.Client{Timeout: 8 * time.Second},
		MediaHost: mediaHost,
		HTTPPort:  httpPort,
		WSPort:    wsPort,
		RTSPPort:  rtspPort,
		RTMPPort:  rtmpPort,
	}
}

type apiResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *Client) call(endpoint string, params map[string]string) (json.RawMessage, error) {
	q := url.Values{}
	q.Set("secret", c.secret)
	for k, v := range params {
		q.Set(k, v)
	}
	endpoint = fmt.Sprintf("%s/index/api/%s?%s", c.apiBase, endpoint, q.Encode())
	resp, err := c.http.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("zlm request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var r apiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("zlm returned non-json body (status %d): %s", resp.StatusCode, string(body))
	}
	if r.Code != 0 {
		return nil, fmt.Errorf("zlm error %d: %s", r.Code, r.Msg)
	}
	return r.Data, nil
}

// AddStreamProxy asks ZLM to pull a stream from a source URL. It returns the
// proxy key which must be kept to delete the proxy later.
func (c *Client) AddStreamProxy(streamKey, sourceURL string) (string, error) {
	data, err := c.call("addStreamProxy", map[string]string{
		"vhost":    "__defaultVhost__",
		"app":      DefaultApp,
		"stream":   streamKey,
		"url":      sourceURL,
		"rtp_type": "0",
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(data, &out)
	return out.Key, nil
}

// DelStreamProxy removes a previously created pull proxy.
func (c *Client) DelStreamProxy(key string) error {
	_, err := c.call("delStreamProxy", map[string]string{"key": key})
	return err
}

// CloseStreams force-closes all publishers/players for a stream key.
func (c *Client) CloseStreams(streamKey string) error {
	_, err := c.call("close_streams", map[string]string{
		"vhost":  "__defaultVhost__",
		"app":    DefaultApp,
		"stream": streamKey,
		"force":  "1",
	})
	return err
}

// StartRecord starts server-side recording for a stream.
func (c *Client) StartRecord(streamKey string) error {
	_, err := c.call("startRecord", map[string]string{
		"vhost":  "__defaultVhost__",
		"app":    DefaultApp,
		"stream": streamKey,
		"type":   "1",
	})
	return err
}

// StopRecord stops server-side recording for a stream.
func (c *Client) StopRecord(streamKey string) error {
	_, err := c.call("stopRecord", map[string]string{
		"vhost":  "__defaultVhost__",
		"app":    DefaultApp,
		"stream": streamKey,
		"type":   "1",
	})
	return err
}

// OpenRtpServer opens an RTP receive port used by GB28181/EHOME push mode.
// A random port is allocated when port is 0.
func (c *Client) OpenRtpServer(streamKey string, port int, tcp bool) (int, error) {
	tcpMode := "0"
	if tcp {
		tcpMode = "1"
	}
	data, err := c.call("openRtpServer", map[string]string{
		"port":      strconv.Itoa(port),
		"tcp_mode":  tcpMode,
		"stream_id": streamKey,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal(data, &out)
	return out.Port, nil
}

// CloseRtpServer closes the RTP receive port for a stream key.
func (c *Client) CloseRtpServer(streamKey string) error {
	_, err := c.call("closeRtpServer", map[string]string{"stream_id": streamKey})
	return err
}

// MediaStream is a minimal view of an active ZLM stream.
type MediaStream struct {
	App              string `json:"app"`
	Stream           string `json:"stream"`
	Schema           string `json:"schema"`
	ReaderCount      int    `json:"readerCount"`
	TotalReaderCount int    `json:"totalReaderCount"`
	OriginType       int    `json:"originType"`
	TotalBytes       int64  `json:"totalBytes"`
	BytesSpeed       int64  `json:"bytesSpeed"`
	AliveSecond      int    `json:"aliveSecond"`
}

// SummarizeStreams folds the per-schema entries ZLM returns for one stream
// (rtsp/rtmp/hls/fmp4/ts) into a single record. Every schema reports the same
// cumulative byte counter, so the largest value wins; reader counts are
// summed because each entry only counts its own schema.
func SummarizeStreams(streams []MediaStream) map[string]MediaStream {
	out := make(map[string]MediaStream, len(streams))
	for _, s := range streams {
		agg, ok := out[s.Stream]
		if !ok {
			agg = s
		} else {
			if s.TotalBytes > agg.TotalBytes {
				agg.TotalBytes = s.TotalBytes
			}
			if s.AliveSecond > agg.AliveSecond {
				agg.AliveSecond = s.AliveSecond
			}
			if s.ReaderCount > 0 {
				agg.ReaderCount += s.ReaderCount
			}
		}
		if s.TotalReaderCount > agg.TotalReaderCount {
			agg.TotalReaderCount = s.TotalReaderCount
		}
		out[s.Stream] = agg
	}
	return out
}

// MediaList returns currently active streams.
func (c *Client) MediaList() ([]MediaStream, error) {
	data, err := c.call("getMediaList", nil)
	if err != nil {
		return nil, err
	}
	var list []MediaStream
	_ = json.Unmarshal(data, &list)
	return list, nil
}

// Healthy reports whether the ZLM API is reachable and responds with code 0.
func (c *Client) Healthy() bool {
	_, err := c.call("getServerConfig", nil)
	return err == nil
}

// RecordFile is the result of listing recorded mp4 files for a stream.
type RecordFile struct {
	RootPath string   `json:"rootPath"`
	Paths    []string `json:"paths"`
}

// GetMp4RecordFile lists mp4 recordings for a stream on a given day (YYYY-MM-DD).
func (c *Client) GetMp4RecordFile(streamKey, period string) (*RecordFile, error) {
	data, err := c.call("getMp4RecordFile", map[string]string{
		"vhost":  "__defaultVhost__",
		"app":    DefaultApp,
		"stream": streamKey,
		"period": period,
	})
	if err != nil {
		return nil, err
	}
	var out RecordFile
	_ = json.Unmarshal(data, &out)
	return &out, nil
}

// GetRecordStatus reports whether server-side recording is active for a stream.
func (c *Client) GetRecordStatus(streamKey string) (bool, error) {
	data, err := c.call("getRecordStatus", map[string]string{
		"vhost":  "__defaultVhost__",
		"app":    DefaultApp,
		"stream": streamKey,
		"type":   "1",
	})
	if err != nil {
		return false, err
	}
	var out struct {
		Status bool `json:"status"`
	}
	_ = json.Unmarshal(data, &out)
	return out.Status, nil
}

func (c *Client) base(scheme string) string {
	host := c.MediaHost
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func (c *Client) portURL(scheme, path string, port int) string {
	host := c.MediaHost
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 80 && scheme == "http" || port == 443 && scheme == "https" {
		return fmt.Sprintf("%s://%s%s", scheme, host, path)
	}
	return fmt.Sprintf("%s%s:%d%s", scheme+"://", host, port, path)
}

// PlayURLs returns all distribution URLs for a stream key.
func (c *Client) PlayURLs(streamKey string) map[string]string {
	app := DefaultApp
	return map[string]string{
		"http-flv":  c.portURL("http", fmt.Sprintf("/%s/%s.live.flv", app, streamKey), c.HTTPPort),
		"http-fmp4": c.portURL("http", fmt.Sprintf("/%s/%s.live.mp4", app, streamKey), c.HTTPPort),
		"hls":       c.portURL("http", fmt.Sprintf("/%s/%s/hls.m3u8", app, streamKey), c.HTTPPort),
		"webrtc":    c.portURL("http", fmt.Sprintf("/index/api/webrtc?app=%s&stream=%s&type=play", app, streamKey), c.HTTPPort),
		"rtmp":      c.portURL("rtmp", fmt.Sprintf("/%s/%s", app, streamKey), c.RTMPPort),
		"rtsp":      c.portURL("rtsp", fmt.Sprintf("/%s/%s", app, streamKey), c.RTSPPort),
	}
}

// PushURL returns the RTMP push URL for push-mode devices.
func (c *Client) PushURL(streamKey string) string {
	return c.portURL("rtmp", fmt.Sprintf("/%s/%s", DefaultApp, streamKey), c.RTMPPort)
}

// RecordURL builds the HTTP playback URL for a recorded file returned by
// GetMp4RecordFile. ZLM versions differ here: some return a date-prefixed path
// ("2024-01-02/14-30-00.mp4"), others only the file name of the day being
// queried ("2024-01-02-14-30-00-0.mp4"). The HTTP layout always carries the
// date directory, so add it when the path does not.
func (c *Client) RecordURL(streamKey, date, path string) string {
	rel := strings.TrimPrefix(path, "/")
	if !strings.Contains(rel, "/") {
		rel = date + "/" + rel
	}
	return c.portURL("http", fmt.Sprintf("/record/%s/%s/%s", DefaultApp, streamKey, rel), c.HTTPPort)
}
