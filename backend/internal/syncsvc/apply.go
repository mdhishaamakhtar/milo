// Package syncsvc is the sync service: phones push changed rows and pull
// everything newer than their cursor. Last write wins per row.
package syncsvc

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

func notYours(what string, id uuid.UUID) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Sprintf("%s %s isn't yours to change", what, id))
}

// ApplyChanges writes a user's changes inside tx. Call it in its own
// transaction: it takes the locks that keep server_seq commits in order.
// An older write than the stored row is skipped silently; a row owned by
// someone else (or a built-in exercise) fails the whole push.
func ApplyChanges(ctx context.Context, tx pgx.Tx, userID uuid.UUID, changes []*milov1.Change) error {
	q := dbq.New(tx)
	if err := q.LockSharedWritesShared(ctx); err != nil {
		return fmt.Errorf("lock shared: %w", err)
	}
	if err := q.LockUserWrites(ctx, userID.String()); err != nil {
		return fmt.Errorf("lock user: %w", err)
	}
	for _, c := range changes {
		if err := applyOne(ctx, q, userID, c); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, q *dbq.Queries, userID uuid.UUID, c *milov1.Change) error {
	switch row := c.GetRow().(type) {
	case *milov1.Change_Exercise:
		return applyExercise(ctx, q, userID, row.Exercise)
	case *milov1.Change_ExercisePref:
		return applyExercisePref(ctx, q, userID, row.ExercisePref)
	case *milov1.Change_Settings:
		return applySettings(ctx, q, userID, row.Settings)
	case *milov1.Change_BodyMeasurement:
		return applyBodyMeasurement(ctx, q, userID, row.BodyMeasurement)
	case *milov1.Change_Workout:
		return applyWorkout(ctx, q, userID, row.Workout)
	case *milov1.Change_WorkoutExercise:
		return applyWorkoutExercise(ctx, q, userID, row.WorkoutExercise)
	case *milov1.Change_Set:
		return applySet(ctx, q, userID, row.Set)
	case *milov1.Change_Template:
		return applyTemplate(ctx, q, userID, row.Template)
	default:
		return invalid("change has no row")
	}
}

// checkOwner runs after an upsert changed nothing: either the stored row is
// newer (fine) or it belongs to someone else (refused).
func checkOwner(owner uuid.UUID, err error, userID uuid.UUID, what string, id uuid.UUID) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s owner: %w", what, err)
	}
	if owner != userID {
		return notYours(what, id)
	}
	return nil
}

func applyExercise(ctx context.Context, q *dbq.Queries, userID uuid.UUID, e *milov1.Exercise) error {
	id, err := parseID(e.GetId(), "exercise")
	if err != nil {
		return err
	}
	if e.GetBuiltin() {
		return notYours("built-in exercise", id)
	}
	updated, err := requiredTime(e.GetUpdatedAt(), "exercise "+e.GetId())
	if err != nil {
		return err
	}
	if e.GetName() == "" {
		return invalid("exercise %s needs a name", id)
	}
	n, err := q.UpsertExercise(ctx, dbq.UpsertExerciseParams{
		ID: id, UserID: &userID, Name: e.GetName(), Aliases: nonNil(e.GetAliases()),
		PrimaryMuscles: nonNil(e.GetPrimaryMuscles()), SecondaryMuscles: nonNil(e.GetSecondaryMuscles()),
		MovementPattern: e.GetMovementPattern(), Equipment: e.GetEquipment(), Measurement: measurement(e.GetMeasurement()),
		Unilateral: e.GetUnilateral(), Met: e.Met, Description: e.GetDescription(), Approximate: e.GetApproximate(),
		UpdatedAt: updated, DeletedAt: tombstone(e.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("exercise %s: %w", id, err)
	}
	if n == 0 {
		owner, err := q.ExerciseOwner(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("exercise owner: %w", err)
		}
		if owner == nil || *owner != userID {
			return notYours("exercise", id)
		}
	}
	return nil
}

func applyExercisePref(ctx context.Context, q *dbq.Queries, userID uuid.UUID, p *milov1.ExercisePref) error {
	exerciseID, err := parseID(p.GetExerciseId(), "exercise pref")
	if err != nil {
		return err
	}
	updated, err := requiredTime(p.GetUpdatedAt(), "exercise pref "+p.GetExerciseId())
	if err != nil {
		return err
	}
	_, err = q.UpsertExercisePref(ctx, dbq.UpsertExercisePrefParams{
		UserID: userID, ExerciseID: exerciseID, Unit: unit(p.GetUnit()), RestS: p.RestS,
		UpdatedAt: updated, DeletedAt: tombstone(p.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("exercise pref %s: %w", exerciseID, err)
	}
	return nil
}

func applySettings(ctx context.Context, q *dbq.Queries, userID uuid.UUID, s *milov1.Settings) error {
	updated, err := requiredTime(s.GetUpdatedAt(), "settings")
	if err != nil {
		return err
	}
	if s.GetRestDefaultS() < 0 {
		return invalid("rest default can't be negative")
	}
	_, err = q.UpsertSettings(ctx, dbq.UpsertSettingsParams{
		UserID: userID, DisplayUnit: unit(s.GetDisplayUnit()), RestDefaultS: s.GetRestDefaultS(),
		HeightCm: s.HeightCm, BirthDate: s.GetBirthDate(), Sex: s.GetSex(), UpdatedAt: updated,
	})
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	return nil
}

func applyBodyMeasurement(ctx context.Context, q *dbq.Queries, userID uuid.UUID, b *milov1.BodyMeasurement) error {
	id, err := parseID(b.GetId(), "body measurement")
	if err != nil {
		return err
	}
	updated, err := requiredTime(b.GetUpdatedAt(), "body measurement "+b.GetId())
	if err != nil {
		return err
	}
	measured, err := requiredTime(b.GetMeasuredAt(), "body measurement "+b.GetId()+" measured_at")
	if err != nil {
		return err
	}
	source := b.GetSource()
	if source == "" {
		source = "manual"
	}
	n, err := q.UpsertBodyMeasurement(ctx, dbq.UpsertBodyMeasurementParams{
		ID: id, UserID: userID, MeasuredAt: measured, WeightKg: b.WeightKg, BodyFatPct: b.BodyFatPct,
		Source: source, UpdatedAt: updated, DeletedAt: tombstone(b.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("body measurement %s: %w", id, err)
	}
	if n == 0 {
		owner, err := q.BodyMeasurementOwner(ctx, id)
		return checkOwner(owner, err, userID, "body measurement", id)
	}
	return nil
}

func applyWorkout(ctx context.Context, q *dbq.Queries, userID uuid.UUID, w *milov1.Workout) error {
	id, err := parseID(w.GetId(), "workout")
	if err != nil {
		return err
	}
	updated, err := requiredTime(w.GetUpdatedAt(), "workout "+w.GetId())
	if err != nil {
		return err
	}
	started, err := requiredTime(w.GetStartedAt(), "workout "+w.GetId()+" started_at")
	if err != nil {
		return err
	}
	templateID, err := parseOptID(w.GetTemplateId(), "workout template")
	if err != nil {
		return err
	}
	healthID, err := parseOptID(w.GetHealthUuid(), "workout health")
	if err != nil {
		return err
	}
	n, err := q.UpsertWorkout(ctx, dbq.UpsertWorkoutParams{
		ID: id, UserID: userID, Name: w.GetName(), Notes: w.GetNotes(), StartedAt: started,
		EndedAt: optTime(w.GetEndedAt()), TemplateID: templateID, HealthUuid: healthID,
		UpdatedAt: updated, DeletedAt: tombstone(w.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("workout %s: %w", id, err)
	}
	if n == 0 {
		owner, err := q.WorkoutOwner(ctx, id)
		return checkOwner(owner, err, userID, "workout", id)
	}
	return nil
}

func applyWorkoutExercise(ctx context.Context, q *dbq.Queries, userID uuid.UUID, we *milov1.WorkoutExercise) error {
	id, err := parseID(we.GetId(), "workout exercise")
	if err != nil {
		return err
	}
	updated, err := requiredTime(we.GetUpdatedAt(), "workout exercise "+we.GetId())
	if err != nil {
		return err
	}
	workoutID, err := parseID(we.GetWorkoutId(), "workout exercise's workout")
	if err != nil {
		return err
	}
	exerciseID, err := parseID(we.GetExerciseId(), "workout exercise's exercise")
	if err != nil {
		return err
	}
	n, err := q.UpsertWorkoutExercise(ctx, dbq.UpsertWorkoutExerciseParams{
		ID: id, UserID: userID, WorkoutID: workoutID, ExerciseID: exerciseID, Position: we.GetPosition(),
		GroupLabel: we.GetGroupLabel(), Notes: we.GetNotes(), UpdatedAt: updated,
		DeletedAt: tombstone(we.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("workout exercise %s: %w", id, err)
	}
	if n == 0 {
		owner, err := q.WorkoutExerciseOwner(ctx, id)
		return checkOwner(owner, err, userID, "workout exercise", id)
	}
	return nil
}

func applySet(ctx context.Context, q *dbq.Queries, userID uuid.UUID, s *milov1.Set) error {
	id, err := parseID(s.GetId(), "set")
	if err != nil {
		return err
	}
	updated, err := requiredTime(s.GetUpdatedAt(), "set "+s.GetId())
	if err != nil {
		return err
	}
	weID, err := parseID(s.GetWorkoutExerciseId(), "set's workout exercise")
	if err != nil {
		return err
	}
	if err := checkWeight(s.GetWeight()); err != nil {
		return err
	}
	n, err := q.UpsertSet(ctx, dbq.UpsertSetParams{
		ID: id, UserID: userID, WorkoutExerciseID: weID, Position: s.GetPosition(), Kind: setKind(s.GetKind()),
		Reps: s.Reps, Weight: s.GetWeight(), WeightUnit: unit(s.GetWeightUnit()), PerSide: s.GetPerSide(),
		DurationS: s.DurationS, DistanceM: s.DistanceM, Rpe: s.Rpe, Done: s.GetDone(),
		UpdatedAt: updated, DeletedAt: tombstone(s.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("set %s: %w", id, err)
	}
	if n == 0 {
		owner, err := q.SetOwner(ctx, id)
		return checkOwner(owner, err, userID, "set", id)
	}
	return nil
}

func applyTemplate(ctx context.Context, q *dbq.Queries, userID uuid.UUID, t *milov1.Template) error {
	id, err := parseID(t.GetId(), "template")
	if err != nil {
		return err
	}
	updated, err := requiredTime(t.GetUpdatedAt(), "template "+t.GetId())
	if err != nil {
		return err
	}
	if t.GetName() == "" && !t.GetDeleted() {
		return invalid("template %s needs a name", id)
	}
	items, err := templateItemsJSON(t.GetItems())
	if err != nil {
		return err
	}
	n, err := q.UpsertTemplate(ctx, dbq.UpsertTemplateParams{
		ID: id, UserID: userID, Name: t.GetName(), Notes: t.GetNotes(), Items: items,
		UpdatedAt: updated, DeletedAt: tombstone(t.GetDeleted(), updated),
	})
	if err != nil {
		return fmt.Errorf("template %s: %w", id, err)
	}
	if n == 0 {
		owner, err := q.TemplateOwner(ctx, id)
		return checkOwner(owner, err, userID, "template", id)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
