package store

import (
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/security"
)

type credentialColumn struct {
	model   any
	columns []string
}

var credentialColumns = []credentialColumn{
	{&model.Device{}, []string{"password"}},
	{&model.AIProvider{}, []string{"api_key"}},
	{&model.CloudStore{}, []string{"access_key", "secret_key"}},
	{&model.NotificationChannel{}, []string{"secret", "smtp_password"}},
	{&model.GBCascade{}, []string{"password"}},
	{&model.GBWhiteList{}, []string{"password"}},
	{&model.GA1400Cascade{}, []string{"password"}},
}

var schemaCache sync.Map

func tableName(value any) string {
	parsed, err := schema.Parse(value, &schemaCache, schema.NamingStrategy{})
	if err != nil {
		return ""
	}
	return parsed.Table
}

// migrateCredentials encrypts legacy plaintext rows and re-encrypts rows
// carrying an older key ID. It is idempotent and fails closed on unknown key
// IDs or undecryptable values (no plaintext fallback).
func migrateCredentials(db *gorm.DB) error {
	enc := security.Default()
	if enc == nil {
		return nil
	}
	for _, spec := range credentialColumns {
		table := tableName(spec.model)
		for _, column := range spec.columns {
			if err := migrateColumn(db, table, column, enc); err != nil {
				return err
			}
		}
	}
	var rows []map[string]any
	if err := db.Table(tableName(&model.PlatformSetting{})).Select("id, value").
		Where("key = ?", GBPasswordSettingKey).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		value := valueString(row["value"])
		if value == "" {
			continue
		}
		updated, err := migrateValue(value, enc)
		if err != nil {
			return fmt.Errorf("platform_settings.gb.sip.password: %w", err)
		}
		if updated != value {
			if err := db.Table(tableName(&model.PlatformSetting{})).Where("id = ?", row["id"]).
				Update("value", updated).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateColumn(db *gorm.DB, table, column string, enc *security.Encryptor) error {
	var rows []map[string]any
	if err := db.Table(table).Select("id, " + column).Find(&rows).Error; err != nil {
		return fmt.Errorf("scan %s.%s: %w", table, column, err)
	}
	for _, row := range rows {
		value := valueString(row[column])
		if value == "" {
			continue
		}
		updated, err := migrateValue(value, enc)
		if err != nil {
			return fmt.Errorf("%s.%s row %v: %w", table, column, row["id"], err)
		}
		if updated != value {
			if err := db.Table(table).Where("id = ?", row["id"]).
				Update(column, updated).Error; err != nil {
				return fmt.Errorf("update %s.%s row %v: %w", table, column, row["id"], err)
			}
		}
	}
	return nil
}

func migrateValue(value string, enc *security.Encryptor) (string, error) {
	if !strings.HasPrefix(value, "enc:") {
		cipher, err := enc.Encrypt(value)
		if err != nil {
			return "", err
		}
		plain, err := enc.Decrypt(cipher)
		if err != nil || plain != value {
			return "", fmt.Errorf("encryption verification failed: %w", err)
		}
		return cipher, nil
	}
	plain, err := enc.Decrypt(value)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(value, "enc:"+enc.KeyID()+":") {
		return enc.Encrypt(plain)
	}
	return value, nil
}

func valueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}
