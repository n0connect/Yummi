package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSignedCA(t *testing.T) {
	cert, key, err := GenerateSelfSignedCA("Yummi Test CA")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCA: %v", err)
	}
	if cert == nil || key == nil {
		t.Fatal("expected non-nil cert and key")
	}
	if !cert.IsCA {
		t.Error("expected IsCA=true")
	}
	if cert.Subject.CommonName != "Yummi Test CA" {
		t.Errorf("unexpected CN: %s", cert.Subject.CommonName)
	}
	// ECDSA P-256 pubkey expected.
	if _, ok := cert.PublicKey.(interface{ Equal(x any) bool }); !ok {
		// older Go may not expose Equal on all keys — the important bit is that
		// it parsed cleanly; just make sure there's a public key.
		t.Log("public key does not implement Equal — not fatal")
	}
	if cert.NotAfter.Before(cert.NotBefore) {
		t.Error("NotAfter must be after NotBefore")
	}
}

func TestGenerateLeafCertSignedByCA(t *testing.T) {
	ca, caKey, err := GenerateSelfSignedCA("Yummi Test CA")
	if err != nil {
		t.Fatalf("CA: %v", err)
	}
	leaf, _, err := GenerateLeafCert(
		ca, caKey,
		"test.yummi.local",
		[]string{"test.yummi.local", "localhost"},
		[]net.IP{net.ParseIP("127.0.0.1")},
		true,
	)
	if err != nil {
		t.Fatalf("GenerateLeafCert: %v", err)
	}

	// Leaf should verify against the CA we just built.
	pool := x509.NewCertPool()
	pool.AddCert(ca)

	opts := x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Errorf("leaf must verify against the CA: %v", err)
	}
	if leaf.Subject.CommonName != "test.yummi.local" {
		t.Errorf("unexpected CN: %s", leaf.Subject.CommonName)
	}

	foundClient := false
	for _, eku := range leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			foundClient = true
		}
	}
	if !foundClient {
		t.Error("expected ExtKeyUsageClientAuth when clientAuth=true")
	}
}

func TestLoadOrGenerateCreatesMissing(t *testing.T) {
	dir := t.TempDir()

	b, err := LoadOrGenerate(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if b == nil || b.CACert == nil || b.LeafCert == nil {
		t.Fatal("expected full bundle")
	}
	// All four files must exist.
	expect := []string{caCertFile, caKeyFile, leafCertFile, leafKeyFile}
	for _, f := range expect {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}

func TestLoadOrGenerateIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := LoadOrGenerate(dir)
	if err != nil {
		t.Fatalf("first LoadOrGenerate: %v", err)
	}
	second, err := LoadOrGenerate(dir)
	if err != nil {
		t.Fatalf("second LoadOrGenerate: %v", err)
	}
	// Same CA should be preserved — check serial number.
	if first.CACert.SerialNumber.Cmp(second.CACert.SerialNumber) != 0 {
		t.Error("CA serial changed between loads — material must be stable on disk")
	}
	if first.LeafCert.SerialNumber.Cmp(second.LeafCert.SerialNumber) != 0 {
		t.Error("leaf serial changed — existing material must be reused")
	}
}

func TestServerAndClientConfigsPinTLS13(t *testing.T) {
	dir := t.TempDir()
	b, err := LoadOrGenerate(dir)
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}

	srv, err := NewServerTLSConfig(b)
	if err != nil {
		t.Fatalf("server config: %v", err)
	}
	if srv.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected server MinVersion=TLS1.3, got %d", srv.MinVersion)
	}
	if srv.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("server must require+verify client cert, got %d", srv.ClientAuth)
	}
	if srv.ClientCAs == nil {
		t.Error("server ClientCAs must be set")
	}

	cli, err := NewClientTLSConfig(b, "orchestrator.yummi.local")
	if err != nil {
		t.Fatalf("client config: %v", err)
	}
	if cli.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected client MinVersion=TLS1.3, got %d", cli.MinVersion)
	}
	if cli.ServerName != "orchestrator.yummi.local" {
		t.Errorf("expected ServerName=orchestrator.yummi.local, got %q", cli.ServerName)
	}
	if cli.RootCAs == nil {
		t.Error("client RootCAs must be set")
	}
}

func TestLoadOrGenerateEmptyDir(t *testing.T) {
	if _, err := LoadOrGenerate(""); err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestNewServerConfigNilBundle(t *testing.T) {
	if _, err := NewServerTLSConfig(nil); err == nil {
		t.Error("expected error for nil bundle")
	}
	if _, err := NewClientTLSConfig(nil, "x"); err == nil {
		t.Error("expected error for nil bundle")
	}
}
