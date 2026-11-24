package security

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const (
	// Fixed 32-byte CSPRNG-style keys used only by tests.
	strictB64Key  = "nyt8HmpNgDXwsunHFIpdYw8eLEuKeQ01bi8LjB1KfmM="
	strictHexKey  = "3c7a91d4f62e08b5a1d9c3704e8f2b6519ac0d73e6b84f21c530d97a4e6b8f52"
	strictB64Key2 = "pdg8B+H5SyYXDD6KXSSftgGDx+SptS0PY47BRwvaWfI="
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

func TestParseStrictKeyValidForms(t *testing.T) {
	for _, raw := range []string{strictB64Key, strictHexKey, "  " + strictB64Key + "  "} {
		key, err := ParseStrictKey(raw)
		if err != nil {
			t.Errorf("ParseStrictKey(%q): %v", raw, err)
			continue
		}
		if key == [32]byte{} {
			t.Fatalf("ParseStrictKey(%q) returned an all-zero key", raw)
		}
	}
	b64, err := base64.StdEncoding.DecodeString(strictB64Key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseStrictKey(strictB64Key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:], b64) {
		t.Fatal("base64 key was not decoded verbatim")
	}
	hexed, err := hex.DecodeString(strictHexKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err = ParseStrictKey(strictHexKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:], hexed) {
		t.Fatal("hex key was not decoded verbatim")
	}
}

func TestParseStrictKeyRejectsInvalidAndWeakKeys(t *testing.T) {
	cases := map[string]string{
		"empty":                     "",
		"whitespace":                "   ",
		"weak passphrase":           "easyavr",
		"long passphrase":           "password123",
		"phrase with spaces":        "correct horse battery staple",
		"base64 of 31 bytes":        base64.StdEncoding.EncodeToString(make([]byte, 31)),
		"base64 of 33 bytes":        base64.StdEncoding.EncodeToString(make([]byte, 33)),
		"hex of 16 bytes":           strings.Repeat("ab", 16),
		"hex of 64 bytes":           strings.Repeat("ab", 64),
		"not base64 or hex":         "!!!not-a-key!!!",
		"all zero base64":           base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"all zero hex":              strings.Repeat("0", 64),
		"all one base64":            base64.StdEncoding.EncodeToString(bytesOf(1, 32)),
		"all ff hex":                strings.Repeat("f", 64),
		"repeated byte base64":      base64.StdEncoding.EncodeToString(bytesOf(0x41, 32)),
		"short cycle base64":        base64.StdEncoding.EncodeToString(bytesOf(0xa5, 16)),
		"ascending bytes base64":    base64.StdEncoding.EncodeToString(ascendingBytes(32)),
		"low distinct count base64": lowDistinctBytes(),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			key, err := ParseStrictKey(raw)
			if err == nil {
				t.Fatalf("ParseStrictKey accepted %s: %x", name, key)
			}
			if trimmed := strings.TrimSpace(raw); trimmed != "" && strings.Contains(err.Error(), trimmed) {
				t.Fatalf("error leaked key material: %v", err)
			}
		})
	}
}

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func ascendingBytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i)
	}
	return out
}

func lowDistinctBytes() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i % 3)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestParseKeyRemainsLenientForMigration(t *testing.T) {
	if _, err := ParseKey("easyavr"); err != nil {
		t.Fatalf("ParseKey must still accept legacy passphrases: %v", err)
	}
	if _, err := ParseStrictKey("easyavr"); err == nil {
		t.Fatal("ParseStrictKey accepted a legacy passphrase")
	}
}

func TestNewStrictRoundTrip(t *testing.T) {
	e, err := NewStrict(strictB64Key, "v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.KeyID() != "v2" {
		t.Fatalf("KeyID: %q", e.KeyID())
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
	if !strings.HasPrefix(a, "enc:v2:") {
		t.Fatalf("unexpected format: %q", a)
	}
	plain, err := e.Decrypt(a)
	if err != nil || plain != "camera-password" {
		t.Fatalf("decrypt: %q, %v", plain, err)
	}
}

func TestNewStrictRejectsWeakPrimaryAndBadMetadata(t *testing.T) {
	for _, primary := range []string{"easyavr", "", base64.StdEncoding.EncodeToString(make([]byte, 32))} {
		if _, err := NewStrict(primary, "v2", nil); err == nil {
			t.Fatalf("NewStrict accepted primary %q", primary)
		}
	}
	if _, err := NewStrict(strictB64Key, "bad:kid", nil); err == nil {
		t.Fatal("kid containing ':' was accepted")
	}
	if _, err := NewStrict(strictB64Key, "v2", []string{"no-colon"}); err == nil {
		t.Fatal("malformed previous entry was accepted")
	}
	defaultKid, err := NewStrict(strictB64Key, "  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := defaultKid.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cipher, "enc:v1:") {
		t.Fatalf("default kid: %q", cipher)
	}
}

func TestNewStrictDecryptsLegacyPassphraseCiphertext(t *testing.T) {
	legacy, err := New("legacy-passphrase", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldCipher, err := legacy.Encrypt("legacy-secret")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewStrict(strictB64Key, "v2", []string{"v1:legacy-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rotated.Decrypt(oldCipher)
	if err != nil || plain != "legacy-secret" {
		t.Fatalf("legacy ciphertext: %q, %v", plain, err)
	}
	// A wrong historical key must not decrypt it either.
	wrong, err := NewStrict(strictB64Key, "v2", []string{"v1:wrong-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := wrong.Decrypt(oldCipher); err == nil || plain != "" {
		t.Fatalf("wrong previous key decrypted: %q, %v", plain, err)
	}
	// Without the previous key the legacy row is unreadable.
	fresh, err := NewStrict(strictB64Key, "v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Decrypt(oldCipher); err == nil {
		t.Fatal("legacy ciphertext decrypted without previous key")
	}
}

func TestNewStrictWrongKeyAndUnknownKid(t *testing.T) {
	e, err := NewStrict(strictB64Key, "v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := e.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewStrict(strictB64Key2, "v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := other.Decrypt(cipher); err == nil || plain == "secret" {
		t.Fatalf("wrong strict key decrypted: %q, %v", plain, err)
	}
	unknownKid := "enc:v99:" + strings.SplitN(cipher, ":", 3)[2]
	if plain, err := e.Decrypt(unknownKid); err == nil || plain != "" {
		t.Fatalf("unknown kid decrypted: %q, %v", plain, err)
	}
}

func TestNewStrictInteroperatesWithNew(t *testing.T) {
	legacy, err := New(strictB64Key, "v2", []string{"v1:legacy-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	strict, err := NewStrict(strictB64Key, "v2", []string{"v1:legacy-passphrase"})
	if err != nil {
		t.Fatal(err)
	}
	// Ciphertext written by the legacy constructor is readable in strict mode.
	fromNew, err := legacy.Encrypt("written-by-new")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := strict.Decrypt(fromNew); err != nil || plain != "written-by-new" {
		t.Fatalf("strict decrypt of legacy ciphertext: %q, %v", plain, err)
	}
	// And vice versa, including the strict hex form of the same key.
	fromStrict, err := strict.Encrypt("written-by-strict")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := legacy.Decrypt(fromStrict); err != nil || plain != "written-by-strict" {
		t.Fatalf("legacy decrypt of strict ciphertext: %q, %v", plain, err)
	}
	hexStrict, err := NewStrict(strictHexKey, "v3", nil)
	if err != nil {
		t.Fatal(err)
	}
	hexLegacy, err := New(strictHexKey, "v3", nil)
	if err != nil {
		t.Fatal(err)
	}
	hexCipher, err := hexStrict.Encrypt("hex-key")
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := hexLegacy.Decrypt(hexCipher); err != nil || plain != "hex-key" {
		t.Fatalf("hex key interop: %q, %v", plain, err)
	}
}
