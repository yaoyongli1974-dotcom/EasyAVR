// Package media provides shared media primitives used across planes, such as
// frame extraction and file layout for recordings and snapshots.
package media

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GrabFrame extracts a single JPEG frame from any stream URL using ffmpeg.
func GrabFrame(ctx context.Context, streamURL string) ([]byte, error) {
	frames, err := GrabFrames(ctx, streamURL, 1)
	if err != nil {
		return nil, err
	}
	return frames[0], nil
}

// GrabFrames extracts up to n consecutive JPEG frames using ffmpeg. Multiple
// frames feed temporal quality checks (freeze/jitter). It returns at least one
// frame on success; fewer than n is not an error.
func GrabFrames(ctx context.Context, streamURL string, n int) ([][]byte, error) {
	if streamURL == "" {
		return nil, fmt.Errorf("empty stream url")
	}
	if n < 1 {
		n = 1
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	args := []string{"-hide_banner", "-loglevel", "error"}
	if strings.HasPrefix(streamURL, "rtsp://") {
		args = append(args, "-rtsp_transport", "tcp")
	}
	args = append(args,
		"-i", streamURL,
		"-frames:v", fmt.Sprintf("%d", n),
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ffmpeg", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame grab failed: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	frames := splitJPEG(out.Bytes())
	if len(frames) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no frame")
	}
	return frames, nil
}

// splitJPEG splits a concatenated MJPEG byte stream into individual JPEG images
// by scanning SOI (0xFFD8) / EOI (0xFFD9) markers.
func splitJPEG(buf []byte) [][]byte {
	var frames [][]byte
	start := -1
	for i := 0; i+1 < len(buf); i++ {
		if start < 0 {
			if buf[i] == 0xFF && buf[i+1] == 0xD8 {
				start = i
			}
			continue
		}
		if buf[i] == 0xFF && buf[i+1] == 0xD9 {
			frames = append(frames, buf[start:i+2])
			start = -1
			i++
		}
	}
	return frames
}
