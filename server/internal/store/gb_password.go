package store

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/security"
)

// GBPasswordSettingKey is the persisted, encrypted SIP digest password.
const GBPasswordSettingKey = "gb.sip.password"

// PersistedGBPassword returns the stored SIP password (decrypted) and whether
// the setting exists. A malformed or undecryptable value is a hard error: the
// service must not silently start with an unknown credential.
func PersistedGBPassword(db *gorm.DB) (string, bool, error) {
	var s model.PlatformSetting
	err := db.Where("key = ?", GBPasswordSettingKey).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if security.Default() == nil {
		return s.Value, true, nil
	}
	plain, err := security.Default().Decrypt(s.Value)
	if err != nil {
		return "", true, fmt.Errorf("stored GB SIP password cannot be decrypted: %w", err)
	}
	return plain, true, nil
}

// SaveGBPassword upserts the encrypted SIP digest password.
func SaveGBPassword(db *gorm.DB, password string) error {
	value := password
	if enc := security.Default(); enc != nil {
		var err error
		value, err = enc.Encrypt(password)
		if err != nil {
			return err
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var s model.PlatformSetting
		err := tx.Where("key = ?", GBPasswordSettingKey).First(&s).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&model.PlatformSetting{Key: GBPasswordSettingKey, Value: value}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&s).Update("value", value).Error
	})
}
