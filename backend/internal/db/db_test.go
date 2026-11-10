package db_test

import (
	"context"
	"testing"

	"github.com/mdhishaamakhtar/milo/backend/internal/db"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

func TestOpenMigratesAndSeeds(t *testing.T) {
	ctx := context.Background()
	pool, url := testkit.DB(t)
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
