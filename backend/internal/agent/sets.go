package agent

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// setInput is one set as Milo describes it.
type setInput struct {
	Reps      *int32   `json:"reps,omitempty"`
	Weight    string   `json:"weight,omitempty" jsonschema_description:"Exact weight as said, e.g. \"20\" or \"22.5\". Omit for none."`
	Unit      string   `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb"`
	PerSide   bool     `json:"per_side,omitempty" jsonschema_description:"Reps were done on each side."`
	DurationS *int32   `json:"duration_s,omitempty"`
	DistanceM *float64 `json:"distance_m,omitempty"`
	Kind      string   `json:"kind,omitempty" jsonschema:"enum=normal,enum=warmup,enum=drop,enum=failure" jsonschema_description:"Leave out for an ordinary working set."`
}

func checkSets(sets []setInput) error {
	for i := range sets {
		s := &sets[i]
		s.Weight = strings.TrimSpace(s.Weight)
		s.Unit = cmp.Or(s.Unit, "kg")
		s.Kind = cmp.Or(s.Kind, "normal")
		switch {
		case !rows.ValidWeight(s.Weight):
			return badInput("weight %q must be a plain number like 20 or 22.5", s.Weight)
		case s.Unit != "kg" && s.Unit != "lb":
			return badInput("unit must be kg or lb, not %q", s.Unit)
		case s.Reps != nil && (*s.Reps < 0 || *s.Reps > 1000):
			return badInput("reps %d is out of range", *s.Reps)
		case rows.Parse[milo.SetKind](s.Kind) == milo.SetKind_SET_KIND_UNSPECIFIED:
			return badInput("kind must be normal, warmup, drop or failure, not %q", s.Kind)
		}
	}
	return nil
}

func (s setInput) text(measurement string) string {
	return kindPrefix(s.Kind) + setText(measurement, s.Weight, s.Unit, s.Reps, s.PerSide, s.DurationS, s.DistanceM)
}

// write marks a set done with what was said.
func (s setInput) write(row *milo.Set, now time.Time) {
	row.Kind = rows.Parse[milo.SetKind](s.Kind)
	row.Reps, row.Weight, row.WeightUnit, row.PerSide = s.Reps, s.Weight, rows.Parse[milo.WeightUnit](s.Unit), s.PerSide
	row.DurationS, row.DistanceM = s.DurationS, s.DistanceM
	row.Done, row.UpdatedAt = true, ts(now)
}

// log writes done sets into a slot and returns the changes and each set's
// number in the exercise. Sets waiting unticked (filled in from last time when
// the workout started) are used first, in order; the rest are added after them.
func (sl *slot) log(sets []setInput, now time.Time) ([]*milo.Change, []int) {
	changes, numbers := make([]*milo.Change, len(sets)), make([]int, len(sets))
	next := 0
	for i, in := range sets {
		for next < len(sl.sets) && sl.sets[next].GetDone() {
			next++
		}
		if next == len(sl.sets) {
			position := int32(1)
			if next > 0 {
				position = sl.sets[next-1].GetPosition() + 1
			}
			sl.sets = append(sl.sets, &milo.Set{Id: newID().String(), WorkoutExerciseId: sl.we.GetId(), Position: position})
		}
		in.write(sl.sets[next], now)
		changes[i], numbers[i] = rows.Change(sl.sets[next]), next+1
	}
	return changes, numbers
}

// kindPrefix marks a set that isn't an ordinary working set: "Warm-up, 40 kg × 10".
func kindPrefix(kind string) string {
	switch kind {
	case "warmup":
		return "Warm-up, "
	case "drop":
		return "Drop set, "
	case "failure":
		return "To failure, "
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// count is "1 set" or "3 sets".
func count(n int, one, many string) string {
	return fmt.Sprintf("%d %s", n, plural(n, one, many))
}
