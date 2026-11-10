package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// propose_change: edits to workouts already in the log, shown as one
// before/after card. Nothing changes until the user applies it.

type changeInput struct {
	Title     string         `json:"title" jsonschema_description:"What is being changed, e.g. the exercise or workout name."`
	Subtitle  string         `json:"subtitle,omitempty" jsonschema_description:"e.g. \"Shoulders, Tue 6 Oct\". Defaults to the workout and its date."`
	Sets      []setEdit      `json:"sets,omitempty" jsonschema_description:"Edits to single sets."`
	Exercises []exerciseEdit `json:"exercises,omitempty" jsonschema_description:"Edits to an exercise inside a workout: remove it, swap it for another, move it, note it, superset it."`
	Workouts  []workoutEdit  `json:"workouts,omitempty" jsonschema_description:"Edits to a workout itself: its name, note, start or end time."`
}

type setEdit struct {
	Op                string   `json:"op" jsonschema:"enum=update,enum=delete,enum=add"`
	SetID             string   `json:"set_id,omitempty" jsonschema_description:"For update and delete."`
	WorkoutExerciseID string   `json:"workout_exercise_id,omitempty" jsonschema_description:"For add: the exercise in the workout that gets the set."`
	Reps              *int32   `json:"reps,omitempty"`
	Weight            *string  `json:"weight,omitempty" jsonschema_description:"Exact weight, e.g. \"22.5\"; \"\" for none."`
	Unit              string   `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb"`
	PerSide           *bool    `json:"per_side,omitempty"`
	DurationS         *int32   `json:"duration_s,omitempty"`
	DistanceM         *float64 `json:"distance_m,omitempty"`
	Done              *bool    `json:"done,omitempty"`
	Kind              string   `json:"kind,omitempty" jsonschema:"enum=normal,enum=warmup,enum=drop,enum=failure"`
}

type exerciseEdit struct {
	Op                string  `json:"op" jsonschema:"enum=update,enum=remove"`
	WorkoutExerciseID string  `json:"workout_exercise_id"`
	ExerciseID        string  `json:"exercise_id,omitempty" jsonschema_description:"Swap it for this exercise, keeping its sets (it was logged under the wrong one)."`
	Position          *int32  `json:"position,omitempty" jsonschema_description:"Move it to this place in the workout, counting from 1."`
	Notes             *string `json:"notes,omitempty" jsonschema_description:"The note on this exercise in this workout; \"\" clears it."`
	GroupLabel        *string `json:"group_label,omitempty" jsonschema_description:"Supersets: neighbouring exercises with the same label are done back to back. \"\" takes it out of its superset."`
}

type workoutEdit struct {
	WorkoutID string  `json:"workout_id"`
	Name      string  `json:"name,omitempty"`
	Notes     *string `json:"notes,omitempty" jsonschema_description:"\"\" clears the note."`
	StartedAt string  `json:"started_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM."`
	EndedAt   string  `json:"ended_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM."`
}

// changeCard collects one card's edits. Each row is loaded once and edited in
// place, so several edits to the same row add up; the changes are read off at the end.
type changeCard struct {
	t         *turn
	base      []*milo.RowVersion
	lines     []*milo.CardLine
	workouts  []*milo.Workout
	exercises []*exerciseEdits // in the order touched
	workoutID uuid.UUID        // the first workout touched, for the subtitle
}

// exerciseEdits is one exercise in a workout, and its sets, before and after the card.
type exerciseEdits struct {
	we          *milo.WorkoutExercise
	name        string
	measurement string
	edited      bool // the exercise row itself changed
	before      []query.Set
	after       map[uuid.UUID]*query.Set // nil = removed
	added       []query.Set
}

func (t *turn) proposeChange(ctx context.Context, in changeInput) (any, error) {
	c := &changeCard{t: t}
	for _, e := range in.Workouts {
		if err := c.editWorkout(ctx, e); err != nil {
			return nil, err
		}
	}
	for _, e := range in.Exercises {
		if err := c.editExercise(ctx, e); err != nil {
			return nil, err
		}
	}
	for _, e := range in.Sets {
		if err := c.editSet(ctx, e); err != nil {
			return nil, err
		}
	}
	changes := c.changes()
	if len(changes) == 0 {
		return nil, badInput("these edits change nothing")
	}
	for _, e := range c.exercises {
		c.lines = append(c.lines, c.setLines(e)...)
	}

	subtitle := in.Subtitle
	if subtitle == "" && c.workoutID != uuid.Nil {
		if w, err := t.userWorkout(ctx, c.workoutID); err == nil {
			subtitle = nameOr(w.Name, "Workout") + ", " + t.day(w.StartedAt)
		}
	}
	card := &milo.Card{
		Kind: milo.CardKind_CARD_KIND_CHANGE, Title: in.Title, Subtitle: subtitle, Lines: c.lines,
		Changes: changes, Base: c.base, AppliedTitle: in.Title + " updated",
	}
	if c.workoutID != uuid.Nil {
		card.WorkoutId = c.workoutID.String()
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": proposedNote}, nil
}

// proposedNote is what Milo is told after a card that waits for a tap.
const proposedNote = "The user sees the card. Nothing changes until they tap the button on it."

func (c *changeCard) line(label, before, after string) {
	c.lines = append(c.lines, &milo.CardLine{Label: label, Before: before, After: after})
}

func (c *changeCard) touch(workoutID uuid.UUID) {
	if c.workoutID == uuid.Nil {
		c.workoutID = workoutID
	}
}

// changes reads the edited rows off the card: workouts, then exercises, then
// sets. It also notes the version of each exercise row it rewrites.
func (c *changeCard) changes() []*milo.Change {
	now := ts(c.t.now)
	var out []*milo.Change
	for _, w := range c.workouts {
		w.UpdatedAt = now
		out = append(out, rows.Change(w))
	}
	for _, e := range c.exercises {
		if e.edited {
			c.base = append(c.base, &milo.RowVersion{Table: "workout_exercise", Id: e.we.GetId(), UpdatedAt: e.we.GetUpdatedAt()})
			e.we.UpdatedAt = now
			out = append(out, rows.Change(e.we))
		}
	}
	for _, e := range c.exercises {
		for _, s := range e.before {
			edited, touched := e.after[s.ID]
			if !touched && !e.we.GetDeleted() {
				continue
			}
			row := rows.Set(s)
			if edited != nil {
				row = rows.Set(*edited)
			}
			row.Deleted = edited == nil || e.we.GetDeleted()
			row.UpdatedAt = now
			out = append(out, rows.Change(row))
		}
		for _, s := range e.added {
			row := rows.Set(s)
			row.UpdatedAt = now
			out = append(out, rows.Change(row))
		}
	}
	return out
}

// MARK: Workouts

func (c *changeCard) editWorkout(ctx context.Context, in workoutEdit) error {
	id, err := parseUUID(in.WorkoutID, "workout_id")
	if err != nil {
		return err
	}
	stored, err := c.t.userWorkout(ctx, id)
	if err != nil {
		return err
	}
	w := rows.Workout(stored)
	before := len(c.lines)
	if name := strings.TrimSpace(in.Name); name != "" && name != w.Name {
		c.line("Workout name", w.Name, name)
		w.Name = name
	}
	if in.Notes != nil && strings.TrimSpace(*in.Notes) != w.Notes {
		c.line("Note", orNone(w.Notes), orNone(strings.TrimSpace(*in.Notes)))
		w.Notes = strings.TrimSpace(*in.Notes)
	}
	if in.StartedAt != "" {
		at, err := c.t.parseLocal(in.StartedAt, "started_at")
		if err != nil {
			return err
		}
		if !at.Equal(stored.StartedAt) {
			c.line("Started", c.t.clock(stored.StartedAt), c.t.clock(at))
			w.StartedAt = ts(at)
		}
	}
	if in.EndedAt != "" {
		if stored.EndedAt == nil {
			return badInput("%q is still running; finish_workout ends it", nameOr(stored.Name, "That workout"))
		}
		at, err := c.t.parseLocal(in.EndedAt, "ended_at")
		if err != nil {
			return err
		}
		if !at.Equal(*stored.EndedAt) {
			c.line("Ended", c.t.clock(*stored.EndedAt), c.t.clock(at))
			w.EndedAt = ts(at)
		}
	}
	if w.EndedAt != nil && !w.EndedAt.AsTime().After(w.StartedAt.AsTime()) {
		return badInput("the workout would end before it starts (%s to %s)", c.t.clock(w.StartedAt.AsTime()), c.t.clock(w.EndedAt.AsTime()))
	}
	if w.StartedAt.AsTime().After(c.t.now.Add(time.Minute)) {
		return badInput("a workout can't start in the future")
	}
	if len(c.lines) == before {
		return nil
	}
	c.base = append(c.base, version("workout", stored.ID, stored.UpdatedAt))
	c.workouts = append(c.workouts, w)
	c.touch(stored.ID)
	return nil
}

// MARK: Exercises in a workout

// exercise loads a workout exercise and its sets the first time an edit touches it.
func (c *changeCard) exercise(ctx context.Context, id string) (*exerciseEdits, error) {
	for _, e := range c.exercises {
		if e.we.GetId() == id {
			return e, nil
		}
	}
	we, err := c.t.userWorkoutExercise(ctx, id)
	if err != nil {
		return nil, err
	}
	ex, err := c.t.visibleExercise(ctx, we.ExerciseID)
	if err != nil {
		return nil, err
	}
	sets, err := c.t.s.q.SetsFor(ctx, query.SetsForParams{UserID: c.t.userID, WeIds: []uuid.UUID{we.ID}})
	if err != nil {
		return nil, err
	}
	e := &exerciseEdits{we: rows.WorkoutExercise(we), name: ex.Name, measurement: ex.Measurement, before: sets, after: map[uuid.UUID]*query.Set{}}
	c.exercises = append(c.exercises, e)
	c.touch(we.WorkoutID)
	return e, nil
}

func (c *changeCard) editExercise(ctx context.Context, in exerciseEdit) error {
	e, err := c.exercise(ctx, in.WorkoutExerciseID)
	if err != nil {
		return err
	}
	if in.Op == "remove" {
		e.we.Deleted, e.edited = true, true
		c.line(e.name, exerciseSummary(e.measurement, e.before), "Removed")
		return nil
	}
	if in.Op != "update" {
		return badInput("exercise op must be update or remove, not %q", in.Op)
	}
	if in.ExerciseID != "" && in.ExerciseID != e.we.GetExerciseId() {
		to, err := c.t.exercise(ctx, in.ExerciseID)
		if err != nil {
			return err
		}
		if to.Measurement != e.measurement {
			return badInput("%s is counted differently from %s, so the sets wouldn't carry over. Remove the exercise and log the sets under %s instead", to.Name, e.name, to.Name)
		}
		c.line("Exercise", e.name, to.Name)
		e.we.ExerciseId, e.name, e.edited = to.ID.String(), to.Name, true
	}
	if in.Notes != nil && strings.TrimSpace(*in.Notes) != e.we.GetNotes() {
		c.line("Note on "+shortName(e.name), orNone(e.we.GetNotes()), orNone(strings.TrimSpace(*in.Notes)))
		e.we.Notes, e.edited = strings.TrimSpace(*in.Notes), true
	}
	if in.GroupLabel != nil && *in.GroupLabel != e.we.GetGroupLabel() {
		c.line(e.name, supersetText(e.we.GetGroupLabel()), supersetText(*in.GroupLabel))
		e.we.GroupLabel, e.edited = *in.GroupLabel, true
	}
	if in.Position != nil {
		return c.move(ctx, e, int(*in.Position))
	}
	return nil
}

// move puts an exercise at a new place and renumbers the ones it passes.
func (c *changeCard) move(ctx context.Context, e *exerciseEdits, to int) error {
	workoutID := uuid.MustParse(e.we.GetWorkoutId())
	wes, err := c.t.s.q.WorkoutExercisesFor(ctx, query.WorkoutExercisesForParams{UserID: c.t.userID, WorkoutIds: []uuid.UUID{workoutID}})
	if err != nil {
		return err
	}
	order := make([]string, len(wes))
	for i, we := range wes {
		order[i] = we.ID.String()
	}
	from := slices.Index(order, e.we.GetId())
	if to < 1 || to > len(order) {
		return badInput("position %d is outside the workout's %d exercises", to, len(order))
	}
	if from == to-1 {
		return nil
	}
	order = slices.Insert(slices.Delete(order, from, from+1), to-1, e.we.GetId())
	c.line(e.name, ordinal(from+1), ordinal(to))
	for i, id := range order {
		moved, err := c.exercise(ctx, id)
		if err != nil {
			return err
		}
		if moved.we.GetPosition() != int32(i+1) {
			moved.we.Position, moved.edited = int32(i+1), true
		}
	}
	return nil
}

// MARK: Sets

func (c *changeCard) editSet(ctx context.Context, in setEdit) error {
	if in.Kind != "" && rows.Parse[milo.SetKind](in.Kind) == milo.SetKind_SET_KIND_UNSPECIFIED {
		return badInput("kind must be normal, warmup, drop or failure, not %q", in.Kind)
	}
	if in.Op == "add" {
		e, err := c.exercise(ctx, in.WorkoutExerciseID)
		if err != nil {
			return err
		}
		s := query.Set{ID: newID(), WorkoutExerciseID: uuid.MustParse(e.we.GetId()), Position: 1, Kind: "normal", WeightUnit: "kg", Done: true}
		if last, ok := e.last(); ok {
			s.Position, s.WeightUnit, s.PerSide = last.Position+1, last.WeightUnit, last.PerSide
		}
		if err := in.apply(&s); err != nil {
			return err
		}
		e.added = append(e.added, s)
		return nil
	}
	if in.Op != "update" && in.Op != "delete" {
		return badInput("set op must be update, delete or add, not %q", in.Op)
	}
	id, err := parseUUID(in.SetID, "set_id")
	if err != nil {
		return err
	}
	stored, err := c.t.s.q.GetUserSet(ctx, query.GetUserSetParams{ID: id, UserID: c.t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return badInput("no set %s in this user's log", id)
	}
	if err != nil {
		return err
	}
	e, err := c.exercise(ctx, stored.WorkoutExerciseID.String())
	if err != nil {
		return err
	}
	if _, touched := e.after[id]; !touched {
		c.base = append(c.base, version("set", id, stored.UpdatedAt))
	}
	if in.Op == "delete" {
		e.after[id] = nil
		return nil
	}
	s := stored
	if earlier := e.after[id]; earlier != nil {
		s = *earlier
	}
	if err := in.apply(&s); err != nil {
		return err
	}
	e.after[id] = &s
	return nil
}

// last is the exercise's final set once the card's edits so far are counted.
func (e *exerciseEdits) last() (query.Set, bool) {
	if n := len(e.added); n > 0 {
		return e.added[n-1], true
	}
	if n := len(e.before); n > 0 {
		return e.before[n-1], true
	}
	return query.Set{}, false
}

// apply copies the fields an edit sets onto a set.
func (in setEdit) apply(s *query.Set) error {
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
	if in.Kind != "" {
		s.Kind = in.Kind
	}
	return nil
}

// setLines shows untouched sets as one line, each changed or added set, and the volume before and after.
func (c *changeCard) setLines(e *exerciseEdits) []*milo.CardLine {
	if e.we.GetDeleted() || (len(e.after) == 0 && len(e.added) == 0) {
		return nil
	}
	var lines []*milo.CardLine
	var untouched []int
	var after []query.Set
	for i, s := range e.before {
		n := i + 1
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
	v0 := maths.VolumeKg(setInputs(e.measurement, e.before))
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
