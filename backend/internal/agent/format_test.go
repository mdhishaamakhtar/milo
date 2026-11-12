package agent

import (
	"testing"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

// How Milo writes sets, times and totals: what the model reads, and so how it talks.
func TestSetsReadTheWayALifterSaysThem(t *testing.T) {
	t.Parallel()
	n := func(v int32) *int32 { return &v }
	m := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		measurement, weight, unit string
		reps                      *int32
		perSide                   bool
		seconds                   *int32
		metres                    *float64
		want                      string
	}{
		{"reps_load", "20.50", "kg", n(15), false, nil, nil, "20.5 kg × 15"},
		{"reps_load", "20", "kg", n(12), true, nil, nil, "20 kg × 12 each side"},
		{"reps_load", "", "kg", nil, false, nil, nil, "0 reps"},
		{"bodyweight", "", "kg", n(12), false, nil, nil, "12 reps"},
		{"bodyweight", "10", "kg", n(8), false, nil, nil, "+10 kg × 8"},
		{"timed", "", "kg", nil, false, n(45), nil, "45 s"},
		{"timed", "16", "kg", nil, false, n(90), nil, "1:30 at 16 kg"},
		{"distance", "", "kg", nil, false, n(1200), m(5000), "5 km in 20 min"},
		{"distance", "20", "kg", nil, false, nil, m(400), "400 m with 20 kg"},
	} {
		if got := setText(c.measurement, c.weight, c.unit, c.reps, c.perSide, c.seconds, c.metres); got != c.want {
			t.Errorf("%s %v: got %q, want %q", c.measurement, c, got, c.want)
		}
	}
}

func TestAnExerciseSumsUpInOneLine(t *testing.T) {
	t.Parallel()
	n := func(v int32) *int32 { return &v }
	set := func(weight string, reps int32, done bool) query.Set {
		return query.Set{Weight: weight, WeightUnit: "kg", Reps: n(reps), Done: done, Kind: "normal"}
	}
	for _, c := range []struct {
		measurement string
		sets        []query.Set
		want        string
	}{
		{"reps_load", []query.Set{set("60", 8, false)}, "no sets done"},
		{"reps_load", []query.Set{set("20", 15, true), set("20", 15, true), set("20", 15, false)}, "2 × 15 at 20 kg"},
		{"bodyweight", []query.Set{set("", 12, true), set("", 12, true)}, "2 × 12 reps"},
		{"timed", []query.Set{{DurationS: n(45), Done: true, Kind: "normal"}, {DurationS: n(45), Done: true, Kind: "normal"}}, "2 × 45 s"},
		{"reps_load", []query.Set{{Weight: "20", WeightUnit: "kg", Reps: n(12), PerSide: true, Done: true}, {Weight: "20", WeightUnit: "kg", Reps: n(12), PerSide: true, Done: true}}, "2 × 12 at 20 kg each side"},
		{"reps_load", []query.Set{set("60", 8, true), set("65", 5, true)}, "2 sets, top 65 kg"},
		{"bodyweight", []query.Set{set("", 12, true), set("", 10, true)}, "2 sets"},
	} {
		if got := exerciseSummary(c.measurement, c.sets); got != c.want {
			t.Errorf("%s: got %q, want %q", c.want, got, c.want)
		}
	}
}

func TestNumbersAndDates(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 22: "22nd", 23: "23rd", 112: "112th"} {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, want)
		}
	}
	if got := kgText(2142.4, "kg"); got != "2,142 kg" {
		t.Errorf("kgText = %q", got)
	}
	if got := kgText(1000, "lb"); got != "2,205 lb" {
		t.Errorf("kgText in pounds = %q", got)
	}
	if got := readingText(ptr(82.44), ptr(18.0), "lb"); got != "181.7 lb, 18% body fat" {
		t.Errorf("readingText = %q", got)
	}

	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	lastYear := time.Date(2025, 10, 6, 7, 0, 0, 0, time.UTC)
	if got := dayText(lastYear, now); got != "Mon 6 Oct 2025" {
		t.Errorf("dayText = %q", got)
	}
	if got := dateText(lastYear, now); got != "6 Oct 2025" {
		t.Errorf("dateText = %q", got)
	}
	// A workout still running, or one whose end was typed before its start, has no length.
	if minutesBetween(now, nil) != 0 || minutesBetween(now, &lastYear) != 0 {
		t.Error("minutesBetween counted a missing or backwards end")
	}
}

func TestTitlesAreCutOnAWord(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"log my push day": "log my push day",
		"what did I do on  my\nlast leg day, and how heavy": "what did I do on my…",
		"supercalifragilisticexpialidocious lifts":          "supercalifragilistic…",
	} {
		if got := truncate(in, 21); got != want {
			t.Errorf("truncate(%q) = %q, want %q", in, got, want)
		}
	}
}

func ptr(v float64) *float64 { return &v }
