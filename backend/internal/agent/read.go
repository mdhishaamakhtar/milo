package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// The read tools. Each tells the user what it looked at with a reading line.

type searchInput struct {
	Query string `json:"query" jsonschema_description:"A few words, e.g. \"landmine\", \"lat pulldown\", \"chest press\"."`
}

func (t *turn) searchExercises(ctx context.Context, in searchInput) (any, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, badInput("query can't be empty")
	}
	found, err := t.s.q.SearchExercises(ctx, query.SearchExercisesParams{UserID: t.userID, Query: q, Lim: 8})
	if err != nil {
		return nil, err
	}
	matches := make([]map[string]any, len(found))
	for i, e := range found {
		m := map[string]any{
			"exercise_id": e.ID.String(), "name": e.Name, "measurement": e.Measurement, "equipment": e.Equipment,
			"muscles": e.PrimaryMuscles, "unilateral": e.Unilateral, "yours": e.UserID != nil,
		}
		if len(e.Aliases) > 0 {
			m["aliases"] = e.Aliases
		}
		if e.LastDone.After(time.Unix(0, 0)) {
			m["last_done"] = t.day(e.LastDone)
		}
		if e.Approximate {
			m["approximate"] = true
		}
		matches[i] = m
	}
	return map[string]any{"matches": matches}, nil
}

type workoutsInput struct {
	WorkoutIDs []string `json:"workout_ids,omitempty"`
	Date       string   `json:"date,omitempty" jsonschema_description:"One day, YYYY-MM-DD in the user's time zone."`
	DateFrom   string   `json:"date_from,omitempty"`
	DateTo     string   `json:"date_to,omitempty"`
	Limit      int32    `json:"limit,omitempty" jsonschema_description:"At most 20. Default 5."`
}

func (t *turn) getWorkouts(ctx context.Context, in workoutsInput) (any, error) {
	limit := min(cmp.Or(in.Limit, 5), 20)
	var ws []query.Workout
	var err error
	var span string // the dates asked about, for the reading line
	switch {
	case len(in.WorkoutIDs) > 0:
		ids := make([]uuid.UUID, len(in.WorkoutIDs))
		for i, s := range in.WorkoutIDs {
			if ids[i], err = parseUUID(s, "workout_id"); err != nil {
				return nil, err
			}
		}
		ws, err = t.s.q.WorkoutsByIDs(ctx, query.WorkoutsByIDsParams{UserID: t.userID, Ids: ids})
	case in.Date != "" || in.DateFrom != "" || in.DateTo != "":
		first, last := cmp.Or(in.DateFrom, in.Date, "2000-01-01"), cmp.Or(in.DateTo, in.Date, t.now.In(t.loc).Format(time.DateOnly))
		from, err1 := t.startOfDay(first)
		to, err2 := t.startOfDay(last)
		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}
		span = t.day(from)
		if first != last {
			span += " to " + t.day(to)
		}
		ws, err = t.s.q.WorkoutsBetween(ctx, query.WorkoutsBetweenParams{UserID: t.userID, FromAt: from, ToAt: to.AddDate(0, 0, 1), Lim: limit})
	default:
		ws, err = t.s.q.RecentWorkouts(ctx, query.RecentWorkoutsParams{UserID: t.userID, Limit: limit})
		slices.Reverse(ws)
	}
	if err != nil {
		return nil, err
	}
	details, err := t.loadDetails(ctx, ws)
	if err != nil {
		return nil, err
	}

	switch {
	case len(details) == 1:
		t.reading(ctx, fmt.Sprintf("Read %s, %s", nameOr(details[0].w.Name, "a workout"), t.day(details[0].w.StartedAt)))
	case len(details) > 1 && span != "":
		t.reading(ctx, fmt.Sprintf("Read %d workouts, %s", len(details), span))
	case len(details) > 1:
		t.reading(ctx, fmt.Sprintf("Read your last %d workouts", len(details)))
	case span != "":
		t.reading(ctx, "Found no workouts on "+span)
	}
	body := t.bodyKg(ctx)
	out := make([]map[string]any, len(details))
	for i, d := range details {
		out[i] = t.workoutJSON(d, body)
	}
	return map[string]any{"workouts": out}, nil
}

type listWorkoutsInput struct {
	DateFrom string `json:"date_from,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to the start of the log."`
	DateTo   string `json:"date_to,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to today."`
	Search   string `json:"search,omitempty" jsonschema_description:"Only workouts whose name, or one of whose exercises or muscles, contains this: \"leg\", \"push\", \"deadlift\"."`
}

// listWorkouts is one line per finished workout over any stretch, newest
// first: enough to find or rank workouts without reading their sets.
func (t *turn) listWorkouts(ctx context.Context, in listWorkoutsInput) (any, error) {
	from, to, err := t.span(cmp.Or(in.DateFrom, "2000-01-01"), in.DateTo, 0)
	if err != nil {
		return nil, err
	}
	details, err := t.finishedBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	search, body := strings.ToLower(strings.TrimSpace(in.Search)), t.bodyKg(ctx)
	out := []map[string]any{}
	for _, d := range slices.Backward(details) {
		sets := map[string]int{} // working sets per muscle
		matches := search == "" || strings.Contains(strings.ToLower(d.w.Name), search)
		for _, e := range d.exercises {
			matches = matches || strings.Contains(strings.ToLower(e.row.ExerciseName), search)
			for _, m := range e.row.PrimaryMuscles {
				matches = matches || strings.Contains(m, search)
				sets[m] += maths.WorkingSets(setInputs(e.row.Measurement, e.sets))
			}
		}
		if !matches {
			continue
		}
		muscles := sortedByCount(sets)
		row := map[string]any{
			"workout_id": d.w.ID.String(), "name": d.w.Name, "date": t.day(d.w.StartedAt), "minutes": minutesBetween(d.w.StartedAt, d.w.EndedAt),
			"exercises": len(d.exercises), "sets": d.doneSets(), "volume": kgText(d.volumeKg(), t.displayUnit), "muscles": muscles[:min(len(muscles), 3)],
		}
		if kcal, ok := maths.ActiveKcal(d.w.EndedAt.Sub(d.w.StartedAt).Seconds(), body, d.shares()); ok {
			row["active_kcal_estimate"] = int(kcal + 0.5)
		}
		out = append(out, row)
	}
	what := "workouts"
	if search != "" {
		what = fmt.Sprintf("workouts matching %q", in.Search)
	}
	t.reading(ctx, fmt.Sprintf("Looked through %d %s", len(out), what))
	return map[string]any{"workouts": out}, nil
}

type historyInput struct {
	ExerciseID string `json:"exercise_id"`
	Limit      int32  `json:"limit,omitempty" jsonschema_description:"Sessions to read, at most 40. Default 12."`
}

func (t *turn) exerciseHistory(ctx context.Context, in historyInput) (any, error) {
	id, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	ex, sessions, err := t.sessions(ctx, id, min(cmp.Or(in.Limit, 12), 40))
	if err != nil {
		return nil, err
	}
	body := t.bodyKg(ctx)
	out := make([]map[string]any, len(sessions))
	oldestFirst := make([]maths.Session, len(sessions))
	for i, s := range sessions {
		inputs := setInputs(ex.Measurement, s.sets)
		oldestFirst[len(sessions)-1-i] = maths.Session{Date: s.at.In(t.loc).Format(time.DateOnly), Sets: inputs}
		sum := maths.Summarize(inputs, body)
		sets := make([]string, len(s.sets))
		for j, set := range s.sets {
			sets[j] = setTextRow(ex.Measurement, set)
			if !set.Done {
				sets[j] += " (not done)"
			}
		}
		item := map[string]any{"date": t.day(s.at), "workout": s.workoutName, "workout_id": s.workoutID.String(), "sets": sets, "volume": kgText(sum.VolumeKg, t.displayUnit)}
		if sum.HasTop {
			item["top_set"] = fmt.Sprintf("%s kg × %d", maths.FormatTenths(sum.TopLoadKg), sum.TopReps)
		}
		if sum.HasE1RM {
			item["e1rm"] = maths.FormatTenths(sum.E1RMKg) + " kg"
		}
		out[i] = item
	}
	bests := []map[string]any{}
	for _, b := range maths.NewBests(oldestFirst, body) {
		item := map[string]any{"date": b.Date, "kind": b.Kind, "value": maths.FormatTenths(b.Value) + " kg"}
		if b.Reps != nil {
			item["reps"] = *b.Reps
		}
		bests = append(bests, item)
	}
	if len(sessions) > 0 {
		t.reading(ctx, fmt.Sprintf("Read %d %s sessions since %s", len(sessions), shortName(ex.Name), sessions[len(sessions)-1].at.In(t.loc).Format("Jan 2006")))
	} else {
		t.reading(ctx, "Found no "+shortName(ex.Name)+" sessions")
	}
	return map[string]any{
		"exercise": ex.Name, "measurement": ex.Measurement, "sessions": out, "bests": bests,
		"note": "Top sets and e1RM are effective load in kg (body weight counted for bodyweight and assisted movements).",
	}, nil
}

type noInput struct{}

func (t *turn) listTemplates(ctx context.Context, _ noInput) (any, error) {
	templates, err := t.s.q.ListTemplatesFor(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(templates))
	for i, tpl := range templates {
		items := []map[string]any{}
		for _, it := range rows.TemplateItems(tpl.Items) {
			name := "(unknown exercise)"
			if id, err := uuid.Parse(it.GetExerciseId()); err == nil {
				if ex, err := t.visibleExercise(ctx, id); err == nil {
					name = ex.Name
				}
			}
			items = append(items, map[string]any{"exercise_id": it.GetExerciseId(), "name": name, "group": it.GetGroupLabel()})
		}
		out[i] = map[string]any{"template_id": tpl.ID.String(), "name": tpl.Name, "items": items}
	}
	t.reading(ctx, fmt.Sprintf("Read your %d templates", len(templates)))
	return map[string]any{"templates": out}, nil
}

// Loading what the tools read.

// workoutDetail is a workout with its exercises and their sets.
type workoutDetail struct {
	w         query.Workout
	exercises []exerciseDetail
}

type exerciseDetail struct {
	row  query.WorkoutExercisesForRow
	sets []query.Set
}

func (d workoutDetail) doneSets() int {
	n := 0
	for _, e := range d.exercises {
		for _, s := range e.sets {
			if s.Done {
				n++
			}
		}
	}
	return n
}

// shares is each exercise's part of the workout, for calories.
func (d workoutDetail) shares() []maths.ExerciseShare {
	out := make([]maths.ExerciseShare, len(d.exercises))
	for i, e := range d.exercises {
		done := 0
		for _, s := range e.sets {
			if s.Done {
				done++
			}
		}
		out[i] = maths.ExerciseShare{MET: e.row.Met, DoneSets: done}
	}
	return out
}

func (d workoutDetail) volumeKg() float64 {
	v := 0.0
	for _, e := range d.exercises {
		v += maths.VolumeKg(setInputs(e.row.Measurement, e.sets))
	}
	return v
}

func (t *turn) loadDetails(ctx context.Context, ws []query.Workout) ([]workoutDetail, error) {
	if len(ws) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(ws))
	for i, w := range ws {
		ids[i] = w.ID
	}
	wes, err := t.s.q.WorkoutExercisesFor(ctx, query.WorkoutExercisesForParams{UserID: t.userID, WorkoutIds: ids})
	if err != nil {
		return nil, err
	}
	weIDs := make([]uuid.UUID, len(wes))
	for i, we := range wes {
		weIDs[i] = we.ID
	}
	sets, err := t.s.q.SetsFor(ctx, query.SetsForParams{UserID: t.userID, WeIds: weIDs})
	if err != nil {
		return nil, err
	}
	setsByWE := map[uuid.UUID][]query.Set{}
	for _, s := range sets {
		setsByWE[s.WorkoutExerciseID] = append(setsByWE[s.WorkoutExerciseID], s)
	}
	byWorkout := map[uuid.UUID][]exerciseDetail{}
	for _, we := range wes {
		byWorkout[we.WorkoutID] = append(byWorkout[we.WorkoutID], exerciseDetail{row: we, sets: setsByWE[we.ID]})
	}
	out := make([]workoutDetail, len(ws))
	for i, w := range ws {
		out[i] = workoutDetail{w: w, exercises: byWorkout[w.ID]}
	}
	return out, nil
}

func (t *turn) workoutJSON(d workoutDetail, bodyKg float64) map[string]any {
	exercises := make([]map[string]any, len(d.exercises))
	for i, e := range d.exercises {
		sets := make([]map[string]any, len(e.sets))
		for j, s := range e.sets {
			sets[j] = map[string]any{"set_id": s.ID.String(), "n": j + 1, "set": setTextRow(e.row.Measurement, s), "done": s.Done}
		}
		item := map[string]any{
			"workout_exercise_id": e.row.ID.String(), "exercise_id": e.row.ExerciseID.String(), "name": e.row.ExerciseName,
			"measurement": e.row.Measurement, "sets": sets, "volume": kgText(maths.VolumeKg(setInputs(e.row.Measurement, e.sets)), t.displayUnit),
		}
		if e.row.GroupLabel != "" {
			item["group"] = e.row.GroupLabel
		}
		if e.row.Notes != "" {
			item["notes"] = e.row.Notes
		}
		if e.row.Approximate {
			item["approximate"] = true
		}
		exercises[i] = item
	}
	out := map[string]any{
		"workout_id": d.w.ID.String(), "name": d.w.Name, "date": t.day(d.w.StartedAt),
		"started": d.w.StartedAt.In(t.loc).Format("15:04"), "exercises": exercises, "volume": kgText(d.volumeKg(), t.displayUnit),
	}
	if d.w.EndedAt != nil {
		out["minutes"] = minutesBetween(d.w.StartedAt, d.w.EndedAt)
		if kcal, ok := maths.ActiveKcal(d.w.EndedAt.Sub(d.w.StartedAt).Seconds(), bodyKg, d.shares()); ok {
			out["active_kcal_estimate"] = int(kcal + 0.5)
		}
	} else {
		out["running"] = true
	}
	if d.w.Notes != "" {
		out["notes"] = d.w.Notes
	}
	return out
}

// session is one exercise in one workout.
type session struct {
	workoutID   uuid.UUID
	workoutName string
	at          time.Time
	sets        []query.Set
}

// sessions reads an exercise's sessions, newest first.
func (t *turn) sessions(ctx context.Context, exerciseID uuid.UUID, limit int32) (query.Exercise, []session, error) {
	ex, err := t.visibleExercise(ctx, exerciseID)
	if err != nil {
		return ex, nil, err
	}
	found, err := t.s.q.ExerciseSessions(ctx, query.ExerciseSessionsParams{UserID: t.userID, ExerciseID: exerciseID, Lim: limit})
	if err != nil {
		return ex, nil, err
	}
	weIDs := make([]uuid.UUID, len(found))
	for i, r := range found {
		weIDs[i] = r.WorkoutExerciseID
	}
	sets, err := t.s.q.SetsFor(ctx, query.SetsForParams{UserID: t.userID, WeIds: weIDs})
	if err != nil {
		return ex, nil, err
	}
	byWE := map[uuid.UUID][]query.Set{}
	for _, s := range sets {
		byWE[s.WorkoutExerciseID] = append(byWE[s.WorkoutExerciseID], s)
	}
	out := make([]session, len(found))
	for i, r := range found {
		out[i] = session{workoutID: r.WorkoutID, workoutName: r.WorkoutName, at: r.StartedAt, sets: byWE[r.WorkoutExerciseID]}
	}
	return ex, out, nil
}

func (t *turn) bodyKg(ctx context.Context) float64 {
	w, err := t.s.q.LatestBodyWeight(ctx, query.LatestBodyWeightParams{UserID: t.userID, At: t.now})
	if err != nil || w == nil {
		return 0
	}
	return *w
}

// shortName drops the equipment in brackets and the capitals: "Chest Press (Machine)" → "chest press".
func shortName(name string) string {
	return strings.ToLower(shortTitle(name))
}

func nameOr(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

func (t *turn) getProfile(ctx context.Context, _ noInput) (any, error) {
	st, err := t.settings(ctx)
	if err != nil {
		return nil, err
	}
	readings, err := t.s.q.BodyMeasurementsFor(ctx, query.BodyMeasurementsForParams{UserID: t.userID, Limit: 20})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(readings))
	for i, m := range readings {
		item := map[string]any{"reading_id": m.ID.String(), "date": t.day(m.MeasuredAt), "from": m.Source}
		if m.WeightKg != nil {
			item["weight"] = weightIn(*m.WeightKg, t.displayUnit)
		}
		if m.BodyFatPct != nil {
			item["body_fat"] = trimNum(fmt.Sprintf("%.1f", *m.BodyFatPct)) + "%"
		}
		out[i] = item
	}
	settings := map[string]any{"totals_and_charts_in": st.DisplayUnit, "default_rest_s": st.RestDefaultS}
	if st.HeightCm != nil {
		settings["height_cm"] = *st.HeightCm
	}
	if st.BirthDate != "" {
		settings["birth_date"] = st.BirthDate
	}
	if st.Sex != "" {
		settings["sex"] = st.Sex
	}
	t.reading(ctx, "Read your body readings and settings")
	return map[string]any{"settings": settings, "body_readings_newest_first": out}, nil
}

// settings is the user's settings row, or the defaults before they have one.
func (t *turn) settings(ctx context.Context) (query.Setting, error) {
	st, err := t.s.q.GetSettingsFor(ctx, t.userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return query.Setting{UserID: t.userID, DisplayUnit: "kg", RestDefaultS: 60}, nil
	}
	return st, err
}

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
