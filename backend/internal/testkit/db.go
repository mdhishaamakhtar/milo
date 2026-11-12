// Package testkit is what the tests share: a fresh database per test, and the
// whole API running on it.
//
// By default one throwaway postgres:17 container (Testcontainers; Docker must
// be running) serves a whole `go test ./...`: Go tests each package in its own
// process, and they find the container by a name made from the test session.
// Every test gets a fresh database inside it, copied from a migrated template.
// Tests own their database, so they run in parallel. Testcontainers' reaper
// removes the container when the run ends. Set MILO_TEST_DATABASE_URL to use
// an existing server instead, e.g. the compose one from `make up`.
package testkit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mdhishaamakhtar/milo/backend/internal/db"
)

const image = "postgres:17"

var (
	pgOnce sync.Once
	pgURL  string
	pgErr  error

	templateOnce sync.Once
	templateName string
	templateErr  error
)

// postgres returns the URL of the Postgres server tests create databases on.
func postgres() (string, error) {
	pgOnce.Do(func() {
		if u := os.Getenv("MILO_TEST_DATABASE_URL"); u != "" {
			pgURL = u
			return
		}
		ctx := context.Background()
		c, err := tcpostgres.Run(ctx, image,
			testcontainers.WithReuseByName("milo-test-postgres-"+testcontainers.SessionID()[:12]),
			tcpostgres.WithDatabase("milo"),
			tcpostgres.WithUsername("milo"),
			tcpostgres.WithPassword("milo"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			pgErr = err
			return
		}
		pgURL, pgErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	return pgURL, pgErr
}

// DB opens a fresh, migrated database that is dropped when the test ends. It
// also returns the database's URL.
func DB(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	dbURL := database(t, true)
	pool, err := db.Open(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("testkit: open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, dbURL
}

// EmptyDB returns the URL of a fresh database with nothing in it, for testing
// the migrations themselves. It is dropped when the test ends.
func EmptyDB(t testing.TB) string {
	t.Helper()
	return database(t, false)
}

// database creates a database, copied from the migrated template or empty.
func database(t testing.TB, migrated bool) string {
	t.Helper()
	ctx := context.Background()
	base, err := postgres()
	if err != nil {
		t.Fatalf("testkit: can't start Postgres (is Docker running?): %v", err)
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testkit: can't reach Postgres at %s: %v", base, err)
	}
	t.Cleanup(func() { _ = admin.Close(ctx) })

	name := "milo_test_" + randomHex(t)
	create := "CREATE DATABASE " + name
	if migrated {
		templateOnce.Do(func() { templateName, templateErr = migratedTemplate(ctx, admin, base) })
		if templateErr != nil {
			t.Fatalf("testkit: template database: %v", templateErr)
		}
		create += " TEMPLATE " + templateName
	}
	if _, err := admin.Exec(ctx, create); err != nil {
		t.Fatalf("testkit: create database: %v", err)
	}
	// Registered after the admin connection's Close, so it runs first.
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)") })
	dbURL, err := withDatabase(base, name)
	if err != nil {
		t.Fatalf("testkit: %v", err)
	}
	return dbURL
}

func randomHex(t testing.TB) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testkit: random: %v", err)
	}
	return hex.EncodeToString(b)
}

// migratedTemplate returns a database with every migration applied, for each test's
// database to be copied from: a copy takes milliseconds, migrating takes most of a
// second. Its name carries a hash of the migrations, so it is made again when they
// change, and test processes for other packages share it.
func migratedTemplate(ctx context.Context, admin *pgx.Conn, base string) (string, error) {
	sum, err := migrationsHash()
	if err != nil {
		return "", err
	}
	name := "milo_template_" + sum
	// Go tests each package in its own process, at the same time: one of them makes it.
	if _, err := admin.Exec(ctx, "SELECT pg_advisory_lock(hashtext($1))", name); err != nil {
		return "", err
	}
	defer func() { _, _ = admin.Exec(ctx, "SELECT pg_advisory_unlock(hashtext($1))", name) }()

	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil || exists {
		return name, err
	}
	// Built under another name and renamed when done, so a run stopped halfway leaves no half-migrated template.
	building := name + "_building"
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+building); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+building); err != nil {
		return "", err
	}
	dbURL, err := withDatabase(base, building)
	if err != nil {
		return "", err
	}
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		return "", err
	}
	pool.Close()
	_, err = admin.Exec(ctx, "ALTER DATABASE "+building+" RENAME TO "+name)
	return name, err
}

// migrationsHash is a short hash of the migration files, read from the source tree.
func migrationsHash() (string, error) {
	_, here, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(here), "..", "db", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("find migrations: %v", err)
	}
	slices.Sort(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

func withDatabase(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
