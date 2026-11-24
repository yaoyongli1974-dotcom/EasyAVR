package config

import (
	"strings"
	"testing"
)

func setProdBase(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"EASYAVR_ENV":                     "production",
		"EASYAVR_JWT_SECRET":              "a-strong-jwt-secret-value-12345",
		"EASYAVR_ADMIN_PASS":              "StrongAdminPass123",
		"EASYAVR_CREDENTIAL_KEY":          "a-strong-credential-key-value-12345",
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
	for _, leak := range []string{"easyavr-dev-secret-change-me", "StrongAdminPass123", "a-strong-credential-key-value-12345"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error leaked secret value: %v", err)
		}
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
