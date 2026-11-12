package sync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// pushChanges writes a user's changes inside tx. Call it in its own transaction: it
// takes the locks that keep this user's server_seq values committing in order,
// so a pull can never skip a row. A write older than the stored row is skipped;
// a row that belongs to someone else (or a built-in exercise) fails the push.
func pushChanges(ctx context.Context, tx pgx.Tx, userID uuid.UUID, changes []*milo.Change) error {
	q := query.New(tx)
	if err := q.LockAgainstBuiltinInserts(ctx); err != nil {
		return err
	}
	if err := q.LockUserWrites(ctx, userID.String()); err != nil {
		return err
	}
	for _, c := range changes {
		if err := push(ctx, q, userID, c); err != nil {
			return err
		}
	}
	return nil
}

func push(ctx context.Context, q *query.Queries, userID uuid.UUID, c *milo.Change) error {
	switch r := c.GetRow().(type) {
	case *milo.Change_Exercise:
		e := r.Exercise
		id, at, err := key(e.GetId(), e.GetUpdatedAt(), "exercise")
		if err != nil {
			return err
		}
		if e.GetBuiltin() {
			return notYours("built-in exercise", id)
		}
		if e.GetName() == "" {
			return invalid("exercise %s needs a name", id)
		}
		n, err := q.UpsertExercise(ctx, query.UpsertExerciseParams{
			ID: id, UserID: &userID, Name: e.GetName(), Aliases: orEmpty(e.GetAliases()),
			PrimaryMuscles: orEmpty(e.GetPrimaryMuscles()), SecondaryMuscles: orEmpty(e.GetSecondaryMuscles()),
			MovementPattern: e.GetMovementPattern(), Equipment: e.GetEquipment(),
			Measurement: cmp.Or(rows.Name(e.GetMeasurement()), "reps_load"), Unilateral: e.GetUnilateral(), Met: e.Met,
			Description: e.GetDescription(), Approximate: e.GetApproximate(), UpdatedAt: at, DeletedAt: tombstone(e.GetDeleted(), at),
		})
		return landed(n, err, userID, "exercise", id, func() (uuid.UUID, error) {
			owner, err := q.ExerciseOwner(ctx, id)
			if owner == nil {
				return uuid.Nil, err // built-in
			}
			return *owner, err
		})

	case *milo.Change_ExercisePref:
		p := r.ExercisePref
		id, at, err := key(p.GetExerciseId(), p.GetUpdatedAt(), "exercise pref")
		if err != nil {
			return err
		}
		// Keyed by (user, exercise), so it can't be someone else's.
		_, err = q.UpsertExercisePref(ctx, query.UpsertExercisePrefParams{
			UserID: userID, ExerciseID: id, Unit: cmp.Or(rows.Name(p.GetUnit()), "kg"), RestS: p.RestS,
			UpdatedAt: at, DeletedAt: tombstone(p.GetDeleted(), at),
		})
		return err

	case *milo.Change_Settings:
		s := r.Settings
		_, at, err := key(userID.String(), s.GetUpdatedAt(), "settings")
		if err != nil {
			return err
		}
		_, err = q.UpsertSettings(ctx, query.UpsertSettingsParams{
			UserID: userID, DisplayUnit: cmp.Or(rows.Name(s.GetDisplayUnit()), "kg"), RestDefaultS: max(0, s.GetRestDefaultS()),
			HeightCm: s.HeightCm, BirthDate: s.GetBirthDate(), Sex: s.GetSex(), UpdatedAt: at,
		})
		return err

	case *milo.Change_BodyMeasurement:
		b := r.BodyMeasurement
		id, at, err := key(b.GetId(), b.GetUpdatedAt(), "body measurement")
		if err != nil {
			return err
		}
		if !b.GetMeasuredAt().IsValid() {
			return invalid("body measurement %s needs measured_at", id)
		}
		n, err := q.UpsertBodyMeasurement(ctx, query.UpsertBodyMeasurementParams{
			ID: id, UserID: userID, MeasuredAt: b.GetMeasuredAt().AsTime(), WeightKg: b.WeightKg, BodyFatPct: b.BodyFatPct,
			Source: cmp.Or(b.GetSource(), "manual"), UpdatedAt: at, DeletedAt: tombstone(b.GetDeleted(), at),
		})
		return landed(n, err, userID, "body measurement", id, func() (uuid.UUID, error) { return q.BodyMeasurementOwner(ctx, id) })

	case *milo.Change_Workout:
		w := r.Workout
		id, at, err := key(w.GetId(), w.GetUpdatedAt(), "workout")
		if err != nil {
			return err
		}
		if !w.GetStartedAt().IsValid() {
			return invalid("workout %s needs started_at", id)
		}
		templateID, err1 := optionalID(w.GetTemplateId())
		healthID, err2 := optionalID(w.GetHealthUuid())
		if err := errors.Join(err1, err2); err != nil {
			return err
		}
		n, err := q.UpsertWorkout(ctx, query.UpsertWorkoutParams{
			ID: id, UserID: userID, Name: w.GetName(), Notes: w.GetNotes(), StartedAt: w.GetStartedAt().AsTime(),
			EndedAt: optionalTime(w.GetEndedAt()), TemplateID: templateID, HealthUuid: healthID,
			UpdatedAt: at, DeletedAt: tombstone(w.GetDeleted(), at),
		})
		return landed(n, err, userID, "workout", id, func() (uuid.UUID, error) { return q.WorkoutOwner(ctx, id) })

	case *milo.Change_WorkoutExercise:
		we := r.WorkoutExercise
		id, at, err := key(we.GetId(), we.GetUpdatedAt(), "workout exercise")
		if err != nil {
			return err
		}
		workoutID, err1 := uuid.Parse(we.GetWorkoutId())
		exerciseID, err2 := uuid.Parse(we.GetExerciseId())
		if errors.Join(err1, err2) != nil {
			return invalid("workout exercise %s needs a workout_id and an exercise_id", id)
		}
		n, err := q.UpsertWorkoutExercise(ctx, query.UpsertWorkoutExerciseParams{
			ID: id, UserID: userID, WorkoutID: workoutID, ExerciseID: exerciseID, Position: we.GetPosition(),
			GroupLabel: we.GetGroupLabel(), Notes: we.GetNotes(), UpdatedAt: at, DeletedAt: tombstone(we.GetDeleted(), at),
		})
		return landed(n, err, userID, "workout exercise", id, func() (uuid.UUID, error) { return q.WorkoutExerciseOwner(ctx, id) })

	case *milo.Change_Set:
		s := r.Set
		id, at, err := key(s.GetId(), s.GetUpdatedAt(), "set")
		if err != nil {
			return err
		}
		weID, err := uuid.Parse(s.GetWorkoutExerciseId())
		if err != nil {
			return invalid("set %s needs a workout_exercise_id", id)
		}
		if !rows.ValidWeight(s.GetWeight()) {
			return invalid("weight %q must be a plain decimal with at most 3 places", s.GetWeight())
		}
		n, err := q.UpsertSet(ctx, query.UpsertSetParams{
			ID: id, UserID: userID, WorkoutExerciseID: weID, Position: s.GetPosition(), Kind: cmp.Or(rows.Name(s.GetKind()), "normal"),
			Reps: s.Reps, Weight: s.GetWeight(), WeightUnit: cmp.Or(rows.Name(s.GetWeightUnit()), "kg"), PerSide: s.GetPerSide(),
			DurationS: s.DurationS, DistanceM: s.DistanceM, Rpe: s.Rpe, Done: s.GetDone(),
			UpdatedAt: at, DeletedAt: tombstone(s.GetDeleted(), at),
		})
		return landed(n, err, userID, "set", id, func() (uuid.UUID, error) { return q.SetOwner(ctx, id) })

	case *milo.Change_Template:
		t := r.Template
		id, at, err := key(t.GetId(), t.GetUpdatedAt(), "template")
		if err != nil {
			return err
		}
		if t.GetName() == "" && !t.GetDeleted() {
			return invalid("template %s needs a name", id)
		}
		for _, it := range t.GetItems() {
			if _, err := uuid.Parse(it.GetExerciseId()); err != nil {
				return invalid("template %s has an item without a valid exercise_id", id)
			}
		}
		items, err := rows.TemplateItemsJSON(t.GetItems())
		if err != nil {
			return err
		}
		n, err := q.UpsertTemplate(ctx, query.UpsertTemplateParams{
			ID: id, UserID: userID, Name: t.GetName(), Notes: t.GetNotes(), Items: items,
			UpdatedAt: at, DeletedAt: tombstone(t.GetDeleted(), at),
		})
		return landed(n, err, userID, "template", id, func() (uuid.UUID, error) { return q.TemplateOwner(ctx, id) })
	}
	return invalid("change has no row")
}

// landed checks an upsert. No row written means the stored row is newer (fine)
// or belongs to someone else (refused).
func landed(n int64, err error, userID uuid.UUID, what string, id uuid.UUID, owner func() (uuid.UUID, error)) error {
	if err != nil {
		return fmt.Errorf("%s %s: %w", what, id, err)
	}
	if n > 0 {
		return nil
	}
	o, err := owner()
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return err
	case o != userID:
		return notYours(what, id)
	}
	return nil
}

func key(id string, updated *timestamppb.Timestamp, what string) (uuid.UUID, time.Time, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, time.Time{}, invalid("%s id %q is not a UUID", what, id)
	}
	if !updated.IsValid() {
		return uuid.Nil, time.Time{}, invalid("%s %s needs updated_at", what, id)
	}
	return parsed, updated.AsTime(), nil
}

func optionalID(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, invalid("%q is not a UUID", s)
	}
	return &id, nil
}

func optionalTime(t *timestamppb.Timestamp) *time.Time {
	if !t.IsValid() {
		return nil
	}
	v := t.AsTime()
	return &v
}

// tombstone is the deleted_at to store: the write's own time when deleted.
func tombstone(deleted bool, at time.Time) *time.Time {
	if !deleted {
		return nil
	}
	return &at
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func invalid(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf(format, args...))
}

func notYours(what string, id uuid.UUID) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Sprintf("%s %s isn't yours to change", what, id))
}
