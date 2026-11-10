package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

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
	out := make([]map[string]any, len(details))
	for i, d := range details {
		out[i] = t.workoutJSON(d)
	}
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

func (t *turn) workoutJSON(d workoutDetail) map[string]any {
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

// shortName drops the equipment in brackets: "Chest Press (Machine)" → "chest press".
func shortName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

func nameOr(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}
