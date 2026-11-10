package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type deleteInput struct {
	What string `json:"what" jsonschema:"enum=workout,enum=template,enum=exercise,enum=body_reading"`
	ID   string `json:"id" jsonschema_description:"The workout_id, template_id, exercise_id or reading_id."`
}

// proposeDelete shows what would go and a red button. Deleting always takes a tap.
func (t *turn) proposeDelete(ctx context.Context, in deleteInput) (any, error) {
	id, err := parseUUID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	var card *milo.Card
	switch in.What {
	case "workout":
		card, err = t.deleteWorkout(ctx, id)
	case "template":
		card, err = t.deleteTemplate(ctx, in.ID)
	case "exercise":
		card, err = t.deleteExercise(ctx, id)
	case "body_reading":
		card, err = t.deleteReading(ctx, id)
	default:
		err = badInput("what must be workout, template, exercise or body_reading, not %q", in.What)
	}
	if err != nil {
		return nil, err
	}
	card.Kind, card.Destructive = milo.CardKind_CARD_KIND_CHANGE, true
	cardID, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": cardID, "shown": proposedNote}, nil
}

func (t *turn) deleteWorkout(ctx context.Context, id uuid.UUID) (*milo.Card, error) {
	w, err := t.userWorkout(ctx, id)
	if err != nil {
		return nil, err
	}
	details, err := t.loadDetails(ctx, []query.Workout{w})
	if err != nil {
		return nil, err
	}
	d := details[0]
	gone := rows.Workout(w)
	gone.Deleted, gone.UpdatedAt = true, ts(t.now)
	card := &milo.Card{
		Title: nameOr(w.Name, "Workout"), WorkoutId: id.String(),
		Subtitle:     fmt.Sprintf("%s, %s, %s", t.day(w.StartedAt), count(len(d.exercises), "exercise", "exercises"), count(d.doneSets(), "set", "sets")),
		ApplyLabel:   "Delete workout",
		AppliedTitle: "Deleted " + nameOr(w.Name, "workout"),
		Changes:      []*milo.Change{rows.Change(gone)},
		Base:         []*milo.RowVersion{version("workout", id, w.UpdatedAt)},
	}
	if w.EndedAt == nil {
		card.ApplyLabel, card.AppliedTitle = "Discard workout", "Discarded "+nameOr(w.Name, "workout")
		card.Subtitle = "Running since " + t.clock(w.StartedAt) + ", " + count(d.doneSets(), "set", "sets") + " ticked"
	}
	for _, e := range d.exercises {
		card.Lines = append(card.Lines, &milo.CardLine{Label: e.row.ExerciseName, After: exerciseSummary(e.row.Measurement, e.sets)})
		we := &milo.WorkoutExercise{
			Id: e.row.ID.String(), WorkoutId: id.String(), ExerciseId: e.row.ExerciseID.String(), Position: e.row.Position,
			GroupLabel: e.row.GroupLabel, Notes: e.row.Notes, Deleted: true, UpdatedAt: ts(t.now),
		}
		card.Changes = append(card.Changes, rows.Change(we))
		for _, s := range e.sets {
			row := rows.Set(s)
			row.Deleted, row.UpdatedAt = true, ts(t.now)
			card.Changes = append(card.Changes, rows.Change(row))
		}
	}
	return card, nil
}

func (t *turn) deleteTemplate(ctx context.Context, id string) (*milo.Card, error) {
	tpl, err := t.userTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	gone := rows.Template(tpl)
	gone.Deleted, gone.UpdatedAt = true, ts(t.now)
	return &milo.Card{
		Title: tpl.Name, Subtitle: "Template, " + count(len(gone.Items), "exercise", "exercises"),
		Note:       "Workouts you did from it stay in your history.",
		ApplyLabel: "Delete template", AppliedTitle: "Deleted the " + tpl.Name + " template",
		Changes: []*milo.Change{rows.Change(gone)},
		Base:    []*milo.RowVersion{version("template", tpl.ID, tpl.UpdatedAt)},
	}, nil
}

func (t *turn) deleteExercise(ctx context.Context, id uuid.UUID) (*milo.Card, error) {
	ex, err := t.visibleExercise(ctx, id)
	if err != nil {
		return nil, err
	}
	if ex.UserID == nil {
		return nil, badInput("%s is built in and can't be deleted; only exercises the user made can", ex.Name)
	}
	used, err := t.s.q.WorkoutExercisesUsing(ctx, query.WorkoutExercisesUsingParams{UserID: t.userID, ExerciseID: id})
	if err != nil {
		return nil, err
	}
	gone := rows.Exercise(ex)
	gone.Deleted, gone.UpdatedAt = true, ts(t.now)
	card := &milo.Card{
		Title: ex.Name, Subtitle: "Your exercise, never logged", ExerciseId: id.String(),
		ApplyLabel: "Delete exercise", AppliedTitle: "Deleted " + ex.Name,
		Changes: []*milo.Change{rows.Change(gone)},
		Base:    []*milo.RowVersion{version("exercise", id, ex.UpdatedAt)},
	}
	if n := len(used); n > 0 {
		card.Subtitle = "Your exercise, logged in " + count(n, "workout", "workouts")
		card.Note = "Those workouts keep it; it just leaves your library."
	}
	return card, nil
}

func (t *turn) deleteReading(ctx context.Context, id uuid.UUID) (*milo.Card, error) {
	m, err := t.s.q.GetUserBodyMeasurement(ctx, query.GetUserBodyMeasurementParams{ID: id, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badInput("no body reading %s; get_profile lists them", id)
	}
	if err != nil {
		return nil, err
	}
	gone := rows.BodyMeasurement(m)
	gone.Deleted, gone.UpdatedAt = true, ts(t.now)
	return &milo.Card{
		Title: readingText(m.WeightKg, m.BodyFatPct, t.displayUnit), Subtitle: "Body reading, " + t.day(m.MeasuredAt),
		ApplyLabel: "Delete reading", AppliedTitle: "Deleted the reading from " + t.day(m.MeasuredAt),
		Changes: []*milo.Change{rows.Change(gone)},
		Base:    []*milo.RowVersion{version("body_measurement", id, m.UpdatedAt)},
	}, nil
}
