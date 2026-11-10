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

// tally is training added up: over a whole stretch, or one week or month of it.
type tally struct {
	workouts, sets, minutes int
	volumeKg                float64
}

// period is one week or month of a stretch.
type period struct {
	start time.Time
	tally
}

type exerciseTally struct {
	name           string
	sessions, sets int
}

type muscleTally struct {
	sets int
	last time.Time
}

// training is a stretch of finished workouts added up: how often, how much,
// which muscles and which exercises. The summary, the training chart and the
// muscle card all read from it.
type training struct {
	from, to  time.Time
	details   []workoutDetail
	total     tally
	days      int
	weeks     []period // every week of the stretch, Monday first, empty ones included
	months    []period
	weekdays  [7]int // Monday first
	muscles   map[string]*muscleTally
	exercises map[uuid.UUID]*exerciseTally
}

// span reads a tool's dates: both ends included, in the user's time zone,
// defaulting to the given number of days up to today.
func (t *turn) span(dateFrom, dateTo string, days int) (from, to time.Time, err error) {
	if to, err = t.startOfDay(cmp.Or(dateTo, t.now.In(t.loc).Format(time.DateOnly))); err != nil {
		return from, to, err
	}
	from = to.AddDate(0, 0, 1-days)
	if dateFrom != "" {
		if from, err = t.startOfDay(dateFrom); err != nil {
			return from, to, err
		}
	}
	if from.After(to) {
		return from, to, badInput("date_from is after date_to")
	}
	return from, to, nil
}

// finishedBetween reads the finished workouts of a stretch, oldest first, with their sets.
func (t *turn) finishedBetween(ctx context.Context, from, to time.Time) ([]workoutDetail, error) {
	ws, err := t.s.q.WorkoutsBetween(ctx, query.WorkoutsBetweenParams{UserID: t.userID, FromAt: from, ToAt: to.AddDate(0, 0, 1), Lim: summaryLimit})
	if err != nil {
		return nil, err
	}
	return t.loadDetails(ctx, slices.DeleteFunc(ws, func(w query.Workout) bool { return w.EndedAt == nil }))
}

// trainingBetween adds up a stretch of training.
func (t *turn) trainingBetween(ctx context.Context, from, to time.Time) (*training, error) {
	details, err := t.finishedBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	tr := &training{from: from, to: to, details: details, muscles: map[string]*muscleTally{}, exercises: map[uuid.UUID]*exerciseTally{}}
	for at := monday(from); !at.After(to); at = at.AddDate(0, 0, 7) {
		tr.weeks = append(tr.weeks, period{start: at})
	}
	for at := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, t.loc); !at.After(to); at = at.AddDate(0, 1, 0) {
		tr.months = append(tr.months, period{start: at})
	}
	days := map[string]bool{}
	for _, d := range details {
		local := d.w.StartedAt.In(t.loc)
		one := tally{workouts: 1, sets: d.doneSets(), minutes: minutesBetween(d.w.StartedAt, d.w.EndedAt), volumeKg: d.volumeKg()}
		week := &tr.weeks[int(monday(local).Sub(tr.weeks[0].start).Hours()/24+0.5)/7]
		month := &tr.months[(local.Year()-from.Year())*12+int(local.Month())-int(from.Month())]
		for _, sum := range []*tally{&tr.total, &week.tally, &month.tally} {
			sum.workouts += one.workouts
			sum.sets += one.sets
			sum.minutes += one.minutes
			sum.volumeKg += one.volumeKg
		}
		days[local.Format(time.DateOnly)] = true
		tr.weekdays[(int(local.Weekday())+6)%7]++
		for _, e := range d.exercises {
			n := maths.WorkingSets(setInputs(e.row.Measurement, e.sets))
			if n == 0 {
				continue
			}
			for _, m := range e.row.PrimaryMuscles {
				mt := tr.muscles[m]
				if mt == nil {
					mt = &muscleTally{}
					tr.muscles[m] = mt
				}
				mt.sets += n
				mt.last = d.w.StartedAt
			}
			et := tr.exercises[e.row.ExerciseID]
			if et == nil {
				et = &exerciseTally{name: e.row.ExerciseName}
				tr.exercises[e.row.ExerciseID] = et
			}
			et.sessions++
			et.sets += n
		}
	}
	tr.days = len(days)
	return tr, nil
}

// monday is the start of the week a moment falls in, in that moment's zone.
func monday(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day()-(int(at.Weekday())+6)%7, 0, 0, 0, 0, at.Location())
}

// streaks finds the longest run of weeks with a workout, and the run that
// reaches the present. A week still under way with nothing in it yet doesn't
// end the current run.
func (tr *training) streaks() (longest, longestStart, current int) {
	run := 0
	for i, w := range tr.weeks {
		if w.workouts == 0 {
			run = 0
			continue
		}
		if run++; run > longest {
			longest, longestStart = run, i-run+1
		}
	}
	last := len(tr.weeks) - 1
	if last >= 0 && tr.weeks[last].workouts == 0 && tr.weeks[last].start.AddDate(0, 0, 6).After(tr.to) {
		last--
	}
	for ; last >= 0 && tr.weeks[last].workouts > 0; last-- {
		current++
	}
	return longest, longestStart, current
}

// musclesBySets is the muscles trained, most sets first.
func (tr *training) musclesBySets() []string {
	return slices.SortedFunc(maps.Keys(tr.muscles), func(a, b string) int {
		return cmp.Or(cmp.Compare(tr.muscles[b].sets, tr.muscles[a].sets), cmp.Compare(a, b))
	})
}

var weekdayNames = [7]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// trainingSummary adds up a stretch of training: how often, how much, which
// muscles and which exercises. One read instead of paging through workouts.
func (t *turn) trainingSummary(ctx context.Context, in summaryInput) (any, error) {
	from, to, err := t.span(in.DateFrom, in.DateTo, 28)
	if err != nil {
		return nil, err
	}
	tr, err := t.trainingBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"from": t.day(from), "to": t.day(to), "workouts": tr.total.workouts, "days_trained": tr.days,
		"sets": tr.total.sets, "volume": kgText(tr.total.volumeKg, t.displayUnit),
	}
	if tr.total.workouts > 0 {
		out["average_minutes"] = tr.total.minutes / tr.total.workouts
		weekdays := map[string]int{}
		for i, n := range tr.weekdays {
			if n > 0 {
				weekdays[weekdayNames[i]] = n
			}
		}
		out["workouts_by_weekday"] = weekdays
		longest, start, current := tr.streaks()
		out["longest_run_of_weeks"] = fmt.Sprintf("%d, from the week of %s", longest, t.day(tr.weeks[start].start))
		out["current_run_of_weeks"] = current
	}
	// Week by week up to half a year; month by month beyond that.
	key, periods := "weeks", tr.weeks
	if len(tr.weeks) > 26 {
		key, periods = "months", tr.months
	}
	list := []map[string]any{}
	for _, p := range periods {
		if p.workouts == 0 && key == "months" {
			continue
		}
		list = append(list, map[string]any{"start": p.start.Format(time.DateOnly), "workouts": p.workouts, "sets": p.sets, "volume": kgText(p.volumeKg, t.displayUnit)})
	}
	out[key] = list
	muscleList := []map[string]any{}
	for _, m := range tr.musclesBySets() {
		muscleList = append(muscleList, map[string]any{"muscle": m, "sets": tr.muscles[m].sets, "last_trained": t.day(tr.muscles[m].last)})
	}
	exerciseList := []map[string]any{}
	ids := slices.SortedFunc(maps.Keys(tr.exercises), func(a, b uuid.UUID) int {
		return cmp.Or(cmp.Compare(tr.exercises[b].sets, tr.exercises[a].sets), cmp.Compare(tr.exercises[a].name, tr.exercises[b].name))
	})
	for _, id := range ids[:min(len(ids), 20)] {
		e := tr.exercises[id]
		exerciseList = append(exerciseList, map[string]any{"exercise_id": id.String(), "name": e.name, "sessions": e.sessions, "sets": e.sets})
	}
	out["sets_by_muscle"], out["most_done_exercises"] = muscleList, exerciseList

	t.reading(ctx, readSpan(tr.total.workouts, t.day(from)+" to "+t.day(to)))
	return out, nil
}

// sortedByCount is a tally's keys, biggest first, ties by name.
func sortedByCount(counts map[string]int) []string {
	return slices.SortedFunc(maps.Keys(counts), func(a, b string) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b))
	})
}

// readSpan is the reading line for a stretch of workouts.
func readSpan(workouts int, span string) string {
	if workouts == 0 {
		return "Found no workouts, " + span
	}
	return fmt.Sprintf("Read %s, %s", count(workouts, "workout", "workouts"), span)
}

func (t *turn) startOfDay(date string) (time.Time, error) {
	d, err := time.ParseInLocation(time.DateOnly, date, t.loc)
	if err != nil {
		return time.Time{}, badInput("date %q must be YYYY-MM-DD", date)
	}
	return d, nil
}
