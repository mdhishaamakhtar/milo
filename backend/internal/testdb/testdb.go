// Package testdb gives each test its own migrated Postgres database.
//
// By default the first test in a package starts a throwaway postgres:17
// container (Testcontainers; Docker must be running) and every test gets a
// fresh database inside it. Testcontainers' reaper removes the container when
// the test process exits. Set MILO_TEST_DATABASE_URL to use an existing server
// instead, e.g. the compose one from `make up` for faster local runs.
package testdb

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
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mdhishaamakhtar/milo/backend/internal/db"
)

const image = "postgres:17"

var (
	serverOnce sync.Once
	serverURL  string
	serverErr  error
)

// server returns the URL of the Postgres server tests create databases on.
func server() (string, error) {
	serverOnce.Do(func() {
		if u := os.Getenv("MILO_TEST_DATABASE_URL"); u != "" {
			serverURL = u
			return
		}
		ctx := context.Background()
		c, err := postgres.Run(ctx, image,
			postgres.WithDatabase("milo"),
			postgres.WithUsername("milo"),
			postgres.WithPassword("milo"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			serverErr = err
			return
		}
		serverURL, serverErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	return serverURL, serverErr
}

// New creates a fresh database, migrates it and drops it when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := NewWithURL(t)
	return pool
}

// NewWithURL is New that also returns the database's connection URL.
func NewWithURL(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()

	base, err := server()
	if err != nil {
		t.Fatalf("testdb: can't start Postgres (is Docker running?): %v", err)
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("testdb: can't reach Postgres at %s: %v", base, err)
	}

	name := "milo_test_" + randomHex(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("testdb: parse url: %v", err)
	}
	u.Path = "/" + name
	dbURL := u.String()

	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("testdb: open: %v", err)
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
		t.Fatalf("testdb: random: %v", err)
	}
	return hex.EncodeToString(b)
}
