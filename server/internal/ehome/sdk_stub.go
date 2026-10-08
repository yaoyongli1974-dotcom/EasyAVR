//go:build !ehome_sdk

package ehome

import (
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
)

// newSDKBackend is only available in binaries built with `-tags ehome_sdk`.
func newSDKBackend(config.EHOMEConfig, *gorm.DB) (Backend, error) { return nil, nil }
