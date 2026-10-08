// Package access enforces GB28181/EHOME registration allow/deny lists
// (手册 3.7.4.2 白名单 / 3.7.4.3 黑名单). A black-list rule matches when all of
// its non-empty fields equal the registrant; any match is denied. When at least
// one enabled white-list rule exists for the protocol, only matching devices may
// register.
package access

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// Check returns whether a registrant is allowed and, when denied, a reason.
func Check(db *gorm.DB, protocol, deviceID, ua, ip string, port int) (bool, string) {
	var blacks []model.GBBlackList
	db.Where("protocol = ? AND enabled = ?", protocol, true).Find(&blacks)
	for _, b := range blacks {
		if matchBlack(b, deviceID, ua, ip, port) {
			return false, fmt.Sprintf("命中黑名单（规则 #%d）", b.ID)
		}
	}

	var whites []model.GBWhiteList
	db.Where("protocol = ? AND enabled = ?", protocol, true).Find(&whites)
	if len(whites) == 0 {
		return true, ""
	}
	for _, w := range whites {
		if matchWhite(w, deviceID, ip, port) {
			return true, ""
		}
	}
	return false, "不在白名单内"
}

func matchBlack(b model.GBBlackList, deviceID, ua, ip string, port int) bool {
	if b.DeviceID != "" && b.DeviceID != deviceID {
		return false
	}
	if b.UA != "" && b.UA != ua {
		return false
	}
	if b.IP != "" && b.IP != ip {
		return false
	}
	if b.Port != 0 && b.Port != port {
		return false
	}
	// Require at least one criterion so an empty rule cannot block everyone.
	return b.DeviceID != "" || b.UA != "" || b.IP != "" || b.Port != 0
}

func matchWhite(w model.GBWhiteList, deviceID, ip string, port int) bool {
	if w.DeviceID != "" && w.DeviceID != deviceID {
		return false
	}
	if w.IP != "" && w.IP != ip {
		return false
	}
	if w.Port != 0 && w.Port != port {
		return false
	}
	return w.DeviceID != "" || w.IP != "" || w.Port != 0
}
