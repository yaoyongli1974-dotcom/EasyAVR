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
	if streamURL == "" {
		return nil, fmt.Errorf("empty stream url")
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
		"-frames:v", "1",
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
	if out.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg produced no frame")
	}
	return out.Bytes(), nil
}
