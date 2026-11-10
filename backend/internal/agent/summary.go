package agent

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
)

type summaryInput struct {
	DateFrom string `json:"date_from,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to four weeks before date_to."`
	DateTo   string `json:"date_to,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to today."`
}

// summaryLimit is the most workouts one summary reads: a year of training twice a day.
const summaryLimit = 730

// trainingSummary adds up a stretch of training: how often, how much, which
// muscles and which exercises. One read instead of paging through workouts.
func (t *turn) trainingSummary(ctx context.Context, in summaryInput) (any, error) {
	today := t.now.In(t.loc).Format(time.DateOnly)
	to, err := t.startOfDay(cmp.Or(in.DateTo, today))
	if err != nil {
		return nil, err
	}
	from := to.AddDate(0, 0, -27)
	if in.DateFrom != "" {
		if from, err = t.startOfDay(in.DateFrom); err != nil {
			return nil, err
		}
	}
	if from.After(to) {
		return nil, badInput("date_from is after date_to")
	}
	ws, err := t.s.q.WorkoutsBetween(ctx, query.WorkoutsBetweenParams{UserID: t.userID, FromAt: from, ToAt: to.AddDate(0, 0, 1), Lim: summaryLimit})
	if err != nil {
		return nil, err
	}
	ws = slices.DeleteFunc(ws, func(w query.Workout) bool { return w.EndedAt == nil })
	details, err := t.loadDetails(ctx, ws)
	if err != nil {
		return nil, err
	}

	type tally struct {
		workouts, sets int
		volumeKg       float64
	}
	type exerciseTally struct {
		name           string
		sessions, sets int
	}
	total, weeks := tally{}, map[string]*tally{}
	muscles, exercises := map[string]int{}, map[uuid.UUID]*exerciseTally{}
	days, minutes := map[string]bool{}, 0
	for _, d := range details {
		local := d.w.StartedAt.In(t.loc)
		week := local.AddDate(0, 0, -(int(local.Weekday())+6)%7).Format(time.DateOnly) // its Monday
		if weeks[week] == nil {
			weeks[week] = &tally{}
		}
		sets, volume := d.doneSets(), d.volumeKg()
		for _, tl := range []*tally{&total, weeks[week]} {
			tl.workouts++
			tl.sets += sets
			tl.volumeKg += volume
		}
		days[local.Format(time.DateOnly)] = true
		minutes += minutesBetween(d.w.StartedAt, d.w.EndedAt)
		for _, e := range d.exercises {
			n := maths.WorkingSets(setInputs(e.row.Measurement, e.sets))
			if n == 0 {
				continue
			}
			for _, m := range e.row.PrimaryMuscles {
				muscles[m] += n
			}
			et := exercises[e.row.ExerciseID]
			if et == nil {
				et = &exerciseTally{name: e.row.ExerciseName}
				exercises[e.row.ExerciseID] = et
			}
			et.sessions++
			et.sets += n
		}
	}

	out := map[string]any{
		"from": t.day(from), "to": t.day(to), "workouts": total.workouts, "days_trained": len(days),
		"sets": total.sets, "volume": kgText(total.volumeKg, t.displayUnit),
	}
	if total.workouts > 0 {
		out["average_minutes"] = minutes / total.workouts
	}
	weekList := []map[string]any{}
	for _, monday := range slices.Sorted(maps.Keys(weeks)) {
		w := weeks[monday]
		weekList = append(weekList, map[string]any{"week_of": monday, "workouts": w.workouts, "sets": w.sets, "volume": kgText(w.volumeKg, t.displayUnit)})
	}
	muscleList := []map[string]any{}
	for _, m := range sortedByCount(muscles) {
		muscleList = append(muscleList, map[string]any{"muscle": m, "sets": muscles[m]})
	}
	exerciseList := []map[string]any{}
	ids := slices.SortedFunc(maps.Keys(exercises), func(a, b uuid.UUID) int {
		return cmp.Or(cmp.Compare(exercises[b].sets, exercises[a].sets), cmp.Compare(exercises[a].name, exercises[b].name))
	})
	for _, id := range ids[:min(len(ids), 20)] {
		e := exercises[id]
		exerciseList = append(exerciseList, map[string]any{"exercise_id": id.String(), "name": e.name, "sessions": e.sessions, "sets": e.sets})
	}
	out["weeks"], out["sets_by_muscle"], out["most_done_exercises"] = weekList, muscleList, exerciseList

	span := t.day(from) + " to " + t.day(to)
	if total.workouts == 0 {
		t.reading(ctx, "Found no workouts, "+span)
	} else {
		t.reading(ctx, fmt.Sprintf("Read %s, %s", count(total.workouts, "workout", "workouts"), span))
	}
	return out, nil
}

func (t *turn) startOfDay(date string) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, date, t.loc)
	if err != nil {
		return time.Time{}, badInput("date %q must be YYYY-MM-DD", date)
	}
	return d, nil
}

// sortedByCount is a tally's keys, biggest first, ties by name.
func sortedByCount(counts map[string]int) []string {
	return slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
}
