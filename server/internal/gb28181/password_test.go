package gb28181

import (
	"testing"

	"github.com/easyavr/easyavr/internal/config"
)

func TestPasswordRotation(t *testing.T) {
	const (
		realm    = "3402000000"
		user     = "34020000001320000001"
		uri      = "sip:34020000001320000001@3402000000"
		nonce    = "123456"
		oldPass  = "oldpw"
		newPass  = "newpw"
		register = "REGISTER"
	)
	srv := NewServer(config.GBConfig{Realm: realm, Password: oldPass}, nil, nil)
	if got := srv.Password(); got != oldPass {
		t.Fatalf("initial password = %q, want %q", got, oldPass)
	}

	oldAuth := BuildAuthorization(user, realm, oldPass, register, uri, nonce)
	if !VerifyDigest(oldAuth, register, realm, srv.Password()) {
		t.Fatal("initial password rejected")
	}

	srv.SetPassword(newPass)
	if got := srv.Password(); got != newPass {
		t.Fatalf("rotated password = %q, want %q", got, newPass)
	}

	newAuth := BuildAuthorization(user, realm, newPass, register, uri, nonce)
	if !VerifyDigest(newAuth, register, realm, srv.Password()) {
		t.Fatal("new password rejected after rotation")
	}
	if VerifyDigest(oldAuth, register, realm, srv.Password()) {
		t.Fatal("old password still accepted after rotation")
	}
	if !VerifyDigest(newAuth, register, realm, newPass) {
		t.Fatal("digest built with new password did not verify")
	}
}

func TestPasswordNilReceiver(t *testing.T) {
	var srv *Server
	if got := srv.Password(); got != "" {
		t.Fatalf("nil server password = %q, want empty", got)
	}
	srv.SetPassword("ignored") // must not panic
	if got := srv.Password(); got != "" {
		t.Fatalf("nil server password after set = %q, want empty", got)
	}
}
