package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/internal/db"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

func TestOpenMigratesAndSeeds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	url := testkit.EmptyDB(t)
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := query.New(pool)

	count, err := q.CountBuiltinExercises(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count < 150 {
		t.Fatalf("built-in exercises = %d, want at least 150", count)
	}
	bench, err := q.GetBuiltinExerciseByName(ctx, "Bench Press (Barbell)")
	if err != nil {
		t.Fatalf("Bench Press (Barbell) missing: %v", err)
	}
	if bench.Measurement != "reps_load" {
		t.Fatalf("Bench Press measurement = %q, want reps_load", bench.Measurement)
	}
	if _, err := q.GetBuiltinExerciseByName(ctx, "Landmine Rotation"); err == nil {
		t.Fatal("Landmine Rotation must not be built in: Milo creates it as a custom exercise")
	}

	// Opening again runs nothing: the library is inserted once.
	before := bench.ServerSeq
	again, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	count2, err := query.New(again).CountBuiltinExercises(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bench2, err := query.New(again).GetBuiltinExerciseByName(ctx, "Bench Press (Barbell)")
	if err != nil {
		t.Fatal(err)
	}
	if count2 != count || bench2.ServerSeq != before {
		t.Fatalf("second Open changed built-ins: count %d→%d, server_seq %d→%d", count, count2, before, bench2.ServerSeq)
	}
}

// The API doesn't start on a database it can't use, and says why.
func TestOpenFailsClearly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := db.Open(ctx, "postgres://milo:milo@127.0.0.1:1/milo?sslmode=disable&connect_timeout=2"); err == nil || !strings.HasPrefix(err.Error(), "ping:") {
		t.Fatalf("nothing listening: got %v, want a ping error", err)
	}

	// A migration that fails stops the start.
	url := testkit.EmptyDB(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "CREATE TABLE exercises (id int)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Open(ctx, url); err == nil || !strings.HasPrefix(err.Error(), "migrate:") {
		t.Fatalf("got %v, want a migrate error", err)
	}
}
