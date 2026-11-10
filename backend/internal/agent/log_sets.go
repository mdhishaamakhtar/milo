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
	WorkoutID  string           `json:"workout_id,omitempty"`
	NewWorkout *newWorkoutInput `json:"new_workout,omitempty" jsonschema_description:"A session that isn't in the log yet."`
	Sets       []setInput       `json:"sets"`
}

type newWorkoutInput struct {
	Name      string `json:"name"`
	StartedAt string `json:"started_at" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM."`
	EndedAt   string `json:"ended_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM. Defaults to an hour after the start."`
}

// logSets logs sets of a known exercise; the phone applies them straight away, with Undo.
func (t *turn) logSets(ctx context.Context, in logSetsInput) (any, error) {
	if len(in.Sets) == 0 {
		return nil, badInput("sets can't be empty")
	}
	if err := checkSets(in.Sets); err != nil {
		return nil, err
	}
	exerciseID, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	ex, err := t.visibleExercise(ctx, exerciseID)
	if err != nil {
		return nil, err
	}

	var changes []*milo.Change
	var w workout
	if in.NewWorkout != nil {
		if w, err = t.newWorkout(*in.NewWorkout); err != nil {
			return nil, err
		}
		changes = append(changes, rows.Change(&milo.Workout{
			Id: w.id.String(), Name: w.name, StartedAt: ts(w.startedAt), EndedAt: ts(t.endOf(*in.NewWorkout, w.startedAt)), UpdatedAt: ts(t.now),
		}))
	} else if w, err = t.resolveWorkout(ctx, in.WorkoutID); err != nil {
		return nil, err
	}

	weID, pos, weChanges, err := t.workoutExercise(ctx, w, exerciseID, true)
	if err != nil {
		return nil, err
	}
	changes = append(changes, weChanges...)
	lines := make([]*milo.CardLine, len(in.Sets))
	for i, s := range in.Sets {
		changes = append(changes, rows.Change(s.row(weID, pos+int32(i), t.now)))
		lines[i] = &milo.CardLine{Label: fmt.Sprintf("Set %d", pos+int32(i)), After: s.text(ex.Measurement)}
	}
	t.nextSet[weID] = pos + int32(len(in.Sets))

	subtitle := ex.Name
	if w.id.String() != t.mctx.GetWorkoutId() {
		subtitle += ", " + nameOr(w.name, "workout") + " " + t.day(w.startedAt)
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_LOGGED, Title: fmt.Sprintf("Logged %d %s", len(in.Sets), plural(len(in.Sets), "set", "sets")),
		Subtitle: subtitle, Lines: lines, Changes: changes, AutoApply: true, WorkoutId: w.id.String(), ExerciseId: exerciseID.String(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "workout_id": w.id.String(), "logged": len(in.Sets), "shown": "Applied on the phone straight away; the user can undo it."}, nil
}

// newWorkout starts a workout in this turn for a session that isn't logged yet.
func (t *turn) newWorkout(in newWorkoutInput) (workout, error) {
	started, err := t.parseLocal(in.StartedAt, "started_at")
	if err != nil {
		return workout{}, err
	}
	w := workout{id: newID(), name: in.Name, startedAt: started, created: true}
	t.workouts[w.id] = w
	return w, nil
}

func (t *turn) endOf(in newWorkoutInput, started time.Time) time.Time {
	if end, err := t.parseLocal(in.EndedAt, "ended_at"); err == nil {
		return end
	}
	return started.Add(time.Hour)
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

