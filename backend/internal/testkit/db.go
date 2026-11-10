// Package testkit is what the tests share: a fresh database per test, and the
// whole API running on it.
//
// By default the first test in a package starts a throwaway postgres:17
// container (Testcontainers; Docker must be running) and every test gets a
// fresh database inside it. Testcontainers' reaper removes the container when
// the test process exits. Set MILO_TEST_DATABASE_URL to use an existing server
// instead, e.g. the compose one from `make up` for faster local runs.
package testkit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mdhishaamakhtar/milo/backend/internal/db"
)

const image = "postgres:17"

var (
	pgOnce sync.Once
	pgURL  string
	pgErr  error
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

// DB creates a fresh database, migrates it and drops it when the test ends.
// It also returns the database's URL.
func DB(t testing.TB) (*pgxpool.Pool, string) {
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

	name := "milo_test_" + randomHex(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("testkit: create database: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("testkit: parse url: %v", err)
	}
	u.Path = "/" + name
	dbURL := u.String()

	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("testkit: open: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		_ = admin.Close(ctx)
	})
	return pool, dbURL
}

func randomHex(t testing.TB) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("testkit: random: %v", err)
	}
	return hex.EncodeToString(b)
}
