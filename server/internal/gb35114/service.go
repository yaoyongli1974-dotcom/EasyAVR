// Package gb35114 provides the cryptographic foundation for GB35114 device
// access: an SM2 platform CA whose certificate can be installed on devices and
// used to sign device certificates, plus SM3 hashing. It uses the gmsm
// (国密) library.
package gb35114

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/tjfoc/gmsm/sm2"
	"github.com/tjfoc/gmsm/sm3"
	gmx509 "github.com/tjfoc/gmsm/x509"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
)

// ErrRevoked is returned when a device certificate has been revoked.
var ErrRevoked = errors.New("certificate revoked")

// CertInfo summarizes a verified device certificate.
type CertInfo struct {
	DeviceID  string    `json:"deviceId"`
	Serial    string    `json:"serial"`
	Subject   string    `json:"subject"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	Enrolled  bool      `json:"enrolled"`
}

// Service manages the platform's SM2 CA and device certificate signing.
type Service struct {
	cfg config.GB35114Config
	db  *gorm.DB
}

func NewService(db *gorm.DB, cfg config.GB35114Config) *Service {
	if cfg.CertDir != "" {
		_ = os.MkdirAll(cfg.CertDir, 0o700)
	}
	return &Service{cfg: cfg, db: db}
}

func (s *Service) platformCertPath() string { return filepath.Join(s.cfg.CertDir, "platform.crt") }
func (s *Service) platformKeyPath() string  { return filepath.Join(s.cfg.CertDir, "platform.key") }

// SM3 returns the SM3 digest of data.
func SM3(data []byte) []byte { return sm3.Sm3Sum(data) }

// CertReady reports whether a platform certificate has been generated.
func (s *Service) CertReady() bool {
	_, err := os.Stat(s.platformCertPath())
	return err == nil
}

// PlatformCert returns (generating on first call) the platform CA cert/key PEM.
func (s *Service) PlatformCert() (certPEM, keyPEM []byte, err error) {
	if cert, e := os.ReadFile(s.platformCertPath()); e == nil {
		key, _ := os.ReadFile(s.platformKeyPath())
		return cert, key, nil
	}
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &gmx509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "EasyAVR GB35114 Platform", Organization: []string{"EasyAVR"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              gmx509.KeyUsageDigitalSignature | gmx509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SignatureAlgorithm:    gmx509.SM2WithSM3,
	}
	der, err := gmx509.CreateCertificate(tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := gmx509.MarshalSm2UnecryptedPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(s.platformCertPath(), certPEM, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(s.platformKeyPath(), keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// SignDeviceCSR signs an SM2 certificate request with the platform CA.
func (s *Service) SignDeviceCSR(csrPEM []byte) ([]byte, error) {
	caCertPEM, caKeyPEM, err := s.PlatformCert()
	if err != nil {
		return nil, err
	}
	caCert, err := gmx509.ReadCertificateFromPem(caCertPEM)
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	caKey, err := gmx509.ReadPrivateKeyFromPem(caKeyPEM, nil)
	if err != nil {
		return nil, fmt.Errorf("read ca key: %w", err)
	}
	csr, err := gmx509.ReadCertificateRequestFromPem(csrPEM)
	if err != nil {
		return nil, fmt.Errorf("read csr: %w", err)
	}
	pub, ok := toSM2PublicKey(csr.PublicKey)
	if !ok {
		return nil, fmt.Errorf("csr does not contain an SM2 public key (%T)", csr.PublicKey)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &gmx509.Certificate{
		SerialNumber:       serial,
		Subject:            csr.Subject,
		NotBefore:          time.Now().Add(-time.Hour),
		NotAfter:           time.Now().AddDate(5, 0, 0),
		KeyUsage:           gmx509.KeyUsageDigitalSignature | gmx509.KeyUsageKeyEncipherment,
		SignatureAlgorithm: gmx509.SM2WithSM3,
	}
	der, err := gmx509.CreateCertificate(tmpl, caCert, pub, caKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// TLSPair is a dual SM2 certificate pair (signature + encipherment) as required
// by GM/T 0024 TLS used by GB35114.
type TLSPair struct {
	SignCertPEM []byte
	SignKeyPEM  []byte
	EncCertPEM  []byte
	EncKeyPEM   []byte
}

func (s *Service) tlsPath(name string) string { return filepath.Join(s.cfg.CertDir, name) }

// PlatformTLS returns (generating on first call) the platform's dual SM2 TLS
// certificate, signed by the platform CA.
func (s *Service) PlatformTLS() (*TLSPair, error) {
	pair := &TLSPair{}
	var err error
	if pair.SignCertPEM, pair.SignKeyPEM, err = s.loadOrIssue("platform_tls_sign", "EasyAVR GB35114 SIP", false); err != nil {
		return nil, err
	}
	if pair.EncCertPEM, pair.EncKeyPEM, err = s.loadOrIssue("platform_tls_enc", "EasyAVR GB35114 SIP Enc", true); err != nil {
		return nil, err
	}
	return pair, nil
}

// IssueDeviceTLS issues a fresh dual SM2 TLS certificate for a device CN.
func (s *Service) IssueDeviceTLS(cn string) (*TLSPair, error) {
	sign, signKey, err := s.issueCert(cn, false)
	if err != nil {
		return nil, err
	}
	enc, encKey, err := s.issueCert(cn+"-enc", true)
	if err != nil {
		return nil, err
	}
	return &TLSPair{SignCertPEM: sign, SignKeyPEM: signKey, EncCertPEM: enc, EncKeyPEM: encKey}, nil
}

// CAPool returns the platform CA as a certificate pool for verifying peers.
func (s *Service) CAPool() (*gmx509.CertPool, error) {
	certPEM, _, err := s.PlatformCert()
	if err != nil {
		return nil, err
	}
	pool := gmx509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		return nil, errors.New("failed to load platform CA into pool")
	}
	return pool, nil
}

func (s *Service) loadOrIssue(name, cn string, enc bool) (certPEM, keyPEM []byte, err error) {
	certPEM, certErr := os.ReadFile(s.tlsPath(name + ".crt"))
	keyPEM, keyErr := os.ReadFile(s.tlsPath(name + ".key"))
	if certErr == nil && keyErr == nil {
		return certPEM, keyPEM, nil
	}
	certPEM, keyPEM, err = s.issueCert(cn, enc)
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(s.tlsPath(name+".crt"), certPEM, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(s.tlsPath(name+".key"), keyPEM, 0o600); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// issueCert creates an SM2 keypair and signs a leaf certificate with the
// platform CA.
func (s *Service) issueCert(cn string, enc bool) (certPEM, keyPEM []byte, err error) {
	caCertPEM, caKeyPEM, err := s.PlatformCert()
	if err != nil {
		return nil, nil, err
	}
	caCert, err := gmx509.ReadCertificateFromPem(caCertPEM)
	if err != nil {
		return nil, nil, err
	}
	caKey, err := gmx509.ReadPrivateKeyFromPem(caKeyPEM, nil)
	if err != nil {
		return nil, nil, err
	}
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	usage := gmx509.KeyUsageDigitalSignature
	if enc {
		usage = gmx509.KeyUsageKeyEncipherment | gmx509.KeyUsageDataEncipherment
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &gmx509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: cn, Organization: []string{"EasyAVR"}},
		NotBefore:          time.Now().Add(-time.Hour),
		NotAfter:           time.Now().AddDate(5, 0, 0),
		KeyUsage:           usage,
		SignatureAlgorithm: gmx509.SM2WithSM3,
	}
	der, err := gmx509.CreateCertificate(tmpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := gmx509.MarshalSm2UnecryptedPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// EnrollDevice signs a device CSR and records the issued certificate so the
// device can later be authenticated (or revoked).
func (s *Service) EnrollDevice(csrPEM []byte, deviceID string) ([]byte, *model.GB35114Cert, error) {
	certPEM, err := s.SignDeviceCSR(csrPEM)
	if err != nil {
		return nil, nil, err
	}
	cert, err := gmx509.ReadCertificateFromPem(certPEM)
	if err != nil {
		return nil, nil, err
	}
	if deviceID == "" {
		deviceID = cert.Subject.CommonName
	}
	rec := &model.GB35114Cert{
		DeviceID:  deviceID,
		Serial:    cert.SerialNumber.String(),
		Subject:   cert.Subject.String(),
		CertPEM:   string(certPEM),
		Status:    "active",
		NotBefore: cert.NotBefore,
		NotAfter:  cert.NotAfter,
	}
	if s.db != nil {
		if err := s.db.Create(rec).Error; err != nil {
			return nil, nil, err
		}
	}
	return certPEM, rec, nil
}

// VerifyCert validates a device certificate against the platform CA and the
// revocation list, returning its metadata.
func (s *Service) VerifyCert(certPEM []byte) (*CertInfo, error) {
	caCertPEM, _, err := s.PlatformCert()
	if err != nil {
		return nil, err
	}
	caCert, err := gmx509.ReadCertificateFromPem(caCertPEM)
	if err != nil {
		return nil, fmt.Errorf("read ca cert: %w", err)
	}
	cert, err := gmx509.ReadCertificateFromPem(certPEM)
	if err != nil {
		return nil, fmt.Errorf("read cert: %w", err)
	}
	if err := cert.CheckSignatureFrom(caCert); err != nil {
		return nil, fmt.Errorf("certificate is not signed by the platform CA: %w", err)
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return nil, errors.New("certificate is outside its validity period")
	}
	info := &CertInfo{
		DeviceID:  cert.Subject.CommonName,
		Serial:    cert.SerialNumber.String(),
		Subject:   cert.Subject.String(),
		NotBefore: cert.NotBefore,
		NotAfter:  cert.NotAfter,
	}
	if s.db != nil {
		var rec model.GB35114Cert
		if err := s.db.Where("serial = ?", info.Serial).First(&rec).Error; err == nil {
			info.Enrolled = true
			info.DeviceID = rec.DeviceID
			if rec.Status == "revoked" {
				return nil, ErrRevoked
			}
		}
	}
	return info, nil
}

// ListCerts returns enrolled device certificates (newest first).
func (s *Service) ListCerts() []model.GB35114Cert {
	var items []model.GB35114Cert
	if s.db != nil {
		s.db.Order("id DESC").Find(&items)
	}
	return items
}

// RevokeCert marks an enrolled certificate as revoked.
func (s *Service) RevokeCert(id uint) error {
	if s.db == nil {
		return errors.New("no database")
	}
	now := time.Now()
	return s.db.Model(&model.GB35114Cert{}).Where("id = ?", id).
		Updates(map[string]any{"status": "revoked", "revoked_at": now}).Error
}

// Fingerprint returns the colon-separated SM3 fingerprint of a certificate DER.
func Fingerprint(certPEM []byte) (string, error) {
	cert, err := gmx509.ReadCertificateFromPem(certPEM)
	if err != nil {
		return "", err
	}
	sum := sm3.Sm3Sum(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = hex.EncodeToString([]byte{b})
	}
	return joinColon(parts), nil
}

func joinColon(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ":"
		}
		out += p
	}
	return out
}

// toSM2PublicKey converts the result of parsing an SM2 CSR (which crypto/x509
// exposes as *ecdsa.PublicKey on the SM2 curve) into a *sm2.PublicKey.
func toSM2PublicKey(v any) (*sm2.PublicKey, bool) {
	switch k := v.(type) {
	case *sm2.PublicKey:
		return k, true
	case *ecdsa.PublicKey:
		return &sm2.PublicKey{Curve: k.Curve, X: k.X, Y: k.Y}, true
	default:
		return nil, false
	}
}
