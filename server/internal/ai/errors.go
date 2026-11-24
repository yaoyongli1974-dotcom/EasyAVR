package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrorCode is a stable, persisted classification of a task failure. Values
// are safe to log and to write to AITaskRun.ErrorCode.
type ErrorCode string

const (
	ErrorProviderUnavailable ErrorCode = "provider_unavailable"
	ErrorProviderTimeout     ErrorCode = "provider_timeout"
	ErrorStreamUnavailable   ErrorCode = "stream_unavailable"
	ErrorFrameCaptureFailed  ErrorCode = "frame_capture_failed"
	ErrorInferenceFailed     ErrorCode = "inference_failed"
	ErrorInvalidTaskConfig   ErrorCode = "invalid_task_config"
	ErrorResourceLimit       ErrorCode = "resource_limit"
	ErrorInternal            ErrorCode = "internal_error"
)

// classifiedError carries the classification alongside the original cause.
// cause is only used for diagnostics; msg is the sanitized text persisted and
// logged (it never contains prompts, API keys, frames or event payloads).
type classifiedError struct {
	code      ErrorCode
	cause     error
	retryable bool
	msg       string
}

func (e *classifiedError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return e.msg
}

func (e *classifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *classifiedError) publicMessage() string {
	if e == nil {
		return ""
	}
	if e.msg != "" {
		return e.msg
	}
	return string(e.code)
}

func classify(code ErrorCode, retryable bool, msg string, cause error) *classifiedError {
	return &classifiedError{code: code, retryable: retryable, msg: msg, cause: cause}
}

// classifyProviderErr maps an analyzer/provider failure to a code. The
// distinction matters: only provider/stream/frame failures are retried, and a
// DB write failure must never be reported as a provider failure.
func classifyProviderErr(ctx context.Context, err error) *classifiedError {
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return classify(ErrorProviderTimeout, true, fmt.Sprintf("provider request timed out (%T)", err), err)
	}
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return classify(ErrorInternal, false, "task execution canceled", err)
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return classify(ErrorProviderTimeout, true, fmt.Sprintf("provider request timed out (%T)", err), err)
		}
		return classify(ErrorProviderUnavailable, true, fmt.Sprintf("provider is unavailable (%T)", err), err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return classify(ErrorProviderUnavailable, true, fmt.Sprintf("provider is unavailable (%T)", err), err)
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"), strings.Contains(msg, "context deadline"):
		return classify(ErrorProviderTimeout, true, fmt.Sprintf("provider request timed out (%T)", err), err)
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"),
		strings.Contains(msg, "dial "), strings.Contains(msg, "eof"), strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "service unavailable"), strings.Contains(msg, "bad gateway"),
		strings.Contains(msg, "gateway timeout"), strings.Contains(msg, "status 5"):
		return classify(ErrorProviderUnavailable, true, fmt.Sprintf("provider is unavailable (%T)", err), err)
	case strings.Contains(msg, "invalid json"), strings.Contains(msg, "invalid response"),
		strings.Contains(msg, "empty choices"), strings.Contains(msg, "unmarshal"):
		return classify(ErrorInferenceFailed, false, fmt.Sprintf("provider returned an unusable result (%T)", err), err)
	default:
		return classify(ErrorInferenceFailed, false, fmt.Sprintf("provider inference failed (%T)", err), err)
	}
}

// classifyFrameErr keeps frame-capture failures distinct from provider failures
// so ffmpeg/stream outages can be retried without being mislabeled.
func classifyFrameErr(ctx context.Context, err error) *classifiedError {
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return classify(ErrorFrameCaptureFailed, false, fmt.Sprintf("frame capture timed out (%T)", err), err)
	}
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return classify(ErrorInternal, false, "task execution canceled", err)
	}
	return classify(ErrorFrameCaptureFailed, true, fmt.Sprintf("frame capture failed (%T)", err), err)
}

func isRetryable(cerr *classifiedError, ctx context.Context) bool {
	if cerr == nil || !cerr.retryable || ctx == nil || ctx.Err() != nil {
		return false
	}
	switch cerr.code {
	case ErrorProviderUnavailable, ErrorProviderTimeout, ErrorStreamUnavailable, ErrorFrameCaptureFailed:
		return true
	default:
		return false
	}
}
