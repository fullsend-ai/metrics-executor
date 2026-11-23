package config

import (
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestDashboardDSN_SpecialCharactersRoundTrip(t *testing.T) {
	cfg := Config{
		DashboardDBUser:     "user@corp",
		DashboardDBPassword: "p@ss:w/rd?&x=1",
		DashboardDBHost:     "db.example.internal",
		DashboardDBPort:     "3306",
		DashboardDBName:     "dashboard/prod",
	}

	dsn, err := cfg.DashboardDSN()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("generated DSN does not parse: %v", err)
	}
	if parsed.User != cfg.DashboardDBUser {
		t.Errorf("user = %q, want %q", parsed.User, cfg.DashboardDBUser)
	}
	if parsed.Passwd != cfg.DashboardDBPassword {
		t.Errorf("password did not round-trip through the DSN")
	}
	if parsed.DBName != cfg.DashboardDBName {
		t.Errorf("db name = %q, want %q", parsed.DBName, cfg.DashboardDBName)
	}
	if parsed.Addr != "db.example.internal:3306" {
		t.Errorf("addr = %q, want db.example.internal:3306", parsed.Addr)
	}
	if !parsed.ParseTime {
		t.Error("expected parseTime to be enabled")
	}
}

func TestLoad_RuntimeLimitsAndLogging(t *testing.T) {
	t.Setenv("REQUEST_TIMEOUT_SECONDS", "45")
	t.Setenv("MAX_CONCURRENT_EXECUTIONS", "7")
	t.Setenv("LOG_CLIENT_IP", "true")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.example, https://b.example")

	cfg := Load()

	if cfg.RequestTimeout.Seconds() != 45 {
		t.Errorf("RequestTimeout = %v, want 45s", cfg.RequestTimeout)
	}
	if cfg.MaxConcurrentExecutions != 7 {
		t.Errorf("MaxConcurrentExecutions = %d, want 7", cfg.MaxConcurrentExecutions)
	}
	if !cfg.LogClientIP {
		t.Error("LogClientIP should be enabled")
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://a.example" || cfg.AllowedOrigins[1] != "https://b.example" {
		t.Errorf("AllowedOrigins = %v, want two origins", cfg.AllowedOrigins)
	}
}

func TestLoad_DefaultsPreserveCompatibility(t *testing.T) {
	for _, key := range []string{"REQUEST_TIMEOUT_SECONDS", "MAX_CONCURRENT_EXECUTIONS", "LOG_CLIENT_IP", "CORS_ALLOWED_ORIGINS"} {
		t.Setenv(key, "")
	}

	cfg := Load()

	if cfg.RequestTimeout.Seconds() != 300 {
		t.Errorf("RequestTimeout = %v, want 300s", cfg.RequestTimeout)
	}
	if cfg.MaxConcurrentExecutions != 32 {
		t.Errorf("MaxConcurrentExecutions = %d, want 32", cfg.MaxConcurrentExecutions)
	}
	if cfg.LogClientIP {
		t.Error("LogClientIP must default to false")
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "*" {
		t.Errorf("AllowedOrigins = %v, want wildcard default", cfg.AllowedOrigins)
	}
}

func TestParseAllowedOrigins_Disable(t *testing.T) {
	for _, value := range []string{"none", "NONE", "-"} {
		if got := parseAllowedOrigins(value); len(got) != 0 {
			t.Errorf("parseAllowedOrigins(%q) = %v, want disabled", value, got)
		}
	}
}
