package access

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&model.GBBlackList{}, &model.GBWhiteList{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestBlacklistBlocksAllMatchingFields(t *testing.T) {
	db := newDB(t)
	db.Create(&model.GBBlackList{Protocol: "GB28181", DeviceID: "34020000001320000001", IP: "1.2.3.4", Enabled: true})

	if allowed, _ := Check(db, "GB28181", "34020000001320000001", "", "1.2.3.4", 5060); allowed {
		t.Fatal("exact match should be denied")
	}
	if allowed, _ := Check(db, "GB28181", "34020000001320000001", "", "9.9.9.9", 5060); !allowed {
		t.Fatal("rule with multiple fields must require all fields to match before blocking")
	}
	if allowed, _ := Check(db, "GB28181", "99999999999999999999", "", "9.9.9.9", 5060); !allowed {
		t.Fatal("unrelated device should be allowed")
	}
}

func TestWhitelistRequiresMatchWhenPresent(t *testing.T) {
	db := newDB(t)
	if allowed, _ := Check(db, "GB28181", "any", "", "1.1.1.1", 5060); !allowed {
		t.Fatal("empty whitelist should allow")
	}
	db.Create(&model.GBWhiteList{Protocol: "GB28181", DeviceID: "34020000001320000001", Enabled: true})
	if allowed, _ := Check(db, "GB28181", "34020000001320000001", "", "1.1.1.1", 5060); !allowed {
		t.Fatal("whitelisted device should be allowed")
	}
	if allowed, _ := Check(db, "GB28181", "other", "", "1.1.1.1", 5060); allowed {
		t.Fatal("non-whitelisted device should be denied when whitelist active")
	}
}

func TestEmptyBlacklistRuleIgnored(t *testing.T) {
	db := newDB(t)
	db.Create(&model.GBBlackList{Protocol: "GB28181", Enabled: true})
	if allowed, _ := Check(db, "GB28181", "any", "", "1.1.1.1", 5060); !allowed {
		t.Fatal("empty rule must not block everyone")
	}
}
