// Package ehome provides the device-access endpoint for Hikvision EHOME/ISUP
// active registration. EHOME/ISUP is a proprietary protocol whose official
// distribution is a native SDK (headers + .so/.dll); this package defines the
// integration seam so it can be driven either by the pure-Go UDP fallback or by
// an optional cgo backend (`ehome_sdk` build tag) that calls the vendor SDK.
package ehome

import (
	"log"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
)

// Server is the EHOME/ISUP device-access entry point; it delegates to a Backend.
type Server struct {
	backend Backend
}

// NewServer selects the SDK backend when the binary is built with the
// `ehome_sdk` tag, otherwise the pure-Go UDP listener.
func NewServer(cfg config.EHOMEConfig, db *gorm.DB) *Server {
	if sdk, err := newSDKBackend(cfg, db); err == nil && sdk != nil {
		log.Printf("[ehome] using Hikvision EHOME/ISUP SDK backend")
		return &Server{backend: sdk}
	} else if err != nil {
		log.Printf("[ehome] SDK backend unavailable (%v), using UDP listener", err)
	}
	return &Server{backend: newUDPBackend(cfg, db)}
}

// Start starts the selected backend.
func (s *Server) Start() error { return s.backend.Start() }

// Stop stops the EHOME/ISUP intake endpoint.
func (s *Server) Stop() { s.backend.Stop() }

// Running reports whether the intake endpoint is active.
func (s *Server) Running() bool { return s.backend.Running() }
