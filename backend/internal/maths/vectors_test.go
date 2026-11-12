package maths_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
)

// load reads testdata/maths/<file> from the repo root, or from MILO_TESTDATA when
// set: `make cover` runs the tests from a copy of the module, outside the repo.
func load[In, Want any](t *testing.T, file string) []struct {
	Name     string `json:"name"`
	Input    In     `json:"input"`
	Expected Want   `json:"expected"`
} {
	t.Helper()
	dir := os.Getenv("MILO_TESTDATA")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "testdata")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "maths", file))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string `json:"name"`
		Input    In     `json:"input"`
		Expected Want   `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatalf("%s has no cases", file)
	}
	return cases
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// opt compares an optional result with an expected value that may be null.
func opt(t *testing.T, name string, got float64, ok bool, want *float64) {
	t.Helper()
	switch {
	case want == nil && ok:
		t.Errorf("%s: got %v, want none", name, got)
	case want != nil && !ok:
		t.Errorf("%s: got none, want %v", name, *want)
	case want != nil && !near(got, *want):
		t.Errorf("%s: got %v, want %v", name, got, *want)
	}
}

func TestUnitsVectors(t *testing.T) {
	t.Parallel()
	type want struct {
		Kg   float64 `json:"kg"`
		Text string  `json:"text"`
	}
	for _, c := range load[struct {
		Op          string `json:"op"`
		Weight      string `json:"weight"`
		Unit        string `json:"unit"`
		DisplayUnit string `json:"display_unit"`
	}, want](t, "units.json") {
		switch c.Input.Op {
		case "to_kg":
			got, ok := maths.ToKg(c.Input.Weight, c.Input.Unit)
			if !ok || !near(got, c.Expected.Kg) {
				t.Errorf("%s: got %v, want %v", c.Name, got, c.Expected.Kg)
			}
		case "display":
			if got := maths.DisplayWeight(c.Input.Weight, c.Input.Unit, c.Input.DisplayUnit); got != c.Expected.Text {
				t.Errorf("%s: got %q, want %q", c.Name, got, c.Expected.Text)
			}
		default:
			t.Fatalf("%s: unknown op %q", c.Name, c.Input.Op)
		}
	}
}

func TestVolumeVectors(t *testing.T) {
	t.Parallel()
	for _, c := range load[struct {
		Sets []maths.SetInput `json:"sets"`
	}, struct {
		VolumeKg float64 `json:"volume_kg"`
	}](t, "volume.json") {
		if got := maths.VolumeKg(c.Input.Sets); !near(got, c.Expected.VolumeKg) {
			t.Errorf("%s: got %v, want %v", c.Name, got, c.Expected.VolumeKg)
		}
	}
}

func TestE1RMVectors(t *testing.T) {
	t.Parallel()
	for _, c := range load[struct {
		LoadKg float64 `json:"load_kg"`
		Reps   int     `json:"reps"`
	}, struct {
		E1RMKg *float64 `json:"e1rm_kg"`
	}](t, "e1rm.json") {
		got, ok := maths.E1RM(c.Input.LoadKg, c.Input.Reps)
		opt(t, c.Name, got, ok, c.Expected.E1RMKg)
	}
}

func TestEffectiveLoadVectors(t *testing.T) {
	t.Parallel()
	for _, c := range load[struct {
		Measurement string  `json:"measurement"`
		Weight      string  `json:"weight"`
		Unit        string  `json:"unit"`
		BodyKg      float64 `json:"body_kg"`
	}, struct {
		LoadKg *float64 `json:"load_kg"`
	}](t, "effective_load.json") {
		got, ok := maths.EffectiveLoadKg(c.Input.Measurement, c.Input.Weight, c.Input.Unit, c.Input.BodyKg)
		opt(t, c.Name, got, ok, c.Expected.LoadKg)
	}
}

func TestCaloriesVectors(t *testing.T) {
	t.Parallel()
	for _, c := range load[struct {
		DurationS float64               `json:"duration_s"`
		BodyKg    float64               `json:"body_kg"`
		Exercises []maths.ExerciseShare `json:"exercises"`
	}, struct {
		Kcal *float64 `json:"kcal"`
	}](t, "calories.json") {
		got, ok := maths.ActiveKcal(c.Input.DurationS, c.Input.BodyKg, c.Input.Exercises)
		opt(t, c.Name, got, ok, c.Expected.Kcal)
	}
}

func TestProgressVectors(t *testing.T) {
	t.Parallel()
	for _, c := range load[struct {
		Sets   []maths.SetInput `json:"sets"`
		BodyKg float64          `json:"body_kg"`
	}, struct {
		VolumeKg  float64  `json:"volume_kg"`
		TopLoadKg *float64 `json:"top_load_kg"`
		TopReps   *int     `json:"top_reps"`
		E1RMKg    *float64 `json:"e1rm_kg"`
	}](t, "progress.json") {
		s := maths.Summarize(c.Input.Sets, c.Input.BodyKg)
		if !near(s.VolumeKg, c.Expected.VolumeKg) {
			t.Errorf("%s volume: got %v, want %v", c.Name, s.VolumeKg, c.Expected.VolumeKg)
		}
		opt(t, c.Name+" top load", s.TopLoadKg, s.HasTop, c.Expected.TopLoadKg)
		if c.Expected.TopReps != nil && s.TopReps != *c.Expected.TopReps {
			t.Errorf("%s top reps: got %d, want %d", c.Name, s.TopReps, *c.Expected.TopReps)
		}
		opt(t, c.Name+" e1rm", s.E1RMKg, s.HasE1RM, c.Expected.E1RMKg)
	}
}

func TestBestsVectors(t *testing.T) {
	t.Parallel()
	for _, c := range load[struct {
		Sessions []maths.Session `json:"sessions"`
		BodyKg   float64         `json:"body_kg"`
	}, struct {
		Bests []maths.Best `json:"bests"`
	}](t, "bests.json") {
		got := maths.NewBests(c.Input.Sessions, c.Input.BodyKg)
		if len(got) != len(c.Expected.Bests) {
			t.Fatalf("%s: got %d bests %+v, want %+v", c.Name, len(got), got, c.Expected.Bests)
		}
		for i, w := range c.Expected.Bests {
			g := got[i]
			if g.Date != w.Date || g.Kind != w.Kind || !near(g.Value, w.Value) {
				t.Errorf("%s best %d: got %+v, want %+v", c.Name, i, g, w)
			}
			if (w.Reps == nil) != (g.Reps == nil) || (w.Reps != nil && *w.Reps != *g.Reps) {
				t.Errorf("%s best %d reps: got %v, want %v", c.Name, i, g.Reps, w.Reps)
			}
		}
	}
}

// The agent counts sets with WorkingSets; the app has no twin of it, so it isn't in the shared vectors.
func TestWorkingSetsCountsOnlySetsThatCount(t *testing.T) {
	t.Parallel()
	sets := []maths.SetInput{
		{Done: true, Kind: "normal"}, {Done: true, Kind: maths.Warmup}, {Done: false, Kind: "normal"},
		{Done: true, Kind: "drop"}, {Done: true, Kind: "failure"},
	}
	if got := maths.WorkingSets(sets); got != 3 {
		t.Fatalf("got %d working sets, want 3", got)
	}
}
