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
