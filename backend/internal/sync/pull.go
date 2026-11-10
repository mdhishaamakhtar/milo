package sync

import (
	"cmp"
	"context"
	"slices"

	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// Pull returns up to limit of the user's rows (and built-in exercises) changed
// after cursor, oldest first, with the new cursor and whether more remain.
// Each table is read up to limit+1 rows; the smallest limit of those across
// all tables are exactly the next page.
func Pull(ctx context.Context, q *query.Queries, userID uuid.UUID, cursor int64, limit int) ([]*milo.Change, int64, bool, error) {
	type item struct {
		seq int64
		row any
	}
	var items []item
	lim := int32(limit + 1)

	exercises, err := q.PullExercises(ctx, query.PullExercisesParams{UserID: &userID, Cursor: cursor, Lim: lim})
	for _, r := range exercises {
		items = append(items, item{r.ServerSeq, rows.Exercise(r)})
	}
	prefs, err2 := q.PullExercisePrefs(ctx, query.PullExercisePrefsParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range prefs {
		items = append(items, item{r.ServerSeq, rows.ExercisePref(r)})
	}
	settings, err3 := q.PullSettings(ctx, query.PullSettingsParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range settings {
		items = append(items, item{r.ServerSeq, rows.Settings(r)})
	}
	body, err4 := q.PullBodyMeasurements(ctx, query.PullBodyMeasurementsParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range body {
		items = append(items, item{r.ServerSeq, rows.BodyMeasurement(r)})
	}
	workouts, err5 := q.PullWorkouts(ctx, query.PullWorkoutsParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range workouts {
		items = append(items, item{r.ServerSeq, rows.Workout(r)})
	}
	wes, err6 := q.PullWorkoutExercises(ctx, query.PullWorkoutExercisesParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range wes {
		items = append(items, item{r.ServerSeq, rows.WorkoutExercise(r)})
	}
	sets, err7 := q.PullSets(ctx, query.PullSetsParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range sets {
		items = append(items, item{r.ServerSeq, rows.Set(r)})
	}
	templates, err8 := q.PullTemplates(ctx, query.PullTemplatesParams{UserID: userID, Cursor: cursor, Lim: lim})
	for _, r := range templates {
		items = append(items, item{r.ServerSeq, rows.Template(r)})
	}
	if err := cmp.Or(err, err2, err3, err4, err5, err6, err7, err8); err != nil {
		return nil, 0, false, err
	}

	slices.SortFunc(items, func(a, b item) int { return cmp.Compare(a.seq, b.seq) })
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	changes := make([]*milo.Change, len(items))
	for i, it := range items {
		changes[i] = rows.Change(it.row)
	}
	if len(items) > 0 {
		cursor = items[len(items)-1].seq
	}
	return changes, cursor, more, nil
}
