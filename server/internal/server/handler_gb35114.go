package server

import (
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/easyavr/easyavr/internal/gb35114"
)

func (a *App) gb35114Config(c *gin.Context) {
	if a.gb35114 == nil {
		ok(c, gin.H{"enabled": false})
		return
	}
	ok(c, gin.H{
		"enabled":          a.settingBool("gb35114_enabled", a.cfg.GB35114.Enabled),
		"running":          a.gb.TLSAddr() != "",
		"whiteList":        a.cfg.GB35114.WhiteList,
		"certDir":          a.cfg.GB35114.CertDir,
		"certReady":        a.gb35114.CertReady(),
		"sipListen":        a.cfg.GB35114.SIPListen,
		"sipRequireClient": a.cfg.GB35114.SIPRequireClient,
	})
}

// gb35114GenerateCert generates the platform SM2 CA and returns it as PEM.
func (a *App) gb35114GenerateCert(c *gin.Context) {
	if a.gb35114 == nil {
		fail(c, http.StatusBadRequest, "GB35114 is disabled")
		return
	}
	cert, key, err := a.gb35114.PlatformCert()
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"cert": string(cert), "key": string(key)})
}

type certRequest struct {
	Cert string `json:"cert"`
}

// gb35114Enroll signs a CSR and records the issued device certificate.
func (a *App) gb35114Enroll(c *gin.Context) {
	if a.gb35114 == nil {
		fail(c, http.StatusBadRequest, "GB35114 is disabled")
		return
	}
	var req struct {
		CSR      string `json:"csr"`
		DeviceID string `json:"deviceId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.CSR == "" {
		fail(c, http.StatusBadRequest, "csr (PEM) is required")
		return
	}
	cert, rec, err := a.gb35114.EnrollDevice([]byte(req.CSR), req.DeviceID)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"cert": string(cert), "record": rec})
}

// gb35114Verify validates a device certificate against the platform CA.
func (a *App) gb35114Verify(c *gin.Context) {
	if a.gb35114 == nil {
		fail(c, http.StatusBadRequest, "GB35114 is disabled")
		return
	}
	var req certRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Cert == "" {
		fail(c, http.StatusBadRequest, "cert (PEM) is required")
		return
	}
	info, err := a.gb35114.VerifyCert([]byte(req.Cert))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	fp, _ := gb35114.Fingerprint([]byte(req.Cert))
	ok(c, gin.H{"valid": true, "info": info, "fingerprint": fp})
}

func (a *App) gb35114Certs(c *gin.Context) {
	if a.gb35114 == nil {
		ok(c, []any{})
		return
	}
	ok(c, a.gb35114.ListCerts())
}

func (a *App) gb35114Revoke(c *gin.Context) {
	id, valid := parseUintParam(c, "id")
	if !valid {
		return
	}
	if a.gb35114 == nil {
		fail(c, http.StatusBadRequest, "GB35114 is disabled")
		return
	}
	if err := a.gb35114.RevokeCert(id); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"id": id})
}

type signCSRRequest struct {
	CSR string `json:"csr"`
}

// gb35114SignCSR signs an SM2 device certificate request.
func (a *App) gb35114SignCSR(c *gin.Context) {
	if a.gb35114 == nil {
		fail(c, http.StatusBadRequest, "GB35114 is disabled")
		return
	}
	var req signCSRRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CSR == "" {
		fail(c, http.StatusBadRequest, "csr (PEM) is required")
		return
	}
	cert, err := a.gb35114.SignDeviceCSR([]byte(req.CSR))
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"cert": string(cert)})
}

// gb35114SM3 hashes the provided data with SM3 (utility for verification).
func (a *App) gb35114SM3(c *gin.Context) {
	var req struct {
		Data string `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid body")
		return
	}
	ok(c, gin.H{"sm3": hex.EncodeToString(gb35114.SM3([]byte(req.Data)))})
}
