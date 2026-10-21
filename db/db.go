// Package db holds the platform's migrations and runs any module's.
package db

import (
	"context"
	"embed"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

// Migrations is the platform's schema (users, seed pairs, game config), version table goose_platform.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Migrate runs the goose migrations under "migrations/" in fsys, versioned in their own table, so
// every module (the platform, a ledger driver, each game) migrates on its own numbering. Run the
// platform's first: games reference users and seed_pairs. Every instance migrates on start; the
// session lock lets one run them while the others wait.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, table string) error {
	sub, err := fs.Sub(fsys, "migrations")
	if err != nil {
		return err
	}
	store, err := database.NewStore(database.DialectPostgres, table)
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	p, err := goose.NewProvider("", stdlib.OpenDBFromPool(pool), sub, goose.WithStore(store), goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}
