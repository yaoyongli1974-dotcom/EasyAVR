package ga1400

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
)

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// parseDigestChallenge extracts realm and nonce from a WWW-Authenticate header.
func parseDigestChallenge(header string) (realm, nonce string) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "digest") {
		return "", ""
	}
	for _, kv := range strings.Split(header[len("Digest"):], ",") {
		kv = strings.TrimSpace(kv)
		i := strings.Index(kv, "=")
		if i < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[:i]))
		val := strings.Trim(strings.TrimSpace(kv[i+1:]), "\"")
		switch key {
		case "realm":
			realm = val
		case "nonce":
			nonce = val
		}
	}
	return realm, nonce
}

// buildDigestAuthorization creates a Digest Authorization header value.
func buildDigestAuthorization(user, realm, password, method, uri, nonce string) string {
	ha1 := md5hex(fmt.Sprintf("%s:%s:%s", user, realm, password))
	ha2 := md5hex(fmt.Sprintf("%s:%s", method, uri))
	response := md5hex(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		user, realm, nonce, uri, response)
}
