package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

type templateInput struct {
	TemplateID string              `json:"template_id,omitempty" jsonschema_description:"To change a template they already have. Leave out for a new one."`
	Name       string              `json:"name,omitempty" jsonschema_description:"Required for a new template; for an existing one, only to rename it."`
	Notes      *string             `json:"notes,omitempty"`
	Items      []templateItemInput `json:"items,omitempty" jsonschema_description:"The whole list in order. Required for a new template; for an existing one, leave out to keep its list."`
}

type templateItemInput struct {
	ExerciseID string `json:"exercise_id"`
	GroupLabel string `json:"group_label,omitempty" jsonschema_description:"Supersets: neighbouring exercises with the same label are done back to back, shown as 4a and 4b."`
	Note       string `json:"note,omitempty" jsonschema_description:"A word or two for the card, e.g. \"fresh\", \"volume\"."`
}

// proposeTemplate drafts a template, or a change to one: exercises in order,
// supersets grouped, no numbers. The user saves it.
func (t *turn) proposeTemplate(ctx context.Context, in templateInput) (any, error) {
	tpl := &milo.Template{Id: newID().String()}
	card := &milo.Card{Kind: milo.CardKind_CARD_KIND_TEMPLATE_DRAFT}
	before := map[string]bool{} // exercises already in the template being changed
	if in.TemplateID != "" {
		stored, err := t.userTemplate(ctx, in.TemplateID)
		if err != nil {
			return nil, err
		}
		tpl = rows.Template(stored)
		for _, it := range tpl.Items {
			before[it.GetExerciseId()] = true
		}
		card.Base = []*milo.RowVersion{version("template", stored.ID, stored.UpdatedAt)}
		card.ApplyLabel = "Save changes"
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		tpl.Name = name
	}
	if in.Notes != nil {
		tpl.Notes = strings.TrimSpace(*in.Notes)
	}
	notes := map[string]string{}
	if len(in.Items) > 0 {
		tpl.Items = make([]*milo.TemplateItem, len(in.Items))
		for i, it := range in.Items {
			tpl.Items[i] = &milo.TemplateItem{ExerciseId: it.ExerciseID, GroupLabel: it.GroupLabel}
			notes[it.ExerciseID] = it.Note
		}
	}
	if tpl.Name == "" || len(tpl.Items) == 0 {
		return nil, badInput("a template needs a name and at least one exercise")
	}

	groups := make([]string, len(tpl.Items))
	for i, it := range tpl.Items {
		groups[i] = it.GetGroupLabel()
	}
	labels := supersetLabels(groups)
	for i, it := range tpl.Items {
		ex, err := t.exercise(ctx, it.GetExerciseId())
		if err != nil {
			return nil, err
		}
		card.Changes = append(card.Changes, t.pending(ex.ID)...)
		note := notes[it.GetExerciseId()]
		if len(before) > 0 && !before[it.GetExerciseId()] && note == "" {
			note = "new"
		}
		card.Lines = append(card.Lines, &milo.CardLine{Label: labels[i], After: ex.Name, Before: note})
	}
	tpl.UpdatedAt = ts(t.now)
	card.Changes = append(card.Changes, rows.Change(tpl))
	card.Title = tpl.Name
	card.Subtitle = "Draft template, " + count(len(tpl.Items), "exercise", "exercises")
	card.AppliedTitle = "Saved the " + tpl.Name + " template"
	if in.TemplateID != "" {
		card.Subtitle = "Your template, " + count(len(tpl.Items), "exercise", "exercises") + " after this change"
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "template_id": tpl.Id, "shown": "The user sees the draft and saves or edits it. Nothing is saved yet."}, nil
}

type deleteInput struct {
	What string   `json:"what" jsonschema:"enum=workout,enum=template,enum=exercise,enum=body_reading"`
	ID   string   `json:"id,omitempty" jsonschema_description:"The workout_id, template_id, exercise_id or reading_id."`
	IDs  []string `json:"ids,omitempty" jsonschema_description:"Several workout_ids, to delete them with one card."`
}

// proposeDelete shows what would go and a red button. Deleting always takes a tap.
func (t *turn) proposeDelete(ctx context.Context, in deleteInput) (any, error) {
	if len(in.IDs) == 1 {
		in.ID, in.IDs = in.IDs[0], nil
	}
	if len(in.IDs) > 1 {
		if in.What != "workout" {
			return nil, badInput("ids is for workouts; delete a %s one at a time with id", in.What)
		}
		card, err := t.deleteWorkouts(ctx, in.IDs)
		if err != nil {
			return nil, err
		}
		cardID, err := t.emitCard(ctx, card)
		if err != nil {
			return nil, err
		}
		return map[string]any{"card_id": cardID, "workouts": len(in.IDs), "shown": proposedNote}, nil
	}
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

// deleteWorkouts is one card that deletes several workouts, each a line on it.
func (t *turn) deleteWorkouts(ctx context.Context, ids []string) (*milo.Card, error) {
	n := count(len(ids), "workout", "workouts")
	all := &milo.Card{Kind: milo.CardKind_CARD_KIND_CHANGE, Destructive: true, Title: "Delete " + n, ApplyLabel: "Delete " + n, AppliedTitle: "Deleted " + n}
	sets := 0
	for i, raw := range ids {
		id, err := parseUUID(raw, "ids")
		if err != nil {
			return nil, err
		}
		one, err := t.deleteWorkout(ctx, id)
		if err != nil {
			return nil, err
		}
		all.Changes, all.Base = append(all.Changes, one.Changes...), append(all.Base, one.Base...)
		for _, c := range one.Changes {
			if c.GetSet() != nil {
				sets++
			}
		}
		switch {
		case i < bulkLines:
			all.Lines = append(all.Lines, &milo.CardLine{Label: one.Title, After: strings.SplitN(one.Subtitle, ",", 2)[0]})
		case i == bulkLines:
			all.Lines = append(all.Lines, &milo.CardLine{Label: fmt.Sprintf("and %d more", len(ids)-bulkLines)})
		}
	}
	all.Subtitle = count(sets, "set", "sets") + " in all"
	return all, nil
}

// bulkLines is how many workouts a bulk delete card names before it says "and N more".
const bulkLines = 8

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
