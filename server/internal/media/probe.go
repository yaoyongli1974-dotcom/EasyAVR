package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// StreamInfo is the subset of an ffprobe stream we surface.
type StreamInfo struct {
	Index         int     `json:"index"`
	Codec         string  `json:"codec"`
	Profile       string  `json:"profile"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	PixFmt        string  `json:"pixFmt,omitempty"`
	BitRate       int64   `json:"bitRate,omitempty"`
	FrameRate     float64 `json:"frameRate,omitempty"`
	SampleRate    int     `json:"sampleRate,omitempty"`
	Channels      int     `json:"channels,omitempty"`
	ChannelLayout string  `json:"channelLayout,omitempty"`
}

// ProbeResult describes a stream as reported by ffprobe.
type ProbeResult struct {
	Format    string      `json:"format"`
	Duration  float64     `json:"duration"`
	BitRate   int64       `json:"bitRate"`
	Video     *StreamInfo `json:"video,omitempty"`
	Audio     *StreamInfo `json:"audio,omitempty"`
	Streams   int         `json:"streams"`
	LatencyMs int64       `json:"latencyMs"`
}

// Probe connects to a stream with ffprobe and returns its format/stream info.
// LatencyMs approximates connect + analyze time (a key playback-diagnostic
// signal). It errors if ffprobe is unavailable or the stream cannot be read.
func Probe(ctx context.Context, streamURL string, timeout time.Duration) (*ProbeResult, error) {
	if streamURL == "" {
		return nil, fmt.Errorf("empty stream url")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, fmt.Errorf("ffprobe not found in PATH: %w", err)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-print_format", "json", "-show_format", "-show_streams"}
	if strings.HasPrefix(streamURL, "rtsp://") {
		args = append(args, "-rtsp_transport", "tcp")
	}
	args = append(args, "-i", streamURL)

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(cctx, "ffprobe", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	latency := time.Since(start).Milliseconds()
	if runErr != nil {
		return nil, fmt.Errorf("ffprobe failed: %w: %s", runErr, strings.TrimSpace(errBuf.String()))
	}
	res, err := ParseProbeJSON(out.Bytes())
	if err != nil {
		return nil, err
	}
	res.LatencyMs = latency
	return res, nil
}

type probeJSON struct {
	Streams []struct {
		Index         int    `json:"index"`
		CodecName     string `json:"codec_name"`
		CodecType     string `json:"codec_type"`
		Profile       string `json:"profile"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		PixFmt        string `json:"pix_fmt"`
		BitRate       string `json:"bit_rate"`
		AvgFrameRate  string `json:"avg_frame_rate"`
		RFrameRate    string `json:"r_frame_rate"`
		SampleRate    string `json:"sample_rate"`
		Channels      int    `json:"channels"`
		ChannelLayout string `json:"channel_layout"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
}

// ParseProbeJSON parses ffprobe -print_format json output.
func ParseProbeJSON(data []byte) (*ProbeResult, error) {
	var p probeJSON
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}
	res := &ProbeResult{
		Format:  p.Format.FormatName,
		Streams: len(p.Streams),
		BitRate: atoi64(p.Format.BitRate),
	}
	res.Duration = atof(p.Format.Duration)
	for _, s := range p.Streams {
		si := StreamInfo{
			Index: s.Index, Codec: s.CodecName, Profile: s.Profile,
			Width: s.Width, Height: s.Height, PixFmt: s.PixFmt,
			BitRate: atoi64(s.BitRate), Channels: s.Channels, ChannelLayout: s.ChannelLayout,
			SampleRate: atoi(s.SampleRate),
		}
		si.FrameRate = atofFrameRate(s.AvgFrameRate)
		if si.FrameRate == 0 {
			si.FrameRate = atofFrameRate(s.RFrameRate)
		}
		switch s.CodecType {
		case "video":
			if res.Video == nil {
				res.Video = &si
			}
		case "audio":
			if res.Audio == nil {
				res.Audio = &si
			}
		}
	}
	if res.Video == nil && res.Audio == nil {
		return nil, fmt.Errorf("no audio/video stream found")
	}
	return res, nil
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func atoi(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

// atofFrameRate parses "30000/1001" or "25" into a float rate.
func atofFrameRate(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		num := atof(s[:i])
		den := atof(s[i+1:])
		if den == 0 {
			return 0
		}
		return num / den
	}
	return atof(s)
}
