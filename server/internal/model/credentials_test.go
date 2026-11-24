package model

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/easyavr/easyavr/internal/security"
)

func TestCredentialHooksEncryptAtRestAndDecryptOnRead(t *testing.T) {
	e, err := security.New("model-hook-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "hooks.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Device{}, &AIProvider{}, &CloudStore{}); err != nil {
		t.Fatal(err)
	}
	dev := Device{Name: "cam", Password: "cam-secret"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Table("devices").Where("id = ?", dev.ID).Pluck("password", &raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "cam-secret") {
		t.Fatalf("password stored in plaintext: %q", raw)
	}
	var got Device
	if err := db.First(&got, dev.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Password != "cam-secret" {
		t.Fatalf("decrypted password: %q", got.Password)
	}

	got.Password = "rotated-secret"
	if err := db.Save(&got).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("devices").Where("id = ?", dev.ID).Pluck("password", &raw).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "rotated-secret") || strings.Contains(raw, "cam-secret") {
		t.Fatalf("updated password stored in plaintext: %q", raw)
	}
	var again Device
	if err := db.First(&again, dev.ID).Error; err != nil {
		t.Fatal(err)
	}
	if again.Password != "rotated-secret" {
		t.Fatalf("updated password decrypt: %q", again.Password)
	}
}
