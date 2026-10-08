package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// apiKeyPrefix marks EasyAVR-issued open API keys.
const apiKeyPrefix = "ea_"

// GenerateAPIKey returns a new plaintext API key together with a display prefix
// and the SHA-256 hash that is persisted.
func GenerateAPIKey() (plain, prefix, hash string, err error) {
	buf := make([]byte, 24)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", err
	}
	plain = apiKeyPrefix + hex.EncodeToString(buf)
	prefix = plain[:len(apiKeyPrefix)+8]
	hash = HashAPIKey(plain)
	return plain, prefix, hash, nil
}

// HashAPIKey returns the hex SHA-256 of a plaintext API key.
func HashAPIKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
