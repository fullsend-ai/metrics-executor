package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardDSN_Plain(t *testing.T) {
	cfg := Config{
		DashboardDBUser:     "user",
		DashboardDBPassword: "pass",
		DashboardDBHost:     "localhost",
		DashboardDBPort:     "3306",
		DashboardDBName:     "dashboard",
		DashboardDBCAPath:   "",
	}

	dsn, err := cfg.DashboardDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "user:pass@tcp(localhost:3306)/dashboard?parseTime=true"
	if dsn != expected {
		t.Errorf("expected %q, got %q", expected, dsn)
	}
}

func TestDevlakeDSN_Plain(t *testing.T) {
	cfg := Config{
		DevlakeDBUser:     "user",
		DevlakeDBPassword: "pass",
		DevlakeDBHost:     "localhost",
		DevlakeDBPort:     "3306",
		DevlakeDBName:     "devlake",
		DevlakeDBCAPath:   "",
	}

	dsn, err := cfg.DevlakeDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "user:pass@tcp(localhost:3306)/devlake?parseTime=true"
	if dsn != expected {
		t.Errorf("expected %q, got %q", expected, dsn)
	}
}

func TestDashboardDSN_WithCA(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")

	cert, key, err := generateSelfSignedCA()
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}

	if err := writeCertAndKey(caPath, cert, key); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	cfg := Config{
		DashboardDBUser:     "user",
		DashboardDBPassword: "pass",
		DashboardDBHost:     "localhost",
		DashboardDBPort:     "3306",
		DashboardDBName:     "dashboard",
		DashboardDBCAPath:   caPath,
	}

	dsn, err := cfg.DashboardDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(dsn, "&tls=dashboard-tls") {
		t.Errorf("expected DSN to contain &tls=dashboard-tls, got %q", dsn)
	}
}

func TestDevlakeDSN_WithCA(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")

	cert, key, err := generateSelfSignedCA()
	if err != nil {
		t.Fatalf("failed to generate CA: %v", err)
	}

	if err := writeCertAndKey(caPath, cert, key); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	cfg := Config{
		DevlakeDBUser:     "user",
		DevlakeDBPassword: "pass",
		DevlakeDBHost:     "localhost",
		DevlakeDBPort:     "3306",
		DevlakeDBName:     "devlake",
		DevlakeDBCAPath:   caPath,
	}

	dsn, err := cfg.DevlakeDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(dsn, "&tls=devlake-tls") {
		t.Errorf("expected DSN to contain &tls=devlake-tls, got %q", dsn)
	}
}

func TestDashboardDSN_CAFilenameotFound(t *testing.T) {
	cfg := Config{
		DashboardDBUser:     "user",
		DashboardDBPassword: "pass",
		DashboardDBHost:     "localhost",
		DashboardDBPort:     "3306",
		DashboardDBName:     "dashboard",
		DashboardDBCAPath:   "/nonexistent/path/ca.pem",
	}

	_, err := cfg.DashboardDSN()
	if err == nil {
		t.Error("expected error for non-existent CA file")
	}
}

func TestDevlakeDSN_CAFilenameotFound(t *testing.T) {
	cfg := Config{
		DevlakeDBUser:     "user",
		DevlakeDBPassword: "pass",
		DevlakeDBHost:     "localhost",
		DevlakeDBPort:     "3306",
		DevlakeDBName:     "devlake",
		DevlakeDBCAPath:   "/nonexistent/path/ca.pem",
	}

	_, err := cfg.DevlakeDSN()
	if err == nil {
		t.Error("expected error for non-existent CA file")
	}
}

func TestDashboardDSN_InvalidPEMData(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")

	if err := os.WriteFile(caPath, []byte("not valid pem data"), 0600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	cfg := Config{
		DashboardDBUser:     "user",
		DashboardDBPassword: "pass",
		DashboardDBHost:     "localhost",
		DashboardDBPort:     "3306",
		DashboardDBName:     "dashboard",
		DashboardDBCAPath:   caPath,
	}

	_, err := cfg.DashboardDSN()
	if err == nil {
		t.Error("expected error for invalid PEM data")
	}
}

func TestDevlakeDSN_InvalidPEMData(t *testing.T) {
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")

	if err := os.WriteFile(caPath, []byte("not valid pem data"), 0600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	cfg := Config{
		DevlakeDBUser:     "user",
		DevlakeDBPassword: "pass",
		DevlakeDBHost:     "localhost",
		DevlakeDBPort:     "3306",
		DevlakeDBName:     "devlake",
		DevlakeDBCAPath:   caPath,
	}

	_, err := cfg.DevlakeDSN()
	if err == nil {
		t.Error("expected error for invalid PEM data")
	}
}

func TestLoad_LogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"invalid", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.input)
			if got := Load().LogLevel; got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestLoad_ScriptTimeout(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"", 30 * time.Second},
		{"5", 5 * time.Second},
		{"0", 30 * time.Second},
		{"-1", 30 * time.Second},
		{"abc", 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Setenv("SCRIPT_TIMEOUT_SECONDS", tt.input)
			if got := Load().ScriptTimeout; got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func generateSelfSignedCA() (certPEM, keyPEM []byte, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	return certPEM, keyPEM, nil
}

func writeCertAndKey(path string, certPEM, keyPEM []byte) error {
	return os.WriteFile(path, append(certPEM, keyPEM...), 0600)
}
