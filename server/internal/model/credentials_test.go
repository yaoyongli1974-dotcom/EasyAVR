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

func TestCredentialHookEncPrefixedPasswordIsEncrypted(t *testing.T) {
	e, err := security.New("enc-prefix-model-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "enc-prefix.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Device{}); err != nil {
		t.Fatal(err)
	}

	plain := "enc:user-supplied-password"
	dev := Device{Name: "cam", Password: plain}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Table("devices").Where("id = ?", dev.ID).Pluck("password", &raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, plain) {
		t.Fatalf("enc:-prefixed password stored in cleartext: %q", raw)
	}
	var got Device
	if err := db.First(&got, dev.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Password != plain {
		t.Fatalf("decrypted password: %q", got.Password)
	}
}

func TestCredentialHookRepeatedSaveDoesNotDoubleEncrypt(t *testing.T) {
	e, err := security.New("repeat-save-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "repeat-save.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Device{}); err != nil {
		t.Fatal(err)
	}

	plain := "cam-secret"
	dev := Device{Name: "cam", Password: plain}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}

	rawPassword := func() string {
		var raw string
		if err := db.Table("devices").Where("id = ?", dev.ID).Pluck("password", &raw).Error; err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first := rawPassword()
	if !strings.HasPrefix(first, "enc:v1:") {
		t.Fatalf("initial ciphertext: %q", first)
	}

	// Same in-memory instance saved again: BeforeSave sees its own ciphertext
	// (hooks mutate in place) and must not wrap it a second time.
	if err := db.Save(&dev).Error; err != nil {
		t.Fatal(err)
	}
	if second := rawPassword(); second != first {
		t.Fatalf("second save changed stored value: %q -> %q", first, second)
	}

	// Save an instance freshly read through AfterFind (plaintext in memory).
	var got Device
	if err := db.First(&got, dev.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Password != plain {
		t.Fatalf("read-back password: %q", got.Password)
	}
	if err := db.Save(&got).Error; err != nil {
		t.Fatal(err)
	}
	third := rawPassword()
	if strings.Contains(third, plain) {
		t.Fatalf("password stored in cleartext: %q", third)
	}
	decrypted, err := e.Decrypt(third)
	if err != nil || decrypted != plain {
		t.Fatalf("stored value after repeated saves: %q, %v", decrypted, err)
	}
}
