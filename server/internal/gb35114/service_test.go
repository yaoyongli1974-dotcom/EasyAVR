package gb35114

import (
	"crypto/rand"
	"crypto/x509/pkix"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjfoc/gmsm/sm2"
	gmx509 "github.com/tjfoc/gmsm/x509"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/store"
)

func TestPlatformCertAndSignCSR(t *testing.T) {
	svc := NewService(nil, config.GB35114Config{Enabled: true, CertDir: filepath.Join(t.TempDir(), "certs")})
	if svc.CertReady() {
		t.Fatal("cert should not exist yet")
	}
	cert, key, err := svc.PlatformCert()
	if err != nil {
		t.Fatalf("platform cert: %v", err)
	}
	if !strings.Contains(string(cert), "CERTIFICATE") || !strings.Contains(string(key), "PRIVATE KEY") {
		t.Fatal("unexpected platform cert/key PEM")
	}
	if !svc.CertReady() {
		t.Fatal("cert should be ready after generation")
	}

	// Create a device SM2 key + CSR and have the platform sign it.
	devKey, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("device key: %v", err)
	}
	csrPEM, err := gmx509.CreateCertificateRequestToPem(
		&gmx509.CertificateRequest{Subject: pkix.Name{CommonName: "device-1"}}, devKey)
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	signed, err := svc.SignDeviceCSR(csrPEM)
	if err != nil {
		t.Fatalf("sign csr: %v", err)
	}
	if !strings.Contains(string(signed), "CERTIFICATE") {
		t.Fatal("signed cert is not PEM")
	}
	if _, err := gmx509.ReadCertificateFromPem(signed); err != nil {
		t.Fatalf("signed cert not parseable: %v", err)
	}

	if got := SM3([]byte("abc")); len(got) != 32 {
		t.Fatalf("SM3 length = %d, want 32", len(got))
	}
}

func TestEnrollAndVerifyDevice(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "gb.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	svc := NewService(db, config.GB35114Config{Enabled: true, CertDir: filepath.Join(t.TempDir(), "certs")})

	devKey, _ := sm2.GenerateKey(rand.Reader)
	csrPEM, _ := gmx509.CreateCertificateRequestToPem(
		&gmx509.CertificateRequest{Subject: pkix.Name{CommonName: "cam-9"}}, devKey)

	certPEM, rec, err := svc.EnrollDevice(csrPEM, "device-9")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if rec.DeviceID != "device-9" || rec.Status != "active" {
		t.Fatalf("bad record: %+v", rec)
	}

	info, err := svc.VerifyCert(certPEM)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !info.Enrolled || info.DeviceID != "device-9" {
		t.Fatalf("verify info: %+v", info)
	}
	fp, err := Fingerprint(certPEM)
	if err != nil || strings.Count(fp, ":") != 31 {
		t.Fatalf("fingerprint = %q (%v)", fp, err)
	}

	// A certificate from another CA must be rejected.
	other := NewService(nil, config.GB35114Config{CertDir: filepath.Join(t.TempDir(), "other")})
	otherPEM, _, err := other.EnrollDevice(csrPEM, "device-9")
	if err != nil {
		t.Fatalf("other enroll: %v", err)
	}
	if _, err := svc.VerifyCert(otherPEM); err == nil {
		t.Fatal("expected foreign certificate to be rejected")
	}

	// Revocation is enforced.
	if err := svc.RevokeCert(rec.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.VerifyCert(certPEM); err != ErrRevoked {
		t.Fatalf("expected ErrRevoked, got %v", err)
	}
}
