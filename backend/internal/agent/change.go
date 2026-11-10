package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type changeInput struct {
	Title    string      `json:"title" jsonschema_description:"Card title, e.g. the exercise or workout name."`
	Subtitle string      `json:"subtitle,omitempty" jsonschema_description:"e.g. \"Shoulders, Tue 6 Oct\"."`
	Edits    []editInput `json:"edits"`
}

type editInput struct {
	Op                string   `json:"op" jsonschema:"enum=update_set,enum=delete_set,enum=add_set,enum=rename_workout,enum=update_exercise"`
	SetID             string   `json:"set_id,omitempty"`
	WorkoutExerciseID string   `json:"workout_exercise_id,omitempty" jsonschema_description:"For add_set."`
	WorkoutID         string   `json:"workout_id,omitempty" jsonschema_description:"For rename_workout."`
	ExerciseID        string   `json:"exercise_id,omitempty" jsonschema_description:"For update_exercise."`
	Reps              *int32   `json:"reps,omitempty"`
	Weight            *string  `json:"weight,omitempty"`
	Unit              string   `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb"`
	PerSide           *bool    `json:"per_side,omitempty"`
	DurationS         *int32   `json:"duration_s,omitempty"`
	DistanceM         *float64 `json:"distance_m,omitempty"`
	Done              *bool    `json:"done,omitempty"`
	Name              string   `json:"name,omitempty" jsonschema_description:"New name, for rename_workout or update_exercise."`
	Measurement       string   `json:"measurement,omitempty" jsonschema:"enum=reps_load,enum=bodyweight,enum=assisted,enum=timed,enum=distance"`
	Unilateral        *bool    `json:"unilateral,omitempty"`
	Equipment         string   `json:"equipment,omitempty"`
	PrimaryMuscles    []string `json:"primary_muscles,omitempty"`
	MET               *float64 `json:"met,omitempty"`
	Confirm           bool     `json:"confirm,omitempty" jsonschema_description:"For update_exercise: the user confirmed it, so it is no longer Approximate."`
}

// changeCard collects one card's edits: the rows to write, the versions they
// were based on, and the before/after lines.
type changeCard struct {
	t         *turn
	changes   []*milo.Change
	base      []*milo.RowVersion
	lines     []*milo.CardLine
	exercises map[uuid.UUID]*setEdits // by workout exercise, in the order touched
	order     []uuid.UUID
	workoutID uuid.UUID // the first workout touched, for the subtitle
}

// setEdits is one exercise's sets before and after the card.
type setEdits struct {
	measurement string
	before      []query.Set
	after       map[uuid.UUID]*query.Set // nil = removed
	added       []query.Set
}

func (t *turn) proposeChange(ctx context.Context, in changeInput) (any, error) {
	if len(in.Edits) == 0 {
		return nil, badInput("edits can't be empty")
	}
	c := &changeCard{t: t, exercises: map[uuid.UUID]*setEdits{}}
	for _, e := range in.Edits {
		var err error
		switch e.Op {
		case "update_set", "delete_set":
			err = c.editSet(ctx, e)
		case "add_set":
			err = c.addSet(ctx, e)
		case "rename_workout":
			err = c.renameWorkout(ctx, e)
		case "update_exercise":
			err = c.updateExercise(ctx, e)
		default:
			err = badInput("unknown op %q", e.Op)
		}
		if err != nil {
			return nil, err
		}
	}
	if len(c.changes) == 0 {
		return nil, badInput("these edits change nothing")
	}
	for _, weID := range c.order {
		c.lines = append(c.lines, c.setLines(c.exercises[weID])...)
	}

	subtitle := in.Subtitle
	if subtitle == "" && c.workoutID != uuid.Nil {
		if w, err := t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: c.workoutID, UserID: t.userID}); err == nil {
			subtitle = nameOr(w.Name, "Workout") + ", " + t.day(w.StartedAt)
		}
	}
	card := &milo.Card{Kind: milo.CardKind_CARD_KIND_CHANGE, Title: in.Title, Subtitle: subtitle, Lines: c.lines, Changes: c.changes, Base: c.base}
	if c.workoutID != uuid.Nil {
		card.WorkoutId = c.workoutID.String()
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": "The user sees the before/after card. Nothing changes until they tap Apply."}, nil
}

// sets loads a workout exercise's sets the first time an edit touches it.
func (c *changeCard) sets(ctx context.Context, weID uuid.UUID) (*setEdits, error) {
	if e, ok := c.exercises[weID]; ok {
		return e, nil
	}
	we, err := c.t.s.q.GetUserWorkoutExercise(ctx, query.GetUserWorkoutExerciseParams{ID: weID, UserID: c.t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badInput("no workout exercise %s in this user's log", weID)
	}
	if err != nil {
		return nil, err
	}
	ex, err := c.t.visibleExercise(ctx, we.ExerciseID)
	if err != nil {
		return nil, err
	}
	sets, err := c.t.s.q.SetsFor(ctx, query.SetsForParams{UserID: c.t.userID, WeIds: []uuid.UUID{weID}})
	if err != nil {
		return nil, err
	}
	e := &setEdits{measurement: ex.Measurement, before: sets, after: map[uuid.UUID]*query.Set{}}
	c.exercises[weID] = e
	c.order = append(c.order, weID)
	if c.workoutID == uuid.Nil {
		c.workoutID = we.WorkoutID
	}
	return e, nil
}

func (c *changeCard) editSet(ctx context.Context, in editInput) error {
	id, err := parseUUID(in.SetID, "set_id")
	if err != nil {
		return err
	}
	s, err := c.t.s.q.GetUserSet(ctx, query.GetUserSetParams{ID: id, UserID: c.t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return badInput("no set %s in this user's log", id)
	}
	if err != nil {
		return err
	}
	e, err := c.sets(ctx, s.WorkoutExerciseID)
	if err != nil {
		return err
	}
	c.base = append(c.base, &milo.RowVersion{Table: "set", Id: s.ID.String(), UpdatedAt: ts(s.UpdatedAt)})
	if in.Op == "delete_set" {
		e.after[s.ID] = nil
		row := rows.Set(s)
		row.Deleted, row.UpdatedAt = true, ts(c.t.now)
		c.changes = append(c.changes, rows.Change(row))
		return nil
	}
	if err := apply(&s, in); err != nil {
		return err
	}
	e.after[s.ID] = &s
	row := rows.Set(s)
	row.UpdatedAt = ts(c.t.now)
	c.changes = append(c.changes, rows.Change(row))
	return nil
}

func (c *changeCard) addSet(ctx context.Context, in editInput) error {
	weID, err := parseUUID(in.WorkoutExerciseID, "workout_exercise_id")
	if err != nil {
		return err
	}
	e, err := c.sets(ctx, weID)
	if err != nil {
		return err
	}
	s := query.Set{ID: newID(), WorkoutExerciseID: weID, Position: int32(len(e.before) + len(e.added) + 1), Kind: "normal", WeightUnit: "kg", Done: true}
	if n := len(e.before); n > 0 {
		s.WeightUnit = e.before[n-1].WeightUnit
	}
	if err := apply(&s, in); err != nil {
		return err
	}
	e.added = append(e.added, s)
	row := rows.Set(s)
	row.UpdatedAt = ts(c.t.now)
	c.changes = append(c.changes, rows.Change(row))
	return nil
}

// apply copies the fields an edit sets onto a set.
func apply(s *query.Set, in editInput) error {
	if in.Weight != nil {
		w := strings.TrimSpace(*in.Weight)
		if !rows.ValidWeight(w) {
			return badInput("weight %q must be a plain number", w)
		}
		s.Weight = w
	}
	if in.Unit == "kg" || in.Unit == "lb" {
		s.WeightUnit = in.Unit
	}
	if in.Reps != nil {
		s.Reps = in.Reps
	}
	if in.PerSide != nil {
		s.PerSide = *in.PerSide
	}
	if in.DurationS != nil {
		s.DurationS = in.DurationS
	}
	if in.DistanceM != nil {
		s.DistanceM = in.DistanceM
	}
	if in.Done != nil {
		s.Done = *in.Done
	}
	return nil
}

func (c *changeCard) renameWorkout(ctx context.Context, in editInput) error {
	id, err := parseUUID(in.WorkoutID, "workout_id")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return badInput("rename_workout needs a name")
	}
	w, err := c.t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: id, UserID: c.t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return badInput("no workout %s in this user's log", id)
	}
	if err != nil {
		return err
	}
	c.base = append(c.base, &milo.RowVersion{Table: "workout", Id: w.ID.String(), UpdatedAt: ts(w.UpdatedAt)})
	c.lines = append(c.lines, &milo.CardLine{Label: "Workout name", Before: w.Name, After: name})
	row := rows.Workout(w)
	row.Name, row.UpdatedAt = name, ts(c.t.now)
	c.changes = append(c.changes, rows.Change(row))
	if c.workoutID == uuid.Nil {
		c.workoutID = w.ID
	}
	return nil
}

func (c *changeCard) updateExercise(ctx context.Context, in editInput) error {
	id, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return err
	}
	ex, err := c.t.visibleExercise(ctx, id)
	if err != nil {
		return err
	}
	if ex.UserID == nil {
		return badInput("%s is built in and can't be edited; propose a new exercise instead", ex.Name)
	}
	c.base = append(c.base, &milo.RowVersion{Table: "exercise", Id: ex.ID.String(), UpdatedAt: ts(ex.UpdatedAt)})
	row := rows.Exercise(ex)
	row.UpdatedAt = ts(c.t.now)
	line := func(label, before, after string) {
		c.lines = append(c.lines, &milo.CardLine{Label: label, Before: before, After: after})
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
		line("Equipment", ex.Equipment, in.Equipment)
		row.Equipment = in.Equipment
	}
	if len(in.PrimaryMuscles) > 0 {
		line("Works", strings.Join(ex.PrimaryMuscles, ", "), strings.Join(in.PrimaryMuscles, ", "))
		row.PrimaryMuscles = in.PrimaryMuscles
	}
	if in.MET != nil {
		row.Met = in.MET
	}
	if in.Confirm && ex.Approximate {
		line("Status", "Approximate", "Confirmed")
		row.Approximate = false
	}
	c.changes = append(c.changes, rows.Change(row))
	return nil
}

// setLines shows untouched sets as one line, each changed or added set, and the volume before and after.
func (c *changeCard) setLines(e *setEdits) []*milo.CardLine {
	var lines []*milo.CardLine
	var untouched []int
	var before, after []query.Set
	for i, s := range e.before {
		n := i + 1
		before = append(before, s)
		edited, touched := e.after[s.ID]
		switch {
		case !touched:
			untouched = append(untouched, n)
			after = append(after, s)
		case edited == nil:
			lines = append(lines, &milo.CardLine{Label: fmt.Sprintf("Set %d", n), Before: setTextRow(e.measurement, s), After: "Removed"})
		default:
			after = append(after, *edited)
			lines = append(lines, &milo.CardLine{Label: fmt.Sprintf("Set %d", n), Before: setTextRow(e.measurement, s), After: setTextRow(e.measurement, *edited)})
		}
	}
	for i, s := range e.added {
		after = append(after, s)
		lines = append(lines, &milo.CardLine{Label: fmt.Sprintf("Set %d", len(e.before)+i+1), After: setTextRow(e.measurement, s)})
	}
	if len(untouched) > 0 {
		text := "unchanged"
		if same := pick(e.before, untouched); allSame(e.measurement, same) {
			text = setTextRow(e.measurement, same[0]) + ", unchanged"
		}
		lines = append([]*milo.CardLine{{Label: setRange(untouched), After: text}}, lines...)
	}
	v0 := maths.VolumeKg(setInputs(e.measurement, before))
	v1 := maths.VolumeKg(setInputs(e.measurement, after))
	if e.measurement == maths.RepsLoad && v0 != v1 {
		lines = append(lines, &milo.CardLine{Label: "Volume for this exercise", Before: thousands(int64(displayKg(v0, c.t.displayUnit) + 0.5)), After: kgText(v1, c.t.displayUnit)})
	}
	return lines
}

// setRange is "Set 2", "Sets 1 to 3" or "Sets 1, 3".
func setRange(ns []int) string {
	if len(ns) == 1 {
		return fmt.Sprintf("Set %d", ns[0])
	}
	if ns[len(ns)-1]-ns[0] == len(ns)-1 {
		return fmt.Sprintf("Sets %d to %d", ns[0], ns[len(ns)-1])
	}
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprint(n)
	}
	return "Sets " + strings.Join(parts, ", ")
}

func pick(sets []query.Set, ns []int) []query.Set {
	out := make([]query.Set, len(ns))
	for i, n := range ns {
		out[i] = sets[n-1]
	}
	return out
}

func allSame(measurement string, sets []query.Set) bool {
	for _, s := range sets[1:] {
		if setTextRow(measurement, s) != setTextRow(measurement, sets[0]) {
			return false
		}
	}
	return true
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
