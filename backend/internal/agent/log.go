package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

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
	return map[string]any{"card_id": id, "workout_id": w.id.String(), "logged": len(in.Sets), "shown": doneNote}, nil
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
