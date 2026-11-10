package agent

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// turn is one user message being answered: who, where, and what Milo has
// proposed so far in it.
type turn struct {
	s           *Service
	userID      uuid.UUID
	convID      uuid.UUID
	mctx        *milo.MiloContext
	loc         *time.Location
	now         time.Time
	displayUnit string
	emit        func(*milo.SendResponse) error

	// Rows proposed earlier in this turn, so later tool calls build on them
	// instead of reading a database that doesn't have them yet.
	workouts  map[uuid.UUID]workout       // created by log_sets
	added     map[[2]uuid.UUID]uuid.UUID  // (workout, exercise) → workout exercise
	nextPos   map[uuid.UUID]int32         // workout → next exercise position
	nextSet   map[uuid.UUID]int32         // workout exercise → next set position
}

func newTurn(s *Service, userID, convID uuid.UUID, mctx *milo.MiloContext, loc *time.Location, displayUnit string, emit func(*milo.SendResponse) error) *turn {
	return &turn{
		s: s, userID: userID, convID: convID, mctx: mctx, loc: loc, now: s.now(), displayUnit: displayUnit, emit: emit,
		workouts: map[uuid.UUID]workout{}, added: map[[2]uuid.UUID]uuid.UUID{},
		nextPos: map[uuid.UUID]int32{}, nextSet: map[uuid.UUID]int32{},
	}
}

// day is "Tue 6 Oct" in the user's time zone, with the year when it isn't this one.
func (t *turn) day(at time.Time) string {
	return dayText(at.In(t.loc), t.now.In(t.loc))
}

// workout is where new sets go.
type workout struct {
	id        uuid.UUID
	name      string
	startedAt time.Time
	created   bool // in this turn, so not in the database
}

// resolveWorkout finds the workout to log into: the given id, a workout created
// earlier in this turn, or the one open on screen.
func (t *turn) resolveWorkout(ctx context.Context, workoutID string) (workout, error) {
	if workoutID == "" {
		workoutID = t.mctx.GetWorkoutId()
	}
	if workoutID == "" {
		return workout{}, badInput("there is no open workout: pass workout_id, or new_workout for a session that isn't logged yet")
	}
	id, err := parseUUID(workoutID, "workout_id")
	if err != nil {
		return workout{}, err
	}
	if w, ok := t.workouts[id]; ok {
		return w, nil
	}
	w, err := t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: id, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return workout{}, badInput("no workout %s in this user's log", id)
	}
	if err != nil {
		return workout{}, err
	}
	return workout{id: id, name: w.Name, startedAt: w.StartedAt}, nil
}

// workoutExercise returns where new sets for an exercise go, the position of the
// next set, and the row to create when the exercise isn't in the workout yet.
// With reuse, sets join the exercise if it's already in the workout.
func (t *turn) workoutExercise(ctx context.Context, w workout, exerciseID uuid.UUID, reuse bool) (uuid.UUID, int32, []*milo.Change, error) {
	k := [2]uuid.UUID{w.id, exerciseID}
	if weID, ok := t.added[k]; ok && reuse {
		return weID, t.nextSet[weID], nil, nil
	}
	if reuse && !w.created {
		we, err := t.s.q.FindWorkoutExercise(ctx, query.FindWorkoutExerciseParams{WorkoutID: w.id, ExerciseID: exerciseID, UserID: t.userID})
		if err == nil {
			last, err := t.s.q.MaxSetPosition(ctx, query.MaxSetPositionParams{WorkoutExerciseID: we.ID, UserID: t.userID})
			t.added[k], t.nextSet[we.ID] = we.ID, last+1
			return we.ID, last + 1, nil, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, 0, nil, err
		}
	}
	pos, ok := t.nextPos[w.id]
	if !ok {
		pos = 1
		if !w.created {
			last, err := t.s.q.MaxExercisePosition(ctx, query.MaxExercisePositionParams{WorkoutID: w.id, UserID: t.userID})
			if err != nil {
				return uuid.Nil, 0, nil, err
			}
			pos = last + 1
		}
	}
	t.nextPos[w.id] = pos + 1
	weID := newID()
	t.added[k], t.nextSet[weID] = weID, 1
	we := &milo.WorkoutExercise{Id: weID.String(), WorkoutId: w.id.String(), ExerciseId: exerciseID.String(), Position: pos, UpdatedAt: ts(t.now)}
	return weID, 1, []*milo.Change{rows.Change(we)}, nil
}

func (t *turn) visibleExercise(ctx context.Context, id uuid.UUID) (query.Exercise, error) {
	ex, err := t.s.q.VisibleExercise(ctx, query.VisibleExerciseParams{ID: id, UserID: &t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ex, badInput("no exercise %s in this user's library; search first, or propose a new exercise", id)
	}
	return ex, err
}

func parseUUID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, badInput("%s %q is not a valid id", what, s)
	}
	return id, nil
}

// newID is a time-ordered id for rows Milo proposes.
func newID() uuid.UUID {
	if id, err := uuid.NewV7(); err == nil {
		return id
	}
	return uuid.New()
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
