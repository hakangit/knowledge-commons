package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string, maxConnections int32) (*Store, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	poolConfig.MaxConns = maxConnections

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, err
	}

	store := &Store{pool: pool}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

func (store *Store) Close() {
	store.pool.Close()
}

func (store *Store) Migrate(ctx context.Context) error {
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock(hashtext('knowledge-commons:migrations'))"); err != nil {
		return err
	}
	defer connection.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext('knowledge-commons:migrations'))")

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := store.applyMigration(ctx, connection, entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) applyMigration(ctx context.Context, connection *pgxpool.Conn, name string) error {
	var applied bool
	if err := connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)", name).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return nil
	}

	contents, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}

	transaction, err := connection.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(context.Background())

	if _, err := transaction.Exec(ctx, string(contents)); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := transaction.Exec(ctx, "INSERT INTO schema_migrations (name) VALUES ($1)", name); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}
