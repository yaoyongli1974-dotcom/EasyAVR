// Package security implements versioned AES-256-GCM encryption for credentials
// stored in the database. The master key never lives in the database or logs.
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// CredentialEncryptor encrypts and decrypts credential strings.
type CredentialEncryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// ErrPlaintext means a value is not in the encrypted format (legacy row).
var ErrPlaintext = errors.New("credential is not encrypted")

// Encryptor holds one active key plus optional older keys for rotation.
type Encryptor struct {
	kid  string
	keys map[string][32]byte
}

// ParseKey accepts base64, hex, or a raw passphrase (SHA-256 derived).
func ParseKey(s string) ([32]byte, error) {
	var key [32]byte
	s = strings.TrimSpace(s)
	if s == "" {
		return key, errors.New("empty encryption key")
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		copy(key[:], b)
		return key, nil
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		copy(key[:], b)
		return key, nil
	}
	return sha256.Sum256([]byte(s)), nil
}

// New builds an encryptor. previous entries use the "<kid>:<key>" form.
func New(primaryKey, kid string, previous []string) (*Encryptor, error) {
	key, err := ParseKey(primaryKey)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(kid) == "" {
		kid = "v1"
	}
	if strings.ContainsAny(kid, ":") {
		return nil, errors.New("key ID must not contain ':'")
	}
	e := &Encryptor{kid: kid, keys: map[string][32]byte{kid: key}}
	for _, entry := range previous {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, fmt.Errorf("invalid previous key entry (expected <kid>:<key>)")
		}
		k, err := ParseKey(parts[1])
		if err != nil {
			return nil, fmt.Errorf("previous key %q: %w", parts[0], err)
		}
		e.keys[parts[0]] = k
	}
	return e, nil
}

// KeyID returns the ID embedded into newly created ciphertexts.
func (e *Encryptor) KeyID() string { return e.kid }

// Encrypt returns "enc:<kid>:<base64(nonce||ciphertext)>" with a fresh random
// nonce for every call. Every non-empty value is really encrypted, including
// strings that merely start with "enc:" — only the empty string is returned
// unchanged.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key := e.keys[e.kid]
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:" + e.kid + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Unknown key IDs or tampered data produce errors;
// legacy plaintext is reported as ErrPlaintext and is never returned as if it
// were successfully decrypted.
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, "enc:") {
		return "", ErrPlaintext
	}
	parts := strings.SplitN(ciphertext, ":", 3)
	if len(parts) != 3 || parts[1] == "" {
		return "", errors.New("malformed encrypted credential")
	}
	key, ok := e.keys[parts[1]]
	if !ok {
		return "", fmt.Errorf("unknown credential key id %q", parts[1])
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("malformed credential payload: %w", err)
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("credential payload too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("credential decryption failed")
	}
	return string(plain), nil
}

var defaultEncryptor atomic.Pointer[Encryptor]

// SetDefault installs the process-wide encryptor used by model hooks.
func SetDefault(e *Encryptor) { defaultEncryptor.Store(e) }

// Default returns the process-wide encryptor, or nil when unset (development).
func Default() *Encryptor { return defaultEncryptor.Load() }

// EncryptStrings encrypts each non-empty field in place. GORM BeforeSave hooks
// may be invoked on an instance whose field already holds ciphertext (for
// example when the same instance is saved twice without an intervening
// AfterFind). Such a field is left untouched only when it genuinely decrypts
// under a known key — never based on the "enc:" prefix — so a repeated save
// cannot double-encrypt while a fake "enc:" plaintext is still encrypted.
func EncryptStrings(fields ...*string) error {
	e := Default()
	if e == nil {
		return nil
	}
	for _, f := range fields {
		if f == nil || *f == "" {
			continue
		}
		if _, err := e.Decrypt(*f); err == nil {
			continue
		}
		v, err := e.Encrypt(*f)
		if err != nil {
			return err
		}
		*f = v
	}
	return nil
}

// DecryptStrings decrypts each field in place and rejects legacy plaintext.
func DecryptStrings(fields ...*string) error {
	e := Default()
	if e == nil {
		return nil
	}
	for _, f := range fields {
		if f == nil || *f == "" {
			continue
		}
		v, err := e.Decrypt(*f)
		if err != nil {
			return fmt.Errorf("decrypt credential: %w", err)
		}
		*f = v
	}
	return nil
}
