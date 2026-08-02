package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hakangit/knowledge-commons/internal/config"
	"github.com/hakangit/knowledge-commons/internal/httpapi"
	"github.com/hakangit/knowledge-commons/internal/identity"
	"github.com/hakangit/knowledge-commons/internal/knowledge"
	postgresstore "github.com/hakangit/knowledge-commons/internal/storage/postgres"
	sqlitestore "github.com/hakangit/knowledge-commons/internal/storage/sqlite"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "source" && os.Args[2] == "sync" {
		if err := syncSource(os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := run(); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := openStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate storage: %w", err)
	}

	knowledgeService := knowledge.NewService(store)
	var sourceService httpapi.SourceOperations
	if _, ok := store.(knowledge.SourceRepository); ok {
		sourceService = knowledgeService
	}
	principalResolver := identity.Resolver(identity.DisabledResolver{})
	switch cfg.IdentityProvider {
	case "header":
		principalResolver = identity.HeaderResolver{}
	case "remote":
		principalResolver, err = identity.NewRemoteResolver(cfg.IdentityURL, nil)
		if err != nil {
			return fmt.Errorf("configure identity: %w", err)
		}
	}
	server := httpapi.NewWithSources(
		cfg.HTTPAddress, version, store, knowledgeService, sourceService,
		principalResolver, httpapi.NewAccessPolicy(cfg.RestrictedSubjects, cfg.IngestSubjects),
	)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	slog.Info(
		"knowledge commons is ready",
		"address", cfg.HTTPAddress,
		"storage", cfg.StorageProvider,
		"version", version,
	)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

type storage interface {
	knowledge.Repository
	Ping(context.Context) error
	Migrate(context.Context) error
	Close()
}

func openStore(ctx context.Context, cfg config.Config) (storage, error) {
	switch cfg.StorageProvider {
	case "sqlite":
		return sqlitestore.Open(ctx, cfg.DataPath)
	case "postgres":
		return postgresstore.Open(ctx, cfg.DatabaseURL, cfg.DatabaseMaxConnections)
	default:
		return nil, fmt.Errorf("unsupported storage provider %q", cfg.StorageProvider)
	}
}

func healthcheck() error {
	url := os.Getenv("KC_HEALTH_URL")
	if url == "" {
		url = "http://127.0.0.1:8080/readyz"
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("health request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return errors.New(response.Status)
	}
	return nil
}
