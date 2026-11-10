package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type newExerciseInput struct {
	Name             string     `json:"name" jsonschema_description:"A clear name, e.g. \"Landmine Rotation\"."`
	Aliases          []string   `json:"aliases,omitempty" jsonschema_description:"Other names, including what the trainer called it."`
	PrimaryMuscles   []string   `json:"primary_muscles"`
	SecondaryMuscles []string   `json:"secondary_muscles,omitempty"`
	MovementPattern  string     `json:"movement_pattern,omitempty"`
	Equipment        string     `json:"equipment,omitempty"`
	Measurement      string     `json:"measurement" jsonschema:"enum=reps_load,enum=bodyweight,enum=assisted,enum=timed,enum=distance"`
	Unilateral       bool       `json:"unilateral" jsonschema_description:"Done one side at a time and counted \"N each side\" (single-arm work, lunges, rotations). True whenever its sets are per_side."`
	MET              float64    `json:"met" jsonschema_description:"Metabolic equivalent for calories, 2 to 12."`
	Description      string     `json:"description,omitempty"`
	WorksSummary     string     `json:"works_summary" jsonschema_description:"For the card, e.g. \"Obliques and core, some shoulders\"."`
	EquipmentSummary string     `json:"equipment_summary" jsonschema_description:"For the card, e.g. \"Barbell in a landmine\"."`
	MovementSummary  string     `json:"movement_summary" jsonschema_description:"For the card, e.g. \"Rotation, standing\"."`
	WorkoutID        string     `json:"workout_id,omitempty" jsonschema_description:"Workout to add the sets to. Defaults to the open workout."`
	Sets             []setInput `json:"sets,omitempty"`
}

// proposeNewExercise shows a card for a movement the library doesn't have, with
// the sets the user did. It is Approximate until they confirm it.
func (t *turn) proposeNewExercise(ctx context.Context, in newExerciseInput) (any, error) {
	in.Name = strings.TrimSpace(in.Name)
	measurement := rows.Parse[milo.Measurement](in.Measurement)
	switch {
	case in.Name == "":
		return nil, badInput("name is required")
	case measurement == milo.Measurement_MEASUREMENT_UNSPECIFIED:
		return nil, badInput("measurement must be one of reps_load, bodyweight, assisted, timed, distance")
	case in.MET < 1.5 || in.MET > 15:
		return nil, badInput("met %.1f is out of range (strength work is usually 3 to 6)", in.MET)
	case len(in.PrimaryMuscles) == 0:
		return nil, badInput("primary_muscles needs at least one muscle")
	}
	if err := checkSets(in.Sets); err != nil {
		return nil, err
	}
	same, err := t.s.q.SearchExercises(ctx, query.SearchExercisesParams{UserID: t.userID, Query: in.Name, Lim: 3})
	if err != nil {
		return nil, err
	}
	for _, e := range same {
		if strings.EqualFold(e.Name, in.Name) {
			return nil, badInput("an exercise called %q already exists (exercise_id %s); log to it with log_sets", e.Name, e.ID)
		}
	}
	for _, s := range in.Sets {
		in.Unilateral = in.Unilateral || s.PerSide // sets counted each side mean one side at a time
	}

	exerciseID, met := newID(), in.MET
	changes := []*milo.Change{rows.Change(&milo.Exercise{
		Id: exerciseID.String(), Name: in.Name, Aliases: in.Aliases, PrimaryMuscles: in.PrimaryMuscles,
		SecondaryMuscles: in.SecondaryMuscles, MovementPattern: in.MovementPattern, Equipment: in.Equipment,
		Measurement: measurement, Unilateral: in.Unilateral, Met: &met, Description: in.Description,
		Approximate: true, UpdatedAt: ts(t.now),
	})}
	card := &milo.Card{
		Kind: milo.CardKind_CARD_KIND_NEW_EXERCISE, Title: in.Name, Subtitle: "New exercise, saved as yours", ExerciseId: exerciseID.String(),
		Lines: []*milo.CardLine{
			{Label: "Works", After: in.WorksSummary},
			{Label: "Equipment", After: in.EquipmentSummary},
			{Label: "Counted", After: countedText(in.Measurement, in.Unilateral)},
			{Label: "Movement", After: in.MovementSummary},
		},
	}
	if len(in.Sets) > 0 {
		w, err := t.resolveWorkout(ctx, in.WorkoutID)
		if err != nil {
			return nil, err
		}
		weID, pos, weChanges, err := t.workoutExercise(ctx, w, exerciseID, false)
		if err != nil {
			return nil, err
		}
		changes = append(changes, weChanges...)
		for i, s := range in.Sets {
			changes = append(changes, rows.Change(s.row(weID, pos+int32(i), t.now)))
			card.Lines = append(card.Lines, &milo.CardLine{Label: fmt.Sprintf("Set %d", i+1), After: s.text(in.Measurement)})
		}
		t.nextSet[weID] = pos + int32(len(in.Sets))
		if in.Sets[0].Weight != "" { // remember the unit they lift this in
			changes = append(changes, rows.Change(&milo.ExercisePref{ExerciseId: exerciseID.String(), Unit: rows.Parse[milo.WeightUnit](in.Sets[0].Unit), UpdatedAt: ts(t.now)}))
		}
		card.WorkoutId = w.id.String()
		card.Note = "Good enough to log now. You can tighten the details later from History."
	}
	card.Changes = changes
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "exercise_id": exerciseID.String(), "shown": "The user sees the card and adds or edits it. Nothing is saved yet."}, nil
}

func countedText(measurement string, unilateral bool) string {
	switch measurement {
	case "bodyweight":
		return "Reps at body weight"
	case "assisted":
		return "Reps with assistance"
	case "timed":
		return "Time"
	case "distance":
		return "Distance"
	}
	if unilateral {
		return "Reps each side"
	}
	return "Reps at a weight"
}
