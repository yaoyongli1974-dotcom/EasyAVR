package snapshot

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotAccessToken(t *testing.T) {
	const secret = "unit-test-secret"

	token, expires, err := SnapshotAccessToken(42, 7, time.Minute, secret)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(token, "s") || len(strings.Split(token, ".")) != 2 {
		t.Fatalf("unexpected token shape: %q", token)
	}
	id, user, parsedExpiry, err := ParseSnapshotAccessToken(token, secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if id != 42 || user != 7 {
		t.Fatalf("claims mismatch: id=%d user=%d", id, user)
	}
	if parsedExpiry.UnixMilli() != expires.UnixMilli() {
		t.Fatalf("expiry mismatch: %v vs %v", parsedExpiry, expires)
	}
	if d := time.Until(parsedExpiry); d < 59*time.Second || d > 61*time.Second {
		t.Fatalf("expiry not ~1m away: %v", d)
	}

	// Zero ttl uses the 5-minute default.
	_, defaultExpiry, err := SnapshotAccessToken(1, 2, 0, secret)
	if err != nil {
		t.Fatalf("issue default: %v", err)
	}
	if d := time.Until(defaultExpiry); d < DefaultTokenTTL-time.Second || d > DefaultTokenTTL+time.Second {
		t.Fatalf("default ttl not ~5m: %v", d)
	}

	// Changing the snapshot ID invalidates the signature.
	parts := strings.Split(strings.TrimPrefix(token, "s"), ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var p accessPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	p.ID = 43
	altered, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	tampered := "s" + base64.RawURLEncoding.EncodeToString(altered) + "." + parts[1]
	if _, _, _, err := ParseSnapshotAccessToken(tampered, secret); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered ID: got %v, want ErrInvalidToken", err)
	}

	// Expiry, wrong secret and malformed/prefix errors.
	expired, _, err := SnapshotAccessToken(42, 7, -time.Minute, secret)
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	if _, _, _, err := ParseSnapshotAccessToken(expired, secret); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired token: got %v, want ErrExpiredToken", err)
	}
	if _, _, _, err := ParseSnapshotAccessToken(token, "other-secret"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong secret: got %v, want ErrInvalidToken", err)
	}
	for _, bad := range []string{"x" + strings.TrimPrefix(token, "s"), "sabc", "s..", ""} {
		if _, _, _, err := ParseSnapshotAccessToken(bad, secret); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("malformed %q: got %v, want ErrInvalidToken", bad, err)
		}
	}
	if _, _, err := SnapshotAccessToken(1, 2, time.Minute, ""); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("empty secret issue: got %v", err)
	}
	if _, _, _, err := ParseSnapshotAccessToken(token, ""); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("empty secret parse: got %v", err)
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	s := NewService(nil, nil, dir)

	got, err := s.ResolvePath(filepath.Join("12", "34.jpg"))
	if err != nil {
		t.Fatalf("valid path: %v", err)
	}
	if want := filepath.Join(dir, "12", "34.jpg"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	base := filepath.Base(dir)
	for _, rel := range []string{
		"../outside.jpg",
		"12/../../../outside.jpg",
		"/etc/passwd",
		"",
		".",
		"../" + base + "-evil/x.jpg", // sibling with a matching prefix
	} {
		if got, err := s.ResolvePath(rel); err == nil {
			t.Fatalf("path %q should be rejected, resolved to %q", rel, got)
		} else if !errors.Is(err, ErrPathOutside) {
			t.Fatalf("path %q: got %v, want ErrPathOutside", rel, err)
		}
	}
}
