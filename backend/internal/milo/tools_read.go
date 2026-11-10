package milo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
)

// workoutDetail is a workout with its exercises and their sets.
type workoutDetail struct {
	w         dbq.Workout
	exercises []exerciseDetail
}

type exerciseDetail struct {
	row  dbq.WorkoutExercisesForRow
	sets []dbq.SetsForRow
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

func (t *turn) loadDetails(ctx context.Context, ws []dbq.Workout) ([]workoutDetail, error) {
	if len(ws) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(ws))
	for i, w := range ws {
		ids[i] = w.ID
	}
	wes, err := t.s.q.WorkoutExercisesFor(ctx, dbq.WorkoutExercisesForParams{UserID: t.userID, WorkoutIds: ids})
	if err != nil {
		return nil, fmt.Errorf("workout exercises: %w", err)
	}
	weIDs := make([]uuid.UUID, len(wes))
	for i, we := range wes {
		weIDs[i] = we.ID
	}
	sets, err := t.s.q.SetsFor(ctx, dbq.SetsForParams{UserID: t.userID, WeIds: weIDs})
	if err != nil {
		return nil, fmt.Errorf("sets: %w", err)
	}
	setsByWE := map[uuid.UUID][]dbq.SetsForRow{}
	for _, s := range sets {
		setsByWE[s.WorkoutExerciseID] = append(setsByWE[s.WorkoutExerciseID], s)
	}
	exByWorkout := map[uuid.UUID][]exerciseDetail{}
	for _, we := range wes {
		exByWorkout[we.WorkoutID] = append(exByWorkout[we.WorkoutID], exerciseDetail{row: we, sets: setsByWE[we.ID]})
	}
	out := make([]workoutDetail, len(ws))
	for i, w := range ws {
		out[i] = workoutDetail{w: w, exercises: exByWorkout[w.ID]}
	}
	return out, nil
}

func (t *turn) workoutJSON(d workoutDetail) map[string]any {
	local := d.w.StartedAt.In(t.loc)
	exercises := make([]map[string]any, 0, len(d.exercises))
	for _, e := range d.exercises {
		sets := make([]map[string]any, 0, len(e.sets))
		for i, s := range e.sets {
			sets = append(sets, map[string]any{
				"set_id": s.ID.String(), "n": i + 1, "set": setTextRow(e.row.Measurement, s), "done": s.Done,
			})
		}
		item := map[string]any{
			"workout_exercise_id": e.row.ID.String(),
			"exercise_id":         e.row.ExerciseID.String(),
			"name":                e.row.ExerciseName,
			"measurement":         e.row.Measurement,
			"sets":                sets,
			"volume":              kgText(maths.VolumeKg(setInputs(e.row.Measurement, e.sets)), t.displayUnit),
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
		exercises = append(exercises, item)
	}
	out := map[string]any{
		"workout_id": d.w.ID.String(),
		"name":       d.w.Name,
		"date":       local.Format("Mon 2 Jan 2006"),
		"started":    local.Format("15:04"),
		"exercises":  exercises,
		"volume":     kgText(d.volumeKg(), t.displayUnit),
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

func (t *turn) searchExercises(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, badInput("query can't be empty")
	}
	rows, err := t.s.q.SearchExercises(ctx, dbq.SearchExercisesParams{UserID: t.userID, Query: strings.TrimSpace(in.Query), Lim: 8})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		item := map[string]any{
			"exercise_id": e.ID.String(), "name": e.Name, "measurement": e.Measurement,
			"equipment": e.Equipment, "muscles": e.PrimaryMuscles, "unilateral": e.Unilateral,
			"yours": e.UserID != nil,
		}
		if len(e.Aliases) > 0 {
			item["aliases"] = e.Aliases
		}
		if e.LastDone.After(time.Unix(0, 0)) {
			item["last_done"] = dayText(e.LastDone.In(t.loc), t.now)
		}
		if e.Approximate {
			item["approximate"] = true
		}
		out = append(out, item)
	}
	return map[string]any{"matches": out}, nil
}

// dayBounds is the start of a local calendar day and the start of the next.
func (t *turn) dayBounds(date string) (time.Time, time.Time, error) {
	d, err := time.ParseInLocation("2006-01-02", date, t.loc)
	if err != nil {
		return time.Time{}, time.Time{}, badInput("date %q must be YYYY-MM-DD", date)
	}
	return d, d.AddDate(0, 0, 1), nil
}

func (t *turn) getWorkouts(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		WorkoutIDs []string `json:"workout_ids"`
		Date       string   `json:"date"`
		DateFrom   string   `json:"date_from"`
		DateTo     string   `json:"date_to"`
		Limit      int32    `json:"limit"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	limit = min(limit, 20)

	var ws []dbq.Workout
	var err error
	var rangeText string
	switch {
	case len(in.WorkoutIDs) > 0:
		ids := make([]uuid.UUID, 0, len(in.WorkoutIDs))
		for _, s := range in.WorkoutIDs {
			id, err := parseUUID(s, "workout_id")
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		ws, err = t.s.q.WorkoutsByIDs(ctx, dbq.WorkoutsByIDsParams{UserID: t.userID, Ids: ids})
	case in.Date != "" || in.DateFrom != "" || in.DateTo != "":
		fromDate, toDate := in.DateFrom, in.DateTo
		if in.Date != "" {
			fromDate, toDate = in.Date, in.Date
		}
		if fromDate == "" {
			fromDate = "2000-01-01"
		}
		if toDate == "" {
			toDate = t.now.In(t.loc).Format("2006-01-02")
		}
		from, _, err1 := t.dayBounds(fromDate)
		_, to, err2 := t.dayBounds(toDate)
		if err := errors.Join(err1, err2); err != nil {
			return nil, err
		}
		rangeText = fmt.Sprintf("%s to %s", dayText(from, t.now), dayText(to.AddDate(0, 0, -1), t.now))
		if in.Date != "" {
			rangeText = dayText(from, t.now)
		}
		ws, err = t.s.q.WorkoutsBetween(ctx, dbq.WorkoutsBetweenParams{UserID: t.userID, FromAt: from, ToAt: to, Lim: limit})
	default:
		ws, err = t.s.q.RecentWorkouts(ctx, dbq.RecentWorkoutsParams{UserID: t.userID, Limit: limit})
		sort.Slice(ws, func(i, j int) bool { return ws[i].StartedAt.Before(ws[j].StartedAt) })
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
		d := details[0]
		t.reading(ctx, fmt.Sprintf("Read %s, %s", nameOr(d.w.Name, "a workout"), dayText(d.w.StartedAt.In(t.loc), t.now)))
	case len(details) > 1 && rangeText != "":
		t.reading(ctx, fmt.Sprintf("Read %d workouts, %s", len(details), rangeText))
	case len(details) > 1:
		t.reading(ctx, fmt.Sprintf("Read your last %d workouts", len(details)))
	case rangeText != "":
		t.reading(ctx, "Found no workouts on "+rangeText)
	}

	out := make([]map[string]any, len(details))
	for i, d := range details {
		out[i] = t.workoutJSON(d)
	}
	return map[string]any{"workouts": out}, nil
}

func nameOr(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}

// session is one exercise in one workout.
type session struct {
	workoutID   uuid.UUID
	workoutName string
	at          time.Time
	weID        uuid.UUID
	sets        []dbq.SetsForRow
}

func (t *turn) exerciseSessions(ctx context.Context, exerciseID uuid.UUID, limit int32) (dbq.Exercise, []session, error) {
	ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: exerciseID, UserID: &t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ex, nil, badInput("no exercise %s in this user's library", exerciseID)
	}
	if err != nil {
		return ex, nil, err
	}
	rows, err := t.s.q.ExerciseSessions(ctx, dbq.ExerciseSessionsParams{UserID: t.userID, ExerciseID: exerciseID, Lim: limit})
	if err != nil {
		return ex, nil, err
	}
	weIDs := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		weIDs[i] = r.WorkoutExerciseID
	}
	sets, err := t.s.q.SetsFor(ctx, dbq.SetsForParams{UserID: t.userID, WeIds: weIDs})
	if err != nil {
		return ex, nil, err
	}
	byWE := map[uuid.UUID][]dbq.SetsForRow{}
	for _, s := range sets {
		byWE[s.WorkoutExerciseID] = append(byWE[s.WorkoutExerciseID], s)
	}
	out := make([]session, len(rows))
	for i, r := range rows {
		out[i] = session{workoutID: r.WorkoutID, workoutName: r.WorkoutName, at: r.StartedAt, weID: r.WorkoutExerciseID, sets: byWE[r.WorkoutExerciseID]}
	}
	return ex, out, nil
}

func (t *turn) bodyKg(ctx context.Context) float64 {
	w, err := t.s.q.LatestBodyWeight(ctx, dbq.LatestBodyWeightParams{UserID: t.userID, At: t.now})
	if err != nil || w == nil {
		return 0
	}
	return *w
}

func (t *turn) exerciseHistory(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		ExerciseID string `json:"exercise_id"`
		Limit      int32  `json:"limit"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 12
	}
	limit = min(limit, 40)
	ex, sessions, err := t.exerciseSessions(ctx, id, limit)
	if err != nil {
		return nil, err
	}
	body := t.bodyKg(ctx)

	out := make([]map[string]any, 0, len(sessions))
	oldestFirst := make([]maths.Session, 0, len(sessions))
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		oldestFirst = append(oldestFirst, maths.Session{Date: s.at.In(t.loc).Format("2006-01-02"), Sets: setInputs(ex.Measurement, s.sets)})
	}
	for _, s := range sessions {
		sum := maths.Summarize(setInputs(ex.Measurement, s.sets), body)
		sets := make([]string, 0, len(s.sets))
		for _, set := range s.sets {
			text := setTextRow(ex.Measurement, set)
			if !set.Done {
				text += " (not done)"
			}
			sets = append(sets, text)
		}
		item := map[string]any{
			"date": dayText(s.at.In(t.loc), t.now), "workout": s.workoutName, "workout_id": s.workoutID.String(),
			"sets": sets, "volume": kgText(sum.VolumeKg, t.displayUnit),
		}
		if sum.HasTop {
			item["top_set"] = fmt.Sprintf("%s kg × %d", maths.FormatTenths(sum.TopLoadKg), sum.TopReps)
		}
		if sum.HasE1RM {
			item["e1rm"] = maths.FormatTenths(sum.E1RMKg) + " kg"
		}
		out = append(out, item)
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
		oldest := sessions[len(sessions)-1].at.In(t.loc)
		t.reading(ctx, fmt.Sprintf("Read %d %s sessions since %s", len(sessions), shortName(ex.Name), oldest.Format("Jan 2006")))
	} else {
		t.reading(ctx, "Found no "+shortName(ex.Name)+" sessions")
	}
	return map[string]any{
		"exercise": ex.Name, "measurement": ex.Measurement, "sessions": out, "bests": bests,
		"note": "Top sets and e1RM are effective load in kg (body weight counted for bodyweight and assisted movements).",
	}, nil
}

// shortName drops the equipment in brackets and lower-cases: "Chest Press (Machine)" → "chest press".
func shortName(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

func (t *turn) listTemplates(ctx context.Context) (any, error) {
	rows, err := t.s.q.ListTemplatesFor(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, tpl := range rows {
		items := []map[string]any{}
		for _, it := range templateItems(tpl.Items) {
			name := "(unknown exercise)"
			if id, err := uuid.Parse(it.ExerciseID); err == nil {
				if ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: id, UserID: &t.userID}); err == nil {
					name = ex.Name
				}
			}
			items = append(items, map[string]any{"exercise_id": it.ExerciseID, "name": name, "group": it.GroupLabel})
		}
		out = append(out, map[string]any{"template_id": tpl.ID.String(), "name": tpl.Name, "items": items})
	}
	t.reading(ctx, fmt.Sprintf("Read your %d templates", len(rows)))
	return map[string]any{"templates": out}, nil
}
