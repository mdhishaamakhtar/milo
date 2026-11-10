package syncsvc

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

type pulled struct {
	seq    int64
	change *milov1.Change
}

// Pull returns up to limit of the user's rows (and built-in exercises) with
// server_seq after cursor, oldest first, plus the new cursor and whether more
// remain. Each table is read up to limit+1 rows; the smallest limit across all
// tables are exactly the next page.
func Pull(ctx context.Context, q *dbq.Queries, userID uuid.UUID, cursor int64, limit int) ([]*milov1.Change, int64, bool, error) {
	lim := int32(limit + 1)
	var out []pulled
	add := func(seq int64, c *milov1.Change) { out = append(out, pulled{seq, c}) }

	exercises, err := q.PullExercises(ctx, dbq.PullExercisesParams{UserID: &userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull exercises: %w", err)
	}
	for _, r := range exercises {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_Exercise{Exercise: exerciseProto(r)}})
	}

	prefs, err := q.PullExercisePrefs(ctx, dbq.PullExercisePrefsParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull exercise prefs: %w", err)
	}
	for _, r := range prefs {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_ExercisePref{ExercisePref: exercisePrefProto(r)}})
	}

	settings, err := q.PullSettings(ctx, dbq.PullSettingsParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull settings: %w", err)
	}
	for _, r := range settings {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_Settings{Settings: settingsProto(r)}})
	}

	body, err := q.PullBodyMeasurements(ctx, dbq.PullBodyMeasurementsParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull body measurements: %w", err)
	}
	for _, r := range body {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_BodyMeasurement{BodyMeasurement: bodyMeasurementProto(r)}})
	}

	workouts, err := q.PullWorkouts(ctx, dbq.PullWorkoutsParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull workouts: %w", err)
	}
	for _, r := range workouts {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_Workout{Workout: workoutProto(r)}})
	}

	wes, err := q.PullWorkoutExercises(ctx, dbq.PullWorkoutExercisesParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull workout exercises: %w", err)
	}
	for _, r := range wes {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_WorkoutExercise{WorkoutExercise: workoutExerciseProto(r)}})
	}

	sets, err := q.PullSets(ctx, dbq.PullSetsParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull sets: %w", err)
	}
	for _, r := range sets {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_Set{Set: setProto(r)}})
	}

	templates, err := q.PullTemplates(ctx, dbq.PullTemplatesParams{UserID: userID, Cursor: cursor, Lim: lim})
	if err != nil {
		return nil, 0, false, fmt.Errorf("pull templates: %w", err)
	}
	for _, r := range templates {
		add(r.ServerSeq, &milov1.Change{Row: &milov1.Change_Template{Template: templateProto(r)}})
	}

	slices.SortFunc(out, func(a, b pulled) int {
		switch {
		case a.seq < b.seq:
			return -1
		case a.seq > b.seq:
			return 1
		}
		return 0
	})
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	changes := make([]*milov1.Change, len(out))
	for i, p := range out {
		changes[i] = p.change
	}
	next := cursor
	if len(out) > 0 {
		next = out[len(out)-1].seq
	}
	return changes, next, more, nil
}
