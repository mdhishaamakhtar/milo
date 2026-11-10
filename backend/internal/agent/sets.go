package agent

import (
	"cmp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// setInput is one set as Milo describes it, for new exercises and log_sets.
type setInput struct {
	Reps      *int32   `json:"reps,omitempty"`
	Weight    string   `json:"weight,omitempty" jsonschema_description:"Exact weight as said, e.g. \"20\" or \"22.5\". Omit for none."`
	Unit      string   `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb"`
	PerSide   bool     `json:"per_side,omitempty" jsonschema_description:"Reps were done on each side."`
	DurationS *int32   `json:"duration_s,omitempty"`
	DistanceM *float64 `json:"distance_m,omitempty"`
}

func checkSets(sets []setInput) error {
	for i := range sets {
		s := &sets[i]
		s.Weight = strings.TrimSpace(s.Weight)
		s.Unit = cmp.Or(s.Unit, "kg")
		switch {
		case !rows.ValidWeight(s.Weight):
			return badInput("weight %q must be a plain number like 20 or 22.5", s.Weight)
		case s.Unit != "kg" && s.Unit != "lb":
			return badInput("unit must be kg or lb, not %q", s.Unit)
		case s.Reps != nil && (*s.Reps < 0 || *s.Reps > 1000):
			return badInput("reps %d is out of range", *s.Reps)
		}
	}
	return nil
}

func (s setInput) text(measurement string) string {
	return setText(measurement, s.Weight, s.Unit, s.Reps, s.PerSide, s.DurationS, s.DistanceM)
}

// row is the set as logged: done, in the workout exercise, at a position.
func (s setInput) row(weID uuid.UUID, position int32, now time.Time) *milo.Set {
	return &milo.Set{
		Id: newID().String(), WorkoutExerciseId: weID.String(), Position: position, Kind: milo.SetKind_SET_KIND_NORMAL,
		Reps: s.Reps, Weight: s.Weight, WeightUnit: rows.Parse[milo.WeightUnit](s.Unit), PerSide: s.PerSide,
		DurationS: s.DurationS, DistanceM: s.DistanceM, Done: true, UpdatedAt: ts(now),
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
