// Package playauth signs and verifies short-lived playback tokens so stream
// URLs expire after a configured TTL (手册 3.7.1.1 播放鉴权/播放时效).
package playauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Sign returns a token and its expiry (unix seconds) for a stream key.
func Sign(secret, streamKey string, ttl time.Duration) (string, int64) {
	exp := time.Now().Add(ttl).Unix()
	return token(secret, streamKey, exp), exp
}

// Verify reports whether token is valid for streamKey and not expired.
func Verify(secret, streamKey, tokenV string, exp int64) bool {
	if tokenV == "" || exp == 0 {
		return false
	}
	if time.Now().Unix() > exp {
		return false
	}
	want := token(secret, streamKey, exp)
	return hmac.Equal([]byte(want), []byte(tokenV))
}

func token(secret, streamKey string, exp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(streamKey))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// AppendQuery appends token and exp query parameters to a URL.
func AppendQuery(rawURL, tok string, exp int64) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%stoken=%s&exp=%d", rawURL, sep, tok, exp)
}
