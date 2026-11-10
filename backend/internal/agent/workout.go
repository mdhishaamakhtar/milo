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
