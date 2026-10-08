package playauth

import (
	"strings"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	tok, exp := Sign("secret", "ch_abc", time.Minute)
	if tok == "" || exp == 0 {
		t.Fatalf("empty token: %q %d", tok, exp)
	}
	if !Verify("secret", "ch_abc", tok, exp) {
		t.Fatal("valid token rejected")
	}
	if Verify("secret", "ch_other", tok, exp) {
		t.Fatal("token accepted for wrong stream")
	}
	if Verify("other-secret", "ch_abc", tok, exp) {
		t.Fatal("token accepted with wrong secret")
	}
}

func TestVerifyExpired(t *testing.T) {
	tok := token("secret", "ch_abc", time.Now().Add(-time.Minute).Unix())
	exp := time.Now().Add(-time.Minute).Unix()
	if Verify("secret", "ch_abc", tok, exp) {
		t.Fatal("expired token accepted")
	}
}

func TestAppendQuery(t *testing.T) {
	got := AppendQuery("http://h/a/b.live.flv", "tok", 123)
	if !strings.Contains(got, "?token=tok&exp=123") {
		t.Fatalf("unexpected query: %s", got)
	}
	got = AppendQuery("http://h/x?app=demo", "tok", 123)
	if !strings.Contains(got, "&token=tok&exp=123") {
		t.Fatalf("unexpected query: %s", got)
	}
}
