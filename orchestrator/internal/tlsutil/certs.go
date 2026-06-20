// Package tlsutil provides a minimal self-signed CA + leaf certificate flow
// used for mTLS between orchestrator (Go orchestrator) and gateway (Rust trust boundary).
//
// The package is intentionally small: generate a local ECDSA P-256 CA once,
// issue a leaf per service, and load-or-regenerate on startup. Cert material
// lives on disk so operators can inspect/rotate them. Nothing here talks to
// a real PKI — Yummi ships its own local trust root by design.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Default filenames inside the TLS directory. Kept flat on purpose — one dir,
// one role, easy to back up.
const (
	caCertFile   = "ca.crt"
	caKeyFile    = "ca.key"
	leafCertFile = "orchestrator.crt"
	leafKeyFile  = "orchestrator.key"
)

// Bundle is the materialized certificate pair held in memory after
// LoadOrGenerate. All fields are populated; nil means "not loaded".
type Bundle struct {
	CACert    *x509.Certificate
	CACertPEM []byte
	CAKey     *ecdsa.PrivateKey
	LeafCert  *x509.Certificate
	LeafPair  tls.Certificate // parsed certificate ready for tls.Config
}

// GenerateSelfSignedCA creates a new ECDSA P-256 CA valid for ~10 years.
// The returned certificate is self-signed and intended to stay local to a
// single Yummi install.
func GenerateSelfSignedCA(subject string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   subject,
			Organization: []string{"Yummi"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("self-sign CA: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}
	return parsed, key, nil
}

// GenerateLeafCert issues a leaf certificate signed by the provided CA.
// The cert is valid for ~1 year and carries the provided DNS/IP SANs.
// clientAuth=true adds ExtKeyUsageClientAuth so it can be used from orchestrator to
// authenticate to gateway; otherwise the cert is server-only.
func GenerateLeafCert(
	ca *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	commonName string,
	dnsNames []string,
	ipAddresses []net.IP,
	clientAuth bool,
) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	if ca == nil || caKey == nil {
		return nil, nil, errors.New("CA cert and key required")
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate leaf key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	extKeyUsage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if clientAuth {
		extKeyUsage = append(extKeyUsage, x509.ExtKeyUsageClientAuth)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Yummi"},
		},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: extKeyUsage,
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("sign leaf cert: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse leaf cert: %w", err)
	}
	return parsed, key, nil
}

// LoadOrGenerate returns a Bundle from the given directory, generating any
// missing material in place. Existing material on disk is kept as-is so
// operators can rotate by deleting files.
func LoadOrGenerate(dir string) (*Bundle, error) {
	if dir == "" {
		return nil, errors.New("tls dir required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create tls dir: %w", err)
	}

	caCertPath := filepath.Join(dir, caCertFile)
	caKeyPath := filepath.Join(dir, caKeyFile)
	leafCertPath := filepath.Join(dir, leafCertFile)
	leafKeyPath := filepath.Join(dir, leafKeyFile)

	bundle := &Bundle{}

	// CA.
	caCert, caKey, err := loadCAPair(caCertPath, caKeyPath)
	if err != nil {
		return nil, err
	}
	if caCert == nil {
		caCert, caKey, err = GenerateSelfSignedCA("Yummi Local CA")
		if err != nil {
			return nil, err
		}
		if err := writeCertPEM(caCertPath, caCert.Raw); err != nil {
			return nil, err
		}
		if err := writeECKeyPEM(caKeyPath, caKey); err != nil {
			return nil, err
		}
	}
	bundle.CACert = caCert
	bundle.CAKey = caKey
	bundle.CACertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw})

	// Leaf.
	leafPair, leafCert, err := loadLeafPair(leafCertPath, leafKeyPath)
	if err != nil {
		return nil, err
	}
	if leafCert == nil {
		leafCert, leafKey, err := GenerateLeafCert(
			caCert, caKey,
			"orchestrator.yummi.local",
			[]string{"orchestrator.yummi.local", "localhost"},
			[]net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
			true, // orchestrator acts as both server and client (mTLS with gateway)
		)
		if err != nil {
			return nil, err
		}
		if err := writeCertPEM(leafCertPath, leafCert.Raw); err != nil {
			return nil, err
		}
		if err := writeECKeyPEM(leafKeyPath, leafKey); err != nil {
			return nil, err
		}
		pair, err := buildTLSCertificate(leafCert, leafKey)
		if err != nil {
			return nil, err
		}
		bundle.LeafCert = leafCert
		bundle.LeafPair = pair
	} else {
		bundle.LeafCert = leafCert
		bundle.LeafPair = leafPair
	}

	return bundle, nil
}

// NewServerTLSConfig builds a tls.Config for an mTLS server (gateway side or an
// orchestrator-hosted control plane). It requires a client certificate signed by the
// same CA and pins the minimum version to TLS 1.3.
func NewServerTLSConfig(b *Bundle) (*tls.Config, error) {
	if b == nil || b.CACert == nil {
		return nil, errors.New("bundle required")
	}
	pool := x509.NewCertPool()
	pool.AddCert(b.CACert)

	return &tls.Config{
		Certificates: []tls.Certificate{b.LeafPair},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// NewClientTLSConfig builds a tls.Config for the orchestrator side connecting to gateway.
// Trusts only the local CA; client cert is mandatory.
func NewClientTLSConfig(b *Bundle, serverName string) (*tls.Config, error) {
	if b == nil || b.CACert == nil {
		return nil, errors.New("bundle required")
	}
	pool := x509.NewCertPool()
	pool.AddCert(b.CACert)

	return &tls.Config{
		Certificates: []tls.Certificate{b.LeafPair},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// ─── internals ───────────────────────────────────────────────────────────────

func randomSerial() (*big.Int, error) {
	// 128-bit serial — RFC 5280 requires a positive integer ≤ 20 bytes.
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("random serial: %w", err)
	}
	return n, nil
}

func loadCAPair(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certBytes, err := os.ReadFile(certPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read ca cert: %w", err)
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read ca key: %w", err)
	}

	certBlock, _ := pem.Decode(certBytes)
	if certBlock == nil {
		return nil, nil, errors.New("ca cert: no PEM block")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyBytes)
	if keyBlock == nil {
		return nil, nil, errors.New("ca key: no PEM block")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse ca key: %w", err)
	}
	return cert, key, nil
}

func loadLeafPair(certPath, keyPath string) (tls.Certificate, *x509.Certificate, error) {
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, nil, nil
	}
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, nil, nil
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("load leaf pair: %w", err)
	}
	parsed, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("parse leaf cert: %w", err)
	}
	return pair, parsed, nil
}

func buildTLSCertificate(cert *x509.Certificate, key *ecdsa.PrivateKey) (tls.Certificate, error) {
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("marshal ec key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return tls.X509KeyPair(certPEM, keyPEM)
}

func writeCertPEM(path string, der []byte) error {
	block := &pem.Block{Type: "CERTIFICATE", Bytes: der}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}

func writeECKeyPEM(path string, key *ecdsa.PrivateKey) error {
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal ec key: %w", err)
	}
	block := &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	return os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
}
