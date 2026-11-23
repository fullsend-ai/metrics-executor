package config

import (
	"log/slog"
	"testing"
	"time"
)

// clearEnv removes every variable Load reads so the test sees the real
// defaults, not values inherited from the developer's shell.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "REFRESH_INTERVAL_SECONDS", "SCRIPT_TIMEOUT_SECONDS",
		"REQUEST_TIMEOUT_SECONDS", "MAX_CONCURRENT_EXECUTIONS",
		"CORS_ALLOWED_ORIGINS", "LOG_CLIENT_IP",
		"LOG_LEVEL", "LOG_FORMAT",
		"DASHBOARD_DB_HOST", "DASHBOARD_DB_PORT", "DASHBOARD_DB_USER",
		"DASHBOARD_DB_PASSWORD", "DASHBOARD_DB_NAME", "DASHBOARD_DB_CA_PATH",
		"DEVLAKE_DB_HOST", "DEVLAKE_DB_PORT", "DEVLAKE_DB_USER",
		"DEVLAKE_DB_PASSWORD", "DEVLAKE_DB_NAME", "DEVLAKE_DB_CA_PATH",
	} {
		t.Setenv(key, "")
	}
}

// TestLoad_PreExistingContractDefaults pins the settings that existed before
// the metrics-executor rename. These values must not drift, because existing
// staging manifests rely on them when a variable is unset.
func TestLoad_PreExistingContractDefaults(t *testing.T) {
	clearEnv(t)
	cfg := Load()

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, "7070"},
		{"RefreshInterval", cfg.RefreshInterval, 60 * time.Second},
		{"ScriptTimeout", cfg.ScriptTimeout, 30 * time.Second},
		{"LogLevel", cfg.LogLevel, slog.LevelInfo},
		{"LogFormat", cfg.LogFormat, "json"},
		{"DashboardDBHost", cfg.DashboardDBHost, "localhost"},
		{"DashboardDBPort", cfg.DashboardDBPort, "3306"},
		{"DashboardDBName", cfg.DashboardDBName, "dashboard"},
		{"DevlakeDBHost", cfg.DevlakeDBHost, "localhost"},
		{"DevlakeDBPort", cfg.DevlakeDBPort, "3306"},
		{"DevlakeDBName", cfg.DevlakeDBName, "devlake"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestLoad_NewSettingsKeepOldBehaviour documents that the new safeguards are
// backwards compatible: CORS stays open and client IP logging stays off.
func TestLoad_NewSettingsKeepOldBehaviour(t *testing.T) {
	clearEnv(t)
	cfg := Load()

	if cfg.RequestTimeout != 300*time.Second {
		t.Errorf("RequestTimeout = %v, want 300s", cfg.RequestTimeout)
	}
	if cfg.MaxConcurrentExecutions != 32 {
		t.Errorf("MaxConcurrentExecutions = %d, want 32", cfg.MaxConcurrentExecutions)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "*" {
		t.Errorf("AllowedOrigins = %v, want wildcard", cfg.AllowedOrigins)
	}
	if cfg.LogClientIP {
		t.Error("LogClientIP must default to false to preserve the old privacy behaviour")
	}
}

func TestHasDB_RequiresBothDatabases(t *testing.T) {
	base := Config{
		DashboardDBUser: "dash", DashboardDBName: "dashboard",
		DevlakeDBUser: "lake", DevlakeDBName: "devlake",
	}
	if !base.HasDB() {
		t.Fatal("complete configuration should enable workflows")
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing dashboard user", func(c *Config) { c.DashboardDBUser = "" }},
		{"missing dashboard name", func(c *Config) { c.DashboardDBName = "" }},
		{"missing devlake user", func(c *Config) { c.DevlakeDBUser = "" }},
		{"missing devlake name", func(c *Config) { c.DevlakeDBName = "" }},
	}
	for _, tt := range tests {
		cfg := base
		tt.mutate(&cfg)
		if cfg.HasDB() {
			t.Errorf("%s: HasDB() = true, want false", tt.name)
		}
	}
}

// TestLoad_EnvOverridesDefaults proves the documented variables still take
// precedence, including the dual database prefixes.
func TestLoad_EnvOverridesDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "8080")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("REFRESH_INTERVAL_SECONDS", "15")
	t.Setenv("SCRIPT_TIMEOUT_SECONDS", "5")
	t.Setenv("DASHBOARD_DB_USER", "dash")
	t.Setenv("DASHBOARD_DB_PASSWORD", "dashpass")
	t.Setenv("DASHBOARD_DB_NAME", "config")
	t.Setenv("DASHBOARD_DB_HOST", "dash-db")
	t.Setenv("DEVLAKE_DB_USER", "lake")
	t.Setenv("DEVLAKE_DB_PASSWORD", "lakepass")
	t.Setenv("DEVLAKE_DB_NAME", "lake")
	t.Setenv("DEVLAKE_DB_HOST", "lake-db")

	cfg := Load()

	if cfg.Port != "8080" || cfg.LogFormat != "text" {
		t.Errorf("server settings not overridden: port=%q format=%q", cfg.Port, cfg.LogFormat)
	}
	if cfg.RefreshInterval != 15*time.Second || cfg.ScriptTimeout != 5*time.Second {
		t.Errorf("durations not overridden: refresh=%v script=%v", cfg.RefreshInterval, cfg.ScriptTimeout)
	}
	if cfg.DashboardDBHost != "dash-db" || cfg.DashboardDBName != "config" || cfg.DevlakeDBHost != "lake-db" || cfg.DevlakeDBName != "lake" {
		t.Errorf("database settings not overridden: %+v", cfg)
	}
	if !cfg.HasDB() {
		t.Error("HasDB() = false with a complete environment")
	}

	dsn, err := cfg.DashboardDSN()
	if err != nil {
		t.Fatalf("DashboardDSN: %v", err)
	}
	if dsn != "dash:dashpass@tcp(dash-db:3306)/config?parseTime=true" {
		t.Errorf("DashboardDSN = %q", dsn)
	}
}
