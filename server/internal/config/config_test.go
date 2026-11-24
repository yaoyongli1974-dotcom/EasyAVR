package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// strictProdKey is a 32-byte random-looking key in the format production requires.
var strictProdKey = base64.StdEncoding.EncodeToString([]byte{
	0x3f, 0xa1, 0x52, 0x9c, 0x07, 0xe3, 0xb8, 0x4d, 0x61, 0x0f, 0x26, 0xca, 0x77, 0x12, 0x90, 0x5e,
	0x83, 0x44, 0xbf, 0x6a, 0x0c, 0xd9, 0x38, 0xfe, 0x71, 0x20, 0xa5, 0x4b, 0x63, 0x17, 0x08, 0xee,
})

func setProdBase(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"EASYAVR_ENV":                     "production",
		"EASYAVR_JWT_SECRET":              "a-strong-jwt-secret-value-12345",
		"EASYAVR_ADMIN_PASS":              "StrongAdminPass123",
		"EASYAVR_CREDENTIAL_KEY":          strictProdKey,
		"EASYAVR_CREDENTIAL_KEY_ID":       "v1",
		"EASYAVR_ALLOW_ORIGIN":            "https://easyavr.cn",
		"EASYAVR_DB_DRIVER":               "sqlite",
		"EASYAVR_DB":                      t.TempDir() + "/easyavr.db",
		"EASYAVR_SNAPSHOT_DIR":            t.TempDir() + "/snapshots",
		"EASYAVR_GB_ENABLED":              "false",
		"EASYAVR_GB35114_ENABLED":         "false",
		"EASYAVR_CLUSTER_ENABLED":         "false",
		"EASYAVR_CREDENTIAL_KEY_PREVIOUS": "",
	} {
		t.Setenv(k, v)
	}
}

func TestValidateProductionRejectsDefaults(t *testing.T) {
	setProdBase(t)
	t.Setenv("EASYAVR_JWT_SECRET", "easyavr-dev-secret-change-me")
	t.Setenv("EASYAVR_ADMIN_PASS", "easyavr")
	t.Setenv("EASYAVR_CREDENTIAL_KEY", "")
	t.Setenv("EASYAVR_ALLOW_ORIGIN", "*")
	c := Load()
	err := c.Validate()
	if err == nil {
		t.Fatal("production accepted default secrets")
	}
	for _, want := range []string{"EASYAVR_JWT_SECRET", "EASYAVR_ADMIN_PASS", "EASYAVR_CREDENTIAL_KEY", "EASYAVR_ALLOW_ORIGIN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in error: %v", want, err)
		}
	}
	for _, leak := range []string{"easyavr-dev-secret-change-me", "StrongAdminPass123", strictProdKey} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaked secret value: %v", err)
		}
	}
}

func TestValidateProductionRejectsWeakCredentialKey(t *testing.T) {
	setProdBase(t)
	t.Setenv("EASYAVR_CREDENTIAL_KEY", "easyavr-password-123")
	err := Load().Validate()
	if err == nil || !strings.Contains(err.Error(), "EASYAVR_CREDENTIAL_KEY") {
		t.Fatalf("production accepted a passphrase credential key: %v", err)
	}
	if strings.Contains(err.Error(), "easyavr-password-123") {
		t.Fatalf("error leaked credential key: %v", err)
	}
}

func TestValidateDevelopmentAcceptsPassphraseKey(t *testing.T) {
	setProdBase(t)
	t.Setenv("EASYAVR_ENV", "development")
	t.Setenv("EASYAVR_CREDENTIAL_KEY", "local-dev-passphrase")
	if err := Load().Validate(); err != nil {
		t.Fatalf("development rejected passphrase key: %v", err)
	}
}

func TestValidateProductionAcceptsStrongConfig(t *testing.T) {
	setProdBase(t)
	if err := Load().Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestValidateProductionChecksEnabledFeatures(t *testing.T) {
	setProdBase(t)
	t.Setenv("EASYAVR_GB_ENABLED", "true")
	c := Load()
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "EASYAVR_GB_ID") {
		t.Fatalf("GB defaults accepted or wrong error: %v", err)
	}
	t.Setenv("EASYAVR_GB_ENABLED", "false")
	t.Setenv("EASYAVR_CLUSTER_ENABLED", "true")
	if err := Load().Validate(); err == nil {
		t.Fatal("cluster default secret accepted")
	}
}

func TestValidateDevelopmentAcceptsDefaults(t *testing.T) {
	t.Setenv("EASYAVR_ENV", "development")
	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatalf("development defaults rejected: %v", err)
	}
}
