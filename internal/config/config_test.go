package config

import (
	"testing"
	"time"
)

func TestLoadDefaultsToLocalStorage(t *testing.T) {
	t.Setenv("KC_STORAGE_PROVIDER", "")
	t.Setenv("KC_DATA_PATH", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.StorageProvider != "sqlite" || cfg.DataPath != "./knowledge-commons.db" {
		t.Fatalf("local storage = %q, %q", cfg.StorageProvider, cfg.DataPath)
	}
	if cfg.IdentityProvider != "disabled" {
		t.Fatalf("identity provider = %q", cfg.IdentityProvider)
	}
}

func TestLoadAppliesOperationalOverrides(t *testing.T) {
	t.Setenv("KC_STORAGE_PROVIDER", "postgres")
	t.Setenv("KC_DATABASE_URL", "postgres://example")
	t.Setenv("KC_HTTP_ADDRESS", ":9090")
	t.Setenv("KC_IDENTITY_PROVIDER", "header")
	t.Setenv("KC_DATABASE_MAX_CONNECTIONS", "40")
	t.Setenv("KC_SHUTDOWN_TIMEOUT", "15s")
	t.Setenv("KC_RESTRICTED_SUBJECTS", "Admin, director")
	t.Setenv("KC_INGEST_SUBJECTS", "admin")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.HTTPAddress != ":9090" {
		t.Fatalf("HTTPAddress = %q", cfg.HTTPAddress)
	}
	if cfg.IdentityProvider != "header" {
		t.Fatalf("IdentityProvider = %q", cfg.IdentityProvider)
	}
	if cfg.DatabaseMaxConnections != 40 {
		t.Fatalf("DatabaseMaxConnections = %d", cfg.DatabaseMaxConnections)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if len(cfg.RestrictedSubjects) != 2 || cfg.RestrictedSubjects[0] != "admin" {
		t.Fatalf("RestrictedSubjects = %#v", cfg.RestrictedSubjects)
	}
	if len(cfg.IngestSubjects) != 1 || cfg.IngestSubjects[0] != "admin" {
		t.Fatalf("IngestSubjects = %#v", cfg.IngestSubjects)
	}
}

func TestLoadRequiresDatabaseURLForPostgres(t *testing.T) {
	t.Setenv("KC_STORAGE_PROVIDER", "postgres")
	t.Setenv("KC_DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing database URL to fail")
	}
}

func TestLoadRequiresURLForRemoteIdentity(t *testing.T) {
	t.Setenv("KC_IDENTITY_PROVIDER", "remote")
	t.Setenv("KC_IDENTITY_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected missing remote identity URL to fail")
	}
}
