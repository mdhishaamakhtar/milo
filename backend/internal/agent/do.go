package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// Running a workout: start one, add exercises to it, finish it. Each is done
// on the phone straight away, the same way the buttons do it.

type startWorkoutInput struct {
	Name          string   `json:"name,omitempty" jsonschema_description:"Defaults to the template's or the copied workout's name, else the weekday, e.g. \"Tuesday workout\"."`
	TemplateID    string   `json:"template_id,omitempty" jsonschema_description:"Start from one of their templates."`
	CopyWorkoutID string   `json:"copy_workout_id,omitempty" jsonschema_description:"Do a past workout again: the same exercises in the same order."`
	ExerciseIDs   []string `json:"exercise_ids,omitempty" jsonschema_description:"Or start with just these exercises, in order."`
}

// startWorkout starts a workout now. Each exercise comes with its sets from
// last time, unticked, as it does when the user starts one by hand.
func (t *turn) startWorkout(ctx context.Context, in startWorkoutInput) (any, error) {
	if w, ok, err := t.runningWorkout(ctx); err != nil {
		return nil, err
	} else if ok {
		return nil, badInput("a workout is already running: %q (workout_id %s), started %s. Add to it with add_exercises or log_sets, or finish it first",
			nameOr(w.name, "Workout"), w.id, t.clock(w.startedAt))
	}

	name := strings.TrimSpace(in.Name)
	row := &milo.Workout{Id: newID().String(), StartedAt: ts(t.now), UpdatedAt: ts(t.now)}
	var items []*milo.TemplateItem
	strict := false // exercises the user named must exist; ones a template remembers may have gone
	switch {
	case in.TemplateID != "":
		tpl, err := t.userTemplate(ctx, in.TemplateID)
		if err != nil {
			return nil, err
		}
		items, name, row.TemplateId = rows.TemplateItems(tpl.Items), cmp.Or(name, tpl.Name), tpl.ID.String()
	case in.CopyWorkoutID != "":
		id, err := parseUUID(in.CopyWorkoutID, "copy_workout_id")
		if err != nil {
			return nil, err
		}
		past, err := t.userWorkout(ctx, id)
		if err != nil {
			return nil, err
		}
		wes, err := t.s.q.WorkoutExercisesFor(ctx, query.WorkoutExercisesForParams{UserID: t.userID, WorkoutIds: []uuid.UUID{id}})
		if err != nil {
			return nil, err
		}
		for _, we := range wes {
			items = append(items, &milo.TemplateItem{ExerciseId: we.ExerciseID.String(), GroupLabel: we.GroupLabel})
		}
		name = cmp.Or(name, past.Name)
		if past.TemplateID != nil {
			row.TemplateId = past.TemplateID.String()
		}
	default:
		strict = true
		for _, id := range in.ExerciseIDs {
			items = append(items, &milo.TemplateItem{ExerciseId: id})
		}
	}
	row.Name = cmp.Or(name, t.now.In(t.loc).Format("Monday")+" workout")

	w := workout{id: uuid.MustParse(row.Id), name: row.Name, startedAt: t.now, running: true, created: true}
	t.workouts[w.id] = w
	changes := []*milo.Change{rows.Change(row)}
	fromLastTime := 0
	for _, it := range items {
		ex, err := t.exercise(ctx, it.GetExerciseId())
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		added, filled, err := t.addExercise(ctx, w, ex, it.GetGroupLabel())
		if err != nil {
			return nil, err
		}
		changes = append(append(changes, t.pending(ex.ID)...), added...)
		if filled {
			fromLastTime++
		}
	}

	n := len(t.slots[w.id])
	subtitle := "Empty for now; add exercises as you go"
	if n > 0 {
		subtitle = count(n, "exercise", "exercises")
		if fromLastTime > 0 {
			subtitle += ", sets filled in from last time"
		}
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: "Started " + row.Name, Subtitle: subtitle,
		Changes: changes, AutoApply: true, WorkoutId: row.Id,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "workout_id": row.Id, "exercises": n, "shown": doneNote + " The workout screen opens."}, nil
}

// addExercise puts an exercise at the end of a running workout with the sets
// from last time, unticked; or one empty set if there is no last time.
func (t *turn) addExercise(ctx context.Context, w workout, ex query.Exercise, groupLabel string) (changes []*milo.Change, filled bool, err error) {
	sl, changes, err := t.newSlot(ctx, w, ex.ID, groupLabel)
	if err != nil {
		return nil, false, err
	}
	last, err := t.s.q.LastSessionSets(ctx, query.LastSessionSetsParams{UserID: t.userID, ExerciseID: ex.ID})
	if err != nil {
		return nil, false, err
	}
	if len(last) == 0 {
		unit := t.displayUnit
		if pref, err := t.s.q.ExercisePrefFor(ctx, query.ExercisePrefForParams{UserID: t.userID, ExerciseID: ex.ID}); err == nil {
			unit = pref.Unit
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, err
		}
		last = []query.Set{{Kind: "normal", WeightUnit: unit, PerSide: ex.Unilateral}}
	} else {
		filled = true
	}
	for i, s := range last {
		s.ID, s.WorkoutExerciseID, s.Position, s.Done, s.UpdatedAt = newID(), uuid.MustParse(sl.we.Id), int32(i+1), false, t.now
		row := rows.Set(s)
		sl.sets = append(sl.sets, row)
		changes = append(changes, rows.Change(row))
	}
	return changes, filled, nil
}

type addExercisesInput struct {
	WorkoutID   string   `json:"workout_id,omitempty" jsonschema_description:"Defaults to the workout on screen, else the one running."`
	ExerciseIDs []string `json:"exercise_ids"`
	Superset    bool     `json:"superset,omitempty" jsonschema_description:"These exercises are done back to back as one superset."`
}

// addExercises adds exercises to the running workout, ready to tick off.
func (t *turn) addExercises(ctx context.Context, in addExercisesInput) (any, error) {
	if len(in.ExerciseIDs) == 0 {
		return nil, badInput("exercise_ids can't be empty")
	}
	if in.Superset && len(in.ExerciseIDs) < 2 {
		return nil, badInput("a superset needs at least two exercises")
	}
	w, err := t.resolveWorkout(ctx, in.WorkoutID)
	if err != nil {
		return nil, err
	}
	if !w.running {
		return nil, badInput("%q is finished. To add what was done in it, use log_sets with its workout_id; that adds the exercise too", nameOr(w.name, "That workout"))
	}
	group := ""
	if in.Superset {
		group = newID().String()[:8]
	}
	var changes []*milo.Change
	var names []string
	for _, id := range in.ExerciseIDs {
		ex, err := t.exercise(ctx, id)
		if err != nil {
			return nil, err
		}
		added, _, err := t.addExercise(ctx, w, ex, group)
		if err != nil {
			return nil, err
		}
		changes = append(append(changes, t.pending(ex.ID)...), added...)
		names = append(names, ex.Name)
	}
	title := "Added " + names[0]
	subtitle := "To " + nameOr(w.name, "your workout")
	if len(names) > 1 {
		title = "Added " + count(len(names), "exercise", "exercises")
		subtitle = strings.Join(names, ", ")
		if in.Superset {
			title += " as a superset"
		}
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: title, Subtitle: subtitle,
		Changes: changes, AutoApply: true, WorkoutId: w.id.String(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "workout_id": w.id.String(), "shown": doneNote}, nil
}

type finishWorkoutInput struct {
	WorkoutID string `json:"workout_id,omitempty" jsonschema_description:"Defaults to the one running."`
	Name      string `json:"name,omitempty" jsonschema_description:"Only to rename it while finishing."`
	Notes     string `json:"notes,omitempty" jsonschema_description:"A note to keep with the workout."`
	EndedAt   string `json:"ended_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM, if they stopped earlier than now."`
}

// finishWorkout ends the running workout: sets never ticked are dropped, and so
// is an exercise left with none, exactly as the Finish button does.
func (t *turn) finishWorkout(ctx context.Context, in finishWorkoutInput) (any, error) {
	w, err := t.resolveWorkout(ctx, in.WorkoutID)
	if err != nil {
		return nil, err
	}
	if !w.running {
		return nil, badInput("%q is already finished", nameOr(w.name, "That workout"))
	}
	if w.created {
		return nil, badInput("that workout was only just started in this message; finish it in a later one")
	}
	row, err := t.userWorkout(ctx, w.id)
	if err != nil {
		return nil, err
	}
	ended := t.now
	if in.EndedAt != "" {
		if ended, err = t.parseLocal(in.EndedAt, "ended_at"); err != nil {
			return nil, err
		}
		if !ended.After(row.StartedAt) || ended.After(t.now.Add(time.Minute)) {
			return nil, badInput("ended_at must be after the start (%s) and not in the future", t.clock(row.StartedAt))
		}
	}
	slots, err := t.slotsOf(ctx, w)
	if err != nil {
		return nil, err
	}

	var changes []*milo.Change
	var done []maths.SetInput
	doneSets, dropped := 0, 0
	for _, sl := range slots {
		kept := 0
		for _, s := range sl.sets {
			if s.GetDone() {
				kept++
				continue
			}
			dropped++
			s.Deleted, s.UpdatedAt = true, ts(t.now)
			changes = append(changes, rows.Change(s))
		}
		if kept == 0 {
			sl.we.Deleted, sl.we.UpdatedAt = true, ts(t.now)
			changes = append(changes, rows.Change(sl.we))
			continue
		}
		ex, err := t.exercise(ctx, sl.we.GetExerciseId())
		if err != nil {
			return nil, err
		}
		doneSets += kept
		for _, s := range sl.sets {
			if s.GetDone() {
				done = append(done, maths.SetInput{Measurement: ex.Measurement, Weight: s.GetWeight(), Unit: rows.Name(s.GetWeightUnit()), Reps: int(s.GetReps()), PerSide: s.GetPerSide(), Done: true, Kind: rows.Name(s.GetKind())})
			}
		}
	}
	if doneSets == 0 {
		return nil, badInput("no sets are ticked in %q, so there is nothing to save. Ask whether they want to discard it (propose_delete) or log what they did first", nameOr(w.name, "this workout"))
	}

	finished := rows.Workout(row)
	finished.Name = cmp.Or(strings.TrimSpace(in.Name), row.Name)
	finished.Notes = cmp.Or(strings.TrimSpace(in.Notes), row.Notes)
	finished.EndedAt, finished.UpdatedAt = ts(ended), ts(t.now)
	changes = append(changes, rows.Change(finished))
	w.name, w.running = finished.Name, false
	t.workouts[w.id] = w

	subtitle := fmt.Sprintf("%d min, %s", minutesBetween(row.StartedAt, &ended), count(doneSets, "set", "sets"))
	if v := maths.VolumeKg(done); v > 0 {
		subtitle += ", " + kgText(v, t.displayUnit)
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: "Finished " + nameOr(finished.Name, "workout"), Subtitle: subtitle,
		Changes: changes, AutoApply: true, WorkoutId: w.id.String(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"card_id": id, "workout_id": w.id.String(), "summary": subtitle, "unticked_sets_dropped": dropped,
		"shown": doneNote + " It is saved to Apple Health if they have that switched on.",
	}, nil
}

// clock is "Tue 6 Oct 18:30" in the user's time zone.
func (t *turn) clock(at time.Time) string {
	return t.day(at) + " " + at.In(t.loc).Format("15:04")
}

type logSetsInput struct {
	ExerciseID string           `json:"exercise_id"`
	WorkoutID  string           `json:"workout_id,omitempty" jsonschema_description:"Defaults to the workout on screen, else the one running."`
	NewWorkout *newWorkoutInput `json:"new_workout,omitempty" jsonschema_description:"A session that already happened and isn't in the log yet."`
	Sets       []setInput       `json:"sets"`
}

type newWorkoutInput struct {
	Name      string `json:"name"`
	StartedAt string `json:"started_at" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM."`
	EndedAt   string `json:"ended_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM. Defaults to an hour after the start."`
}

// logSets logs done sets of a known exercise; the phone applies them straight away, with Undo.
func (t *turn) logSets(ctx context.Context, in logSetsInput) (any, error) {
	if len(in.Sets) == 0 {
		return nil, badInput("sets can't be empty")
	}
	if err := checkSets(in.Sets); err != nil {
		return nil, err
	}
	ex, err := t.exercise(ctx, in.ExerciseID)
	if err != nil {
		return nil, err
	}

	var changes []*milo.Change
	var w workout
	if in.NewWorkout != nil {
		var created *milo.Change
		if w, created, err = t.newWorkout(*in.NewWorkout); err != nil {
			return nil, err
		}
		changes = append(changes, created)
	} else if w, err = t.resolveWorkout(ctx, in.WorkoutID); err != nil {
		return nil, err
	}

	sl, added, err := t.slot(ctx, w, ex.ID)
	if err != nil {
		return nil, err
	}
	logged, numbers := sl.log(in.Sets, t.now)
	changes = append(append(append(changes, t.pending(ex.ID)...), added...), logged...)
	lines := make([]*milo.CardLine, len(in.Sets))
	for i, s := range in.Sets {
		lines[i] = &milo.CardLine{Label: fmt.Sprintf("Set %d", numbers[i]), After: s.text(ex.Measurement)}
	}

	subtitle := ex.Name
	if w.id.String() != t.mctx.GetWorkoutId() && !w.running {
		subtitle += ", " + nameOr(w.name, "workout") + " " + t.day(w.startedAt)
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: "Logged " + count(len(in.Sets), "set", "sets"),
		Subtitle: subtitle, Lines: lines, Changes: changes, AutoApply: true, WorkoutId: w.id.String(), ExerciseId: ex.ID.String(),
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"card_id": id, "workout_id": w.id.String(), "logged": len(in.Sets), "shown": doneNote}
	if best := t.newBest(ctx, ex, in.Sets); best != "" {
		out["new_best"] = best
	}
	return out, nil
}

// newBest says whether the sets just logged beat everything logged before on
// this exercise, so Milo can tell the user: "Heaviest set yet: 62.5 kg × 8 (before: 60 kg × 8 on Tue 10 Nov)".
func (t *turn) newBest(ctx context.Context, ex query.Exercise, sets []setInput) string {
	_, sessions, err := t.sessions(ctx, ex.ID, 60)
	if err != nil {
		return ""
	}
	body := t.bodyKg(ctx)
	var before mark
	for _, s := range sessions {
		if m, ok := topMark(exerciseDetail{row: query.WorkoutExercisesForRow{Measurement: ex.Measurement}, sets: s.sets}, s.at, body); ok && m.kg > before.kg {
			before = m
		}
	}
	logged := make([]query.Set, len(sets))
	for i, in := range sets {
		logged[i] = query.Set{Weight: in.Weight, WeightUnit: in.Unit, Reps: in.Reps, PerSide: in.PerSide, Kind: in.Kind, Done: true}
	}
	now, ok := topMark(exerciseDetail{row: query.WorkoutExercisesForRow{Measurement: ex.Measurement}, sets: logged}, t.now, body)
	if !ok || before.kg == 0 || now.kg <= before.kg+1e-9 {
		return ""
	}
	return fmt.Sprintf("Heaviest set yet on %s: %s (before: %s on %s). Worth a word.", ex.Name, now.text, before.text, t.day(before.at))
}

// doneNote is what Milo is told after something was applied without a tap.
const doneNote = "Done on the phone straight away; the user can undo it."

// newWorkout creates a finished workout in this turn for a session that isn't logged yet.
func (t *turn) newWorkout(in newWorkoutInput) (workout, *milo.Change, error) {
	started, err := t.parseLocal(in.StartedAt, "started_at")
	if err != nil {
		return workout{}, nil, err
	}
	ended := started.Add(time.Hour)
	if in.EndedAt != "" {
		if ended, err = t.parseLocal(in.EndedAt, "ended_at"); err != nil {
			return workout{}, nil, err
		}
		if !ended.After(started) {
			return workout{}, nil, badInput("ended_at must be after started_at")
		}
	}
	w := workout{id: newID(), name: in.Name, startedAt: started, created: true}
	t.workouts[w.id] = w
	row := &milo.Workout{Id: w.id.String(), Name: w.name, StartedAt: ts(started), EndedAt: ts(ended), UpdatedAt: ts(t.now)}
	return w, rows.Change(row), nil
}

// parseLocal reads "2006-01-02T15:04" in the user's zone, or RFC 3339.
func (t *turn) parseLocal(s, what string) (time.Time, error) {
	if v, err := time.ParseInLocation("2006-01-02T15:04", s, t.loc); err == nil {
		return v, nil
	}
	if v, err := time.Parse(time.RFC3339, s); err == nil {
		return v, nil
	}
	return time.Time{}, badInput("%s %q must look like 2026-11-09T18:30", what, s)
}

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

// Body readings and settings: log a reading, change a setting, set how one exercise is logged.

type logBodyInput struct {
	Weight     string   `json:"weight,omitempty" jsonschema_description:"Body weight as said, e.g. \"82.4\"."`
	Unit       string   `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb" jsonschema_description:"Defaults to the unit their totals are shown in."`
	BodyFatPct *float64 `json:"body_fat_pct,omitempty"`
	MeasuredAt string   `json:"measured_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM. Defaults to now."`
}

// logBody adds a body weight or body fat reading; applied straight away, with Undo.
func (t *turn) logBody(ctx context.Context, in logBodyInput) (any, error) {
	row := &milo.BodyMeasurement{Id: newID().String(), MeasuredAt: ts(t.now), Source: "manual", UpdatedAt: ts(t.now)}
	if in.MeasuredAt != "" {
		at, err := t.parseLocal(in.MeasuredAt, "measured_at")
		if err != nil {
			return nil, err
		}
		if at.After(t.now.Add(time.Minute)) {
			return nil, badInput("a reading can't be in the future")
		}
		row.MeasuredAt = ts(at)
	}
	if w := strings.TrimSpace(in.Weight); w != "" {
		unit := in.Unit
		if unit == "" {
			unit = t.displayUnit
		}
		kg, ok := maths.ToKg(w, unit)
		if !rows.ValidWeight(w) || !ok || kg < 20 || kg > 400 {
			return nil, badInput("weight %q %s doesn't look like a body weight", w, unit)
		}
		row.WeightKg = &kg
	}
	if in.BodyFatPct != nil {
		if *in.BodyFatPct < 2 || *in.BodyFatPct > 70 {
			return nil, badInput("body fat %.1f%% is out of range", *in.BodyFatPct)
		}
		row.BodyFatPct = in.BodyFatPct
	}
	if row.WeightKg == nil && row.BodyFatPct == nil {
		return nil, badInput("give a weight, a body fat percentage, or both")
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: "Logged " + readingText(row.WeightKg, row.BodyFatPct, t.displayUnit),
		Subtitle: "Body reading, " + t.day(row.MeasuredAt.AsTime()), Changes: []*milo.Change{rows.Change(row)}, AutoApply: true,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "reading_id": row.Id, "shown": doneNote}, nil
}

type settingsInput struct {
	DisplayUnit  string   `json:"display_unit,omitempty" jsonschema:"enum=kg,enum=lb" jsonschema_description:"The unit totals and charts are shown in. Logged weights keep their own unit."`
	RestDefaultS *int32   `json:"rest_default_s,omitempty" jsonschema_description:"The default rest timer, in seconds."`
	HeightCm     *float64 `json:"height_cm,omitempty"`
	BirthDate    *string  `json:"birth_date,omitempty" jsonschema_description:"YYYY-MM-DD; \"\" clears it."`
	Sex          *string  `json:"sex,omitempty" jsonschema:"enum=female,enum=male,enum=other,enum=" jsonschema_description:"\"\" clears it."`
}

// updateSettings changes the user's settings; applied straight away, with Undo.
func (t *turn) updateSettings(ctx context.Context, in settingsInput) (any, error) {
	st, err := t.settings(ctx)
	if err != nil {
		return nil, err
	}
	var said []string
	if u := in.DisplayUnit; u != "" && u != st.DisplayUnit {
		if u != "kg" && u != "lb" {
			return nil, badInput("display_unit must be kg or lb")
		}
		st.DisplayUnit = u
		said = append(said, "Totals and charts in "+unitName(u))
	}
	if r := in.RestDefaultS; r != nil && *r != st.RestDefaultS {
		if *r < 10 || *r > 900 {
			return nil, badInput("rest_default_s must be between 10 and 900 seconds")
		}
		st.RestDefaultS = *r
		said = append(said, "Rest timer "+durationText(int(*r)))
	}
	if h := in.HeightCm; h != nil {
		if *h < 50 || *h > 260 {
			return nil, badInput("height_cm must be between 50 and 260")
		}
		st.HeightCm = h
		said = append(said, fmt.Sprintf("Height %.0f cm", *h))
	}
	if b := in.BirthDate; b != nil && *b != st.BirthDate {
		if *b != "" {
			born, err := time.Parse(time.DateOnly, *b)
			if err != nil || born.After(t.now) {
				return nil, badInput("birth_date %q must be a past date, YYYY-MM-DD", *b)
			}
		}
		st.BirthDate = *b
		said = append(said, "Date of birth "+orNone(*b))
	}
	if s := in.Sex; s != nil && *s != st.Sex {
		st.Sex = *s
		said = append(said, "Sex "+orNone(*s))
	}
	if len(said) == 0 {
		return nil, badInput("nothing here differs from their current settings")
	}
	st.UpdatedAt = t.now
	title, subtitle := said[0], ""
	if len(said) > 1 {
		title, subtitle = "Settings updated", strings.Join(said, ", ")
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: title, Subtitle: subtitle,
		Changes: []*milo.Change{rows.Change(rows.Settings(st))}, AutoApply: true,
	})
	if err != nil {
		return nil, err
	}
	t.displayUnit = st.DisplayUnit
	return map[string]any{"card_id": id, "shown": doneNote}, nil
}

type exercisePrefsInput struct {
	ExerciseID string `json:"exercise_id"`
	Unit       string `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb" jsonschema_description:"The unit this exercise is logged in from now on."`
	RestS      *int32 `json:"rest_s,omitempty" jsonschema_description:"This exercise's own rest timer in seconds; 0 goes back to the default."`
}

// setExercisePreferences sets the unit and rest timer one exercise uses.
func (t *turn) setExercisePreferences(ctx context.Context, in exercisePrefsInput) (any, error) {
	ex, err := t.exercise(ctx, in.ExerciseID)
	if err != nil {
		return nil, err
	}
	row := &milo.ExercisePref{ExerciseId: ex.ID.String(), Unit: rows.Parse[milo.WeightUnit](t.displayUnit)}
	stored, err := t.s.q.ExercisePrefFor(ctx, query.ExercisePrefForParams{UserID: t.userID, ExerciseID: ex.ID})
	if err == nil {
		row = rows.ExercisePref(stored)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var said []string
	if u := rows.Parse[milo.WeightUnit](in.Unit); u != milo.WeightUnit_WEIGHT_UNIT_UNSPECIFIED && u != row.Unit {
		row.Unit = u
		said = append(said, "logged in "+unitName(in.Unit))
	}
	if r := in.RestS; r != nil {
		switch {
		case *r == 0 && row.RestS != nil:
			row.RestS = nil
			said = append(said, "rest back to the default")
		case *r < 0 || *r > 900:
			return nil, badInput("rest_s must be between 10 and 900 seconds, or 0 for the default")
		case *r > 0 && (row.RestS == nil || *row.RestS != *r):
			row.RestS = r
			said = append(said, "rest "+durationText(int(*r)))
		}
	}
	if len(said) == 0 {
		return nil, badInput("%s is already set that way", ex.Name)
	}
	row.UpdatedAt = ts(t.now)
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: ex.Name, Subtitle: sentence(strings.Join(said, ", ")),
		Changes: []*milo.Change{rows.Change(row)}, AutoApply: true, ExerciseId: ex.ID.String(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": doneNote}, nil
}
