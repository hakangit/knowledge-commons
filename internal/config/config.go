package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddress            string
	IdentityProvider       string
	IdentityURL            string
	StorageProvider        string
	DataPath               string
	DatabaseURL            string
	DatabaseMaxConnections int32
	ShutdownTimeout        time.Duration
	RestrictedSubjects     []string
	IngestSubjects         []string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddress:            envOrDefault("KC_HTTP_ADDRESS", ":8080"),
		IdentityProvider:       strings.ToLower(envOrDefault("KC_IDENTITY_PROVIDER", "disabled")),
		IdentityURL:            strings.TrimSpace(os.Getenv("KC_IDENTITY_URL")),
		StorageProvider:        strings.ToLower(envOrDefault("KC_STORAGE_PROVIDER", "sqlite")),
		DataPath:               envOrDefault("KC_DATA_PATH", "./knowledge-commons.db"),
		DatabaseURL:            os.Getenv("KC_DATABASE_URL"),
		DatabaseMaxConnections: 20,
		ShutdownTimeout:        10 * time.Second,
		RestrictedSubjects:     splitList(os.Getenv("KC_RESTRICTED_SUBJECTS")),
		IngestSubjects:         splitList(os.Getenv("KC_INGEST_SUBJECTS")),
	}
	if cfg.IdentityProvider != "disabled" && cfg.IdentityProvider != "header" && cfg.IdentityProvider != "remote" {
		return Config{}, fmt.Errorf("KC_IDENTITY_PROVIDER must be disabled, header, or remote")
	}
	if cfg.IdentityProvider == "remote" && cfg.IdentityURL == "" {
		return Config{}, fmt.Errorf("KC_IDENTITY_URL is required for remote identity")
	}

	switch cfg.StorageProvider {
	case "sqlite":
		if strings.TrimSpace(cfg.DataPath) == "" {
			return Config{}, fmt.Errorf("KC_DATA_PATH is required for sqlite storage")
		}
	case "postgres":
		if strings.TrimSpace(cfg.DatabaseURL) == "" {
			return Config{}, fmt.Errorf("KC_DATABASE_URL is required for postgres storage")
		}
	default:
		return Config{}, fmt.Errorf("KC_STORAGE_PROVIDER must be sqlite or postgres")
	}

	if raw := os.Getenv("KC_DATABASE_MAX_CONNECTIONS"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 1 {
			return Config{}, fmt.Errorf("KC_DATABASE_MAX_CONNECTIONS must be a positive integer")
		}
		cfg.DatabaseMaxConnections = int32(value)
	}

	if raw := os.Getenv("KC_SHUTDOWN_TIMEOUT"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("KC_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		cfg.ShutdownTimeout = value
	}

	return cfg, nil
}

func splitList(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.ToLower(strings.TrimSpace(item)); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
