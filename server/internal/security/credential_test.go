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
	if enc, err := e.Encrypt(""); err != nil || enc != "" {
		t.Fatalf("empty encrypt: %q, %v", enc, err)
	}
	if dec, err := e.Decrypt(""); err != nil || dec != "" {
		t.Fatalf("empty decrypt: %q, %v", dec, err)
	}
}

// TestEncPrefixedPlaintextIsEncrypted is the regression test for the
// vulnerability where Encrypt() returned any string starting with "enc:"
// verbatim, storing user-supplied passwords in cleartext.
func TestEncPrefixedPlaintextIsEncrypted(t *testing.T) {
	e, err := New("enc-prefix-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{
		"enc:password",
		"enc:v1:not-really-ciphertext",
		"enc:v1:AAAA",
		"enc:",
	} {
		cipher, err := e.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", plain, err)
		}
		if cipher == plain {
			t.Fatalf("plaintext %q was returned unchanged (stored in cleartext)", plain)
		}
		if !strings.HasPrefix(cipher, "enc:v1:") {
			t.Fatalf("unexpected ciphertext format for %q: %q", plain, cipher)
		}
		got, err := e.Decrypt(cipher)
		if err != nil || got != plain {
			t.Fatalf("round trip of %q: %q, %v", plain, got, err)
		}
	}
}

func TestEncryptStringsSkipsOnlyRealCiphertext(t *testing.T) {
	e, err := New("helper-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(e)
	t.Cleanup(func() { SetDefault(nil) })

	// A fake "enc:" string must be encrypted by the helper as well.
	v := "enc:fake-password"
	if err := EncryptStrings(&v); err != nil {
		t.Fatal(err)
	}
	first := v
	if !strings.HasPrefix(first, "enc:v1:") || first == "enc:fake-password" {
		t.Fatalf("fake enc: plaintext was not encrypted: %q", first)
	}
	// Re-running the helper on a real ciphertext is a no-op (no double encrypt).
	if err := EncryptStrings(&v); err != nil {
		t.Fatal(err)
	}
	if v != first {
		t.Fatalf("value was double-encrypted: %q -> %q", first, v)
	}
	if err := DecryptStrings(&v); err != nil {
		t.Fatal(err)
	}
	if v != "enc:fake-password" {
		t.Fatalf("decrypted value: %q", v)
	}
}

func TestUnknownKeyIDErrors(t *testing.T) {
	e, _ := New("known-key", "v1", nil)
	cipher, err := e.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	unknownKid := "enc:v99:" + strings.SplitN(cipher, ":", 3)[2]
	if plain, err := e.Decrypt(unknownKid); err == nil || plain != "" {
		t.Fatalf("unknown kid decrypted: %q, %v", plain, err)
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
