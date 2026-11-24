package model

import (
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/security"
)

// Credential fields are encrypted transparently at the persistence layer.
// API keys that are stored only as hashes (User.PasswordHash, APIKey.KeyHash)
// are deliberately not included here.

func (d *Device) BeforeSave(*gorm.DB) error     { return security.EncryptStrings(&d.Password) }
func (d *Device) AfterFind(*gorm.DB) error      { return security.DecryptStrings(&d.Password) }
func (p *AIProvider) BeforeSave(*gorm.DB) error { return security.EncryptStrings(&p.APIKey) }
func (p *AIProvider) AfterFind(*gorm.DB) error  { return security.DecryptStrings(&p.APIKey) }
func (c *CloudStore) BeforeSave(*gorm.DB) error {
	return security.EncryptStrings(&c.AccessKey, &c.SecretKey)
}
func (c *CloudStore) AfterFind(*gorm.DB) error {
	return security.DecryptStrings(&c.AccessKey, &c.SecretKey)
}
func (n *NotificationChannel) BeforeSave(*gorm.DB) error {
	return security.EncryptStrings(&n.Secret, &n.SMTPPassword)
}
func (n *NotificationChannel) AfterFind(*gorm.DB) error {
	return security.DecryptStrings(&n.Secret, &n.SMTPPassword)
}
func (g *GBCascade) BeforeSave(*gorm.DB) error { return security.EncryptStrings(&g.Password) }
func (g *GBCascade) AfterFind(*gorm.DB) error  { return security.DecryptStrings(&g.Password) }
func (w *GBWhiteList) BeforeSave(*gorm.DB) error {
	return security.EncryptStrings(&w.Password)
}
func (w *GBWhiteList) AfterFind(*gorm.DB) error {
	return security.DecryptStrings(&w.Password)
}
func (c *GA1400Cascade) BeforeSave(*gorm.DB) error { return security.EncryptStrings(&c.Password) }
func (c *GA1400Cascade) AfterFind(*gorm.DB) error  { return security.DecryptStrings(&c.Password) }
