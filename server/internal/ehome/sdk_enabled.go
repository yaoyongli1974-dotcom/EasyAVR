//go:build ehome_sdk

// This file is compiled only with `-tags ehome_sdk`. It is the integration point
// for Hikvision's native EHOME/ISUP SDK (HCEHOMESDK / ISUP SDK). The vendor
// ships headers + shared libraries (e.g. HCECMS.h, libhpr.so, libHCCore.so), not
// a public protocol specification, so the actual cgo bindings are added once
// those headers are available. Until then the server falls back to the pure-Go
// UDP listener (see backend.go).
package ehome

import (
	"errors"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
)

// newSDKBackend returns the native-SDK backend once implemented.
func newSDKBackend(config.EHOMEConfig, *gorm.DB) (Backend, error) {
	return nil, errors.New("ehome_sdk: native SDK bindings not implemented yet (provide HCECMS.h + libs)")
}
