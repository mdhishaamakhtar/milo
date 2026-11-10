package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// Exercises in the user's library: propose a new one, or change one of theirs.

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
	WorkoutID        string     `json:"workout_id,omitempty" jsonschema_description:"Workout to add it to. Defaults to the workout on screen, else the one running."`
	Sets             []setInput `json:"sets,omitempty" jsonschema_description:"The sets they did, if they said."`
	AddToWorkout     bool       `json:"add_to_workout,omitempty" jsonschema_description:"Put it in the workout with an empty set to fill in, when they want to do it now but haven't said any sets."`
}

// proposeNewExercise shows a card for a movement the library doesn't have, with
// the sets the user did. It is Approximate until they confirm it.
func (t *turn) proposeNewExercise(ctx context.Context, in newExerciseInput) (any, error) {
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		return nil, badInput("name is required")
	case rows.Parse[milo.Measurement](in.Measurement) == milo.Measurement_MEASUREMENT_UNSPECIFIED:
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
			return nil, badInput("an exercise called %q already exists (exercise_id %s); use that one", e.Name, e.ID)
		}
	}
	for _, s := range in.Sets {
		in.Unilateral = in.Unilateral || s.PerSide // sets counted each side mean one side at a time
	}

	ex := query.Exercise{
		ID: newID(), UserID: &t.userID, Name: in.Name, Aliases: in.Aliases, PrimaryMuscles: in.PrimaryMuscles,
		SecondaryMuscles: in.SecondaryMuscles, MovementPattern: in.MovementPattern, Equipment: in.Equipment,
		Measurement: in.Measurement, Unilateral: in.Unilateral, Met: &in.MET, Description: in.Description,
		Approximate: true, UpdatedAt: t.now,
	}
	t.proposed[ex.ID] = ex
	card := &milo.Card{
		Kind: milo.CardKind_CARD_KIND_NEW_EXERCISE, Title: in.Name, Subtitle: "New exercise, saved as yours", ExerciseId: ex.ID.String(),
		AppliedTitle: in.Name + " added", Changes: t.pending(ex.ID),
		Lines: []*milo.CardLine{
			{Label: "Works", After: in.WorksSummary},
			{Label: "Equipment", After: in.EquipmentSummary},
			{Label: "Counted", After: countedText(in.Measurement, in.Unilateral)},
			{Label: "Movement", After: in.MovementSummary},
		},
	}
	if len(in.Sets) > 0 || in.AddToWorkout {
		w, err := t.resolveWorkout(ctx, in.WorkoutID)
		if err != nil {
			return nil, err
		}
		card.WorkoutId = w.id.String()
		if len(in.Sets) == 0 {
			added, _, err := t.addExercise(ctx, w, ex, "")
			if err != nil {
				return nil, err
			}
			card.Changes = append(card.Changes, added...)
		} else {
			sl, added, err := t.newSlot(ctx, w, ex.ID, "")
			if err != nil {
				return nil, err
			}
			logged, _ := sl.log(in.Sets, t.now)
			card.Changes = append(append(card.Changes, added...), logged...)
			for i, s := range in.Sets {
				card.Lines = append(card.Lines, &milo.CardLine{Label: fmt.Sprintf("Set %d", i+1), After: s.text(in.Measurement)})
			}
			if in.Sets[0].Weight != "" { // remember the unit they lift this in
				card.Changes = append(card.Changes, rows.Change(&milo.ExercisePref{ExerciseId: ex.ID.String(), Unit: rows.Parse[milo.WeightUnit](in.Sets[0].Unit), UpdatedAt: ts(t.now)}))
			}
			card.Note = "Good enough to log now. You can tighten the details later."
		}
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "exercise_id": ex.ID.String(), "shown": "The user sees the card and adds or edits it. Nothing is saved yet, but you can already use this exercise_id in other tools in this reply."}, nil
}

// pending is the row for an exercise proposed earlier in this turn, so a card
// that uses the exercise brings it along; nil for one that already exists.
func (t *turn) pending(id uuid.UUID) []*milo.Change {
	ex, ok := t.proposed[id]
	if !ok {
		return nil
	}
	return []*milo.Change{rows.Change(rows.Exercise(ex))}
}

type exerciseChangeInput struct {
	ExerciseID     string   `json:"exercise_id"`
	Name           string   `json:"name,omitempty"`
	Measurement    string   `json:"measurement,omitempty" jsonschema:"enum=reps_load,enum=bodyweight,enum=assisted,enum=timed,enum=distance"`
	Unilateral     *bool    `json:"unilateral,omitempty"`
	Equipment      string   `json:"equipment,omitempty"`
	PrimaryMuscles []string `json:"primary_muscles,omitempty"`
	MET            *float64 `json:"met,omitempty"`
	Confirm        bool     `json:"confirm,omitempty" jsonschema_description:"The user confirmed the details, so it is no longer Approximate."`
	MergeInto      string   `json:"merge_into,omitempty" jsonschema_description:"It turned out to be this other exercise: move every workout and template that uses it over, then remove it."`
}

// proposeExerciseChange proposes a change to one of the user's own exercises.
func (t *turn) proposeExerciseChange(ctx context.Context, in exerciseChangeInput) (any, error) {
	ex, err := t.exercise(ctx, in.ExerciseID)
	if err != nil {
		return nil, err
	}
	if ex.UserID == nil {
		return nil, badInput("%s is built in and can't be changed. Its unit and rest are set with set_exercise_preferences; for a different movement, propose a new exercise", ex.Name)
	}
	if in.MergeInto != "" {
		return t.mergeExercise(ctx, ex, in.MergeInto)
	}
	row := rows.Exercise(ex)
	row.UpdatedAt = ts(t.now)
	var lines []*milo.CardLine
	line := func(label, before, after string) {
		lines = append(lines, &milo.CardLine{Label: label, Before: before, After: after})
	}
	if n := strings.TrimSpace(in.Name); n != "" && n != ex.Name {
		line("Name", ex.Name, n)
		row.Name = n
	}
	if m := rows.Parse[milo.Measurement](in.Measurement); m != milo.Measurement_MEASUREMENT_UNSPECIFIED && in.Measurement != ex.Measurement {
		line("Counted", countedText(ex.Measurement, ex.Unilateral), countedText(in.Measurement, ex.Unilateral))
		row.Measurement = m
	}
	if in.Unilateral != nil && *in.Unilateral != ex.Unilateral {
		line("One side at a time", yesNo(ex.Unilateral), yesNo(*in.Unilateral))
		row.Unilateral = *in.Unilateral
	}
	if in.Equipment != "" && in.Equipment != ex.Equipment {
		line("Equipment", orNone(ex.Equipment), in.Equipment)
		row.Equipment = in.Equipment
	}
	if len(in.PrimaryMuscles) > 0 && !slices.Equal(in.PrimaryMuscles, ex.PrimaryMuscles) {
		line("Works", orNone(strings.Join(ex.PrimaryMuscles, ", ")), strings.Join(in.PrimaryMuscles, ", "))
		row.PrimaryMuscles = in.PrimaryMuscles
	}
	if in.MET != nil {
		if *in.MET < 1.5 || *in.MET > 15 {
			return nil, badInput("met %.1f is out of range (strength work is usually 3 to 6)", *in.MET)
		}
		row.Met = in.MET
	}
	if in.Confirm && ex.Approximate {
		line("Status", "Approximate", "Confirmed")
		row.Approximate = false
	}
	if len(lines) == 0 && in.MET == nil {
		return nil, badInput("these edits change nothing about %s", ex.Name)
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_CHANGE, Title: ex.Name, Subtitle: "Your exercise", Lines: lines, ExerciseId: ex.ID.String(),
		AppliedTitle: row.Name + " updated", Changes: []*milo.Change{rows.Change(row)},
		Base: []*milo.RowVersion{version("exercise", ex.ID, ex.UpdatedAt)},
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": proposedNote}, nil
}

// mergeLimit keeps a merge card to a size the phone applies in one go.
const mergeLimit = 500

// mergeExercise folds one of the user's exercises into another: its history
// and templates move over and it leaves the library.
func (t *turn) mergeExercise(ctx context.Context, from query.Exercise, intoID string) (any, error) {
	into, err := t.exercise(ctx, intoID)
	switch {
	case err != nil:
		return nil, err
	case into.ID == from.ID:
		return nil, badInput("merge_into is the same exercise")
	case into.Measurement != from.Measurement:
		return nil, badInput("%s and %s are counted differently, so their sets can't be merged", from.Name, into.Name)
	}
	used, err := t.s.q.WorkoutExercisesUsing(ctx, query.WorkoutExercisesUsingParams{UserID: t.userID, ExerciseID: from.ID})
	if err != nil {
		return nil, err
	}
	if len(used) > mergeLimit {
		return nil, badInput("%s is in %d workouts, too many to merge in one go", from.Name, len(used))
	}
	card := &milo.Card{
		Kind: milo.CardKind_CARD_KIND_CHANGE, Title: "Merge into " + into.Name, Subtitle: from.Name, ExerciseId: into.ID.String(),
		ApplyLabel: "Merge", AppliedTitle: "Merged " + from.Name + " into " + into.Name,
		Base: []*milo.RowVersion{version("exercise", from.ID, from.UpdatedAt)},
	}
	for _, we := range used {
		row := rows.WorkoutExercise(we)
		row.ExerciseId, row.UpdatedAt = into.ID.String(), ts(t.now)
		card.Changes = append(card.Changes, rows.Change(row))
	}
	templates, err := t.s.q.ListTemplatesFor(ctx, t.userID)
	if err != nil {
		return nil, err
	}
	moved := 0
	for _, tpl := range templates {
		row, hit := rows.Template(tpl), false
		for _, it := range row.Items {
			if it.GetExerciseId() == from.ID.String() {
				it.ExerciseId, hit = into.ID.String(), true
			}
		}
		if hit {
			moved++
			row.UpdatedAt = ts(t.now)
			card.Changes = append(card.Changes, rows.Change(row))
		}
	}
	gone := rows.Exercise(from)
	gone.Deleted, gone.UpdatedAt = true, ts(t.now)
	card.Changes = append(card.Changes, rows.Change(gone))
	card.Lines = []*milo.CardLine{
		{Label: "Workouts", After: count(len(used), "session moves", "sessions move") + " to " + shortName(into.Name)},
		{Label: "Templates", After: count(moved, "template", "templates") + " updated"},
		{Label: from.Name, Before: "Yours", After: "Removed"},
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": proposedNote}, nil
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
