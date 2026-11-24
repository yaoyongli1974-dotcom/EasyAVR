package security

import (
	"errors"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTripAndNonce(t *testing.T) {
	e, err := New("a-32-byte-raw-passphrase-for-tests", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.Encrypt("camera-password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Encrypt("camera-password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("nonce was reused")
	}
	if !strings.HasPrefix(a, "enc:v1:") {
		t.Fatalf("unexpected format: %q", a)
	}
	plain, err := e.Decrypt(a)
	if err != nil || plain != "camera-password" {
		t.Fatalf("decrypt: %q, %v", plain, err)
	}
	if _, err := e.Encrypt(""); err != nil {
		t.Fatal(err)
	}
}

func TestWrongKeyAndTamperingFailClosed(t *testing.T) {
	e, err := New("key-one", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := e.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := New("key-two", "v1", nil)
	if plain, err := other.Decrypt(cipher); err == nil || plain == "secret" {
		t.Fatalf("wrong key decrypted value: %q, %v", plain, err)
	}
	tampered := cipher[:len(cipher)-2] + "AA"
	if _, err := e.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	if _, err := e.Decrypt("plain-text"); !errors.Is(err, ErrPlaintext) {
		t.Fatalf("expected ErrPlaintext, got %v", err)
	}
}

func TestPreviousKeyRotation(t *testing.T) {
	old, _ := New("old-key", "v1", nil)
	cipher, err := old.Encrypt("legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := New("new-key", "v2", []string{"v1:old-key"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rotated.Decrypt(cipher)
	if err != nil || plain != "legacy-secret" {
		t.Fatalf("previous key: %q, %v", plain, err)
	}
	current, err := rotated.Encrypt("fresh-secret")
	if err != nil || !strings.HasPrefix(current, "enc:v2:") {
		t.Fatalf("current key: %q, %v", current, err)
	}
	unknown, _ := New("third-key", "v3", nil)
	if _, err := unknown.Decrypt(cipher); err == nil {
		t.Fatal("unknown key ID was accepted")
	}
}

func TestParseKeyForms(t *testing.T) {
	for _, raw := range []string{"dGhpcyBpcyBhIDMyIGJ5dGUgcGFzc3BocmFzZSE=", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "raw passphrase"} {
		if _, err := ParseKey(raw); err != nil {
			t.Errorf("ParseKey(%q): %v", raw, err)
		}
	}
	if _, err := ParseKey("  "); err == nil {
		t.Fatal("empty key accepted")
	}
}
