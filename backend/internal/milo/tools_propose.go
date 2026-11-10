package milo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
)

// Proposal tools build cards. A card carries the exact rows the phone writes
// when the user acts on it; nothing here writes user data.

type setIn struct {
	Reps      *int32   `json:"reps"`
	Weight    string   `json:"weight"`
	Unit      string   `json:"unit"`
	PerSide   bool     `json:"per_side"`
	DurationS *int32   `json:"duration_s"`
	DistanceM *float64 `json:"distance_m"`
}

var weightPattern = regexp.MustCompile(`^\d{1,6}(\.\d{1,3})?$`)

var measurements = map[string]milov1.Measurement{
	maths.RepsLoad:   milov1.Measurement_MEASUREMENT_REPS_LOAD,
	maths.Bodyweight: milov1.Measurement_MEASUREMENT_BODYWEIGHT,
	maths.Assisted:   milov1.Measurement_MEASUREMENT_ASSISTED,
	maths.Timed:      milov1.Measurement_MEASUREMENT_TIMED,
	maths.Distance:   milov1.Measurement_MEASUREMENT_DISTANCE,
}

func unitProto(u string) milov1.WeightUnit {
	if u == "lb" {
		return milov1.WeightUnit_WEIGHT_UNIT_LB
	}
	return milov1.WeightUnit_WEIGHT_UNIT_KG
}

func (s *setIn) check() error {
	s.Weight = strings.TrimSpace(s.Weight)
	if s.Weight != "" && !weightPattern.MatchString(s.Weight) {
		return badInput("weight %q must be a plain number like 20 or 22.5", s.Weight)
	}
	switch s.Unit {
	case "":
		s.Unit = "kg"
	case "kg", "lb":
	default:
		return badInput("unit must be kg or lb, not %q", s.Unit)
	}
	if s.Reps != nil && (*s.Reps < 0 || *s.Reps > 1000) {
		return badInput("reps %d is out of range", *s.Reps)
	}
	return nil
}

func (s setIn) text(measurement string) string {
	return setText(measurement, s.Weight, s.Unit, s.Reps, s.PerSide, s.DurationS, s.DistanceM)
}

func (s setIn) row(id, weID uuid.UUID, position int32, now time.Time) *milov1.Set {
	return &milov1.Set{
		Id: id.String(), WorkoutExerciseId: weID.String(), Position: position, Kind: milov1.SetKind_SET_KIND_NORMAL,
		Reps: s.Reps, Weight: s.Weight, WeightUnit: unitProto(s.Unit), PerSide: s.PerSide,
		DurationS: s.DurationS, DistanceM: s.DistanceM, Done: true, UpdatedAt: timestamppb.New(now),
	}
}

func newID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return id
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func change(row any) *milov1.Change {
	switch r := row.(type) {
	case *milov1.Exercise:
		return &milov1.Change{Row: &milov1.Change_Exercise{Exercise: r}}
	case *milov1.ExercisePref:
		return &milov1.Change{Row: &milov1.Change_ExercisePref{ExercisePref: r}}
	case *milov1.Workout:
		return &milov1.Change{Row: &milov1.Change_Workout{Workout: r}}
	case *milov1.WorkoutExercise:
		return &milov1.Change{Row: &milov1.Change_WorkoutExercise{WorkoutExercise: r}}
	case *milov1.Set:
		return &milov1.Change{Row: &milov1.Change_Set{Set: r}}
	case *milov1.Template:
		return &milov1.Change{Row: &milov1.Change_Template{Template: r}}
	}
	panic(fmt.Sprintf("change: unexpected row %T", row))
}

// target is where new sets go: an existing workout or one created in this turn.
type target struct {
	workoutID uuid.UUID
	name      string
	startedAt time.Time
	fresh     *newWorkout // non-nil when created in this turn
}

func (t *turn) resolveWorkout(ctx context.Context, workoutID string) (target, error) {
	if workoutID == "" && t.mctx.GetWorkoutId() != "" {
		workoutID = t.mctx.GetWorkoutId()
	}
	if workoutID == "" {
		return target{}, badInput("there is no open workout: pass workout_id, or new_workout for a session that isn't logged yet")
	}
	id, err := parseUUID(workoutID, "workout_id")
	if err != nil {
		return target{}, err
	}
	if nw, ok := t.newWorkouts[id]; ok {
		return target{workoutID: id, name: nw.name, startedAt: nw.startedAt, fresh: nw}, nil
	}
	w, err := t.s.q.GetUserWorkout(ctx, dbq.GetUserWorkoutParams{ID: id, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return target{}, badInput("no workout %s in this user's log", id)
	}
	if err != nil {
		return target{}, err
	}
	return target{workoutID: id, name: w.Name, startedAt: w.StartedAt}, nil
}

// addExercise returns the workout exercise to put sets in, creating it (as a change) when needed,
// and the position the next set takes.
func (t *turn) addExercise(ctx context.Context, tg target, exerciseID uuid.UUID, reuse bool) (uuid.UUID, int32, []*milov1.Change, error) {
	if tg.fresh != nil {
		if weID, ok := tg.fresh.exercises[exerciseID]; ok && reuse {
			return weID, tg.fresh.setCounts[weID] + 1, nil, nil
		}
		tg.fresh.position++
		weID := newID()
		tg.fresh.exercises[exerciseID] = weID
		we := &milov1.WorkoutExercise{Id: weID.String(), WorkoutId: tg.workoutID.String(), ExerciseId: exerciseID.String(), Position: tg.fresh.position, UpdatedAt: ts(t.now)}
		return weID, 1, []*milov1.Change{change(we)}, nil
	}
	if reuse {
		we, err := t.s.q.FindWorkoutExercise(ctx, dbq.FindWorkoutExerciseParams{WorkoutID: tg.workoutID, ExerciseID: exerciseID, UserID: t.userID})
		if err == nil {
			pos, err := t.s.q.MaxSetPosition(ctx, dbq.MaxSetPositionParams{WorkoutExerciseID: we.ID, UserID: t.userID})
			return we.ID, pos + 1, nil, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, 0, nil, err
		}
	}
	pos, err := t.s.q.MaxExercisePosition(ctx, dbq.MaxExercisePositionParams{WorkoutID: tg.workoutID, UserID: t.userID})
	if err != nil {
		return uuid.UUID{}, 0, nil, err
	}
	weID := newID()
	we := &milov1.WorkoutExercise{Id: weID.String(), WorkoutId: tg.workoutID.String(), ExerciseId: exerciseID.String(), Position: pos + 1, UpdatedAt: ts(t.now)}
	return weID, 1, []*milov1.Change{change(we)}, nil
}

func countedText(measurement string, unilateral bool) string {
	switch measurement {
	case maths.Bodyweight:
		return "Reps at body weight"
	case maths.Assisted:
		return "Reps with assistance"
	case maths.Timed:
		return "Time"
	case maths.Distance:
		return "Distance"
	}
	if unilateral {
		return "Reps each side"
	}
	return "Reps at a weight"
}

func (t *turn) proposeNewExercise(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		Name             string   `json:"name"`
		Aliases          []string `json:"aliases"`
		PrimaryMuscles   []string `json:"primary_muscles"`
		SecondaryMuscles []string `json:"secondary_muscles"`
		MovementPattern  string   `json:"movement_pattern"`
		Equipment        string   `json:"equipment"`
		Measurement      string   `json:"measurement"`
		Unilateral       bool     `json:"unilateral"`
		MET              float64  `json:"met"`
		Description      string   `json:"description"`
		WorksSummary     string   `json:"works_summary"`
		EquipmentSummary string   `json:"equipment_summary"`
		MovementSummary  string   `json:"movement_summary"`
		WorkoutID        string   `json:"workout_id"`
		Sets             []setIn  `json:"sets"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, badInput("name is required")
	}
	m, ok := measurements[in.Measurement]
	if !ok {
		return nil, badInput("measurement must be one of reps_load, bodyweight, assisted, timed, distance")
	}
	if in.MET < 1.5 || in.MET > 15 {
		return nil, badInput("met %.1f is out of range (strength work is usually 3 to 6)", in.MET)
	}
	if len(in.PrimaryMuscles) == 0 {
		return nil, badInput("primary_muscles needs at least one muscle")
	}
	if existing, err := t.s.q.SearchExercises(ctx, dbq.SearchExercisesParams{UserID: t.userID, Query: in.Name, Lim: 3}); err == nil {
		for _, e := range existing {
			if strings.EqualFold(e.Name, in.Name) {
				return nil, badInput("an exercise called %q already exists (exercise_id %s); log to it with log_sets", e.Name, e.ID)
			}
		}
	}
	for i := range in.Sets {
		if err := in.Sets[i].check(); err != nil {
			return nil, err
		}
		// Sets counted "each side" mean the exercise is done one side at a time.
		if in.Sets[i].PerSide {
			in.Unilateral = true
		}
	}

	exerciseID := newID()
	met := in.MET
	ex := &milov1.Exercise{
		Id: exerciseID.String(), Name: in.Name, Aliases: in.Aliases, PrimaryMuscles: in.PrimaryMuscles,
		SecondaryMuscles: in.SecondaryMuscles, MovementPattern: in.MovementPattern, Equipment: in.Equipment,
		Measurement: m, Unilateral: in.Unilateral, Met: &met, Description: in.Description, Approximate: true,
		UpdatedAt: ts(t.now),
	}
	changes := []*milov1.Change{change(ex)}
	lines := []*milov1.CardLine{
		{Label: "Works", After: in.WorksSummary},
		{Label: "Equipment", After: in.EquipmentSummary},
		{Label: "Counted", After: countedText(in.Measurement, in.Unilateral)},
		{Label: "Movement", After: in.MovementSummary},
	}
	card := &milov1.Card{
		Kind: milov1.CardKind_CARD_KIND_NEW_EXERCISE, Title: in.Name, Subtitle: "New exercise, saved as yours",
		ExerciseId: exerciseID.String(),
	}

	if len(in.Sets) > 0 {
		tg, err := t.resolveWorkout(ctx, in.WorkoutID)
		if err != nil {
			return nil, err
		}
		weID, pos, weChanges, err := t.addExercise(ctx, tg, exerciseID, false)
		if err != nil {
			return nil, err
		}
		changes = append(changes, weChanges...)
		for i, s := range in.Sets {
			changes = append(changes, change(s.row(newID(), weID, pos+int32(i), t.now)))
			lines = append(lines, &milov1.CardLine{Label: fmt.Sprintf("Set %d", i+1), After: s.text(in.Measurement)})
		}
		if tg.fresh != nil {
			tg.fresh.setCounts[weID] = pos + int32(len(in.Sets)) - 1
		}
		if in.Sets[0].Weight != "" {
			changes = append(changes, change(&milov1.ExercisePref{ExerciseId: exerciseID.String(), Unit: unitProto(in.Sets[0].Unit), UpdatedAt: ts(t.now)}))
		}
		card.WorkoutId = tg.workoutID.String()
		card.Note = "Good enough to log now. You can tighten the details later from History."
	}
	card.Lines = lines
	card.Changes = changes
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "exercise_id": exerciseID.String(), "shown": "The user sees the card and adds or edits it. Nothing is saved yet."}, nil
}

// parseLocal reads "2006-01-02T15:04" in the user's zone (or RFC 3339).
func (t *turn) parseLocal(s, what string) (time.Time, error) {
	if v, err := time.ParseInLocation("2006-01-02T15:04", s, t.loc); err == nil {
		return v, nil
	}
	if v, err := time.Parse(time.RFC3339, s); err == nil {
		return v, nil
	}
	return time.Time{}, badInput("%s %q must look like 2026-11-09T18:30", what, s)
}

func (t *turn) logSets(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		ExerciseID string `json:"exercise_id"`
		WorkoutID  string `json:"workout_id"`
		NewWorkout *struct {
			Name      string `json:"name"`
			StartedAt string `json:"started_at"`
			EndedAt   string `json:"ended_at"`
		} `json:"new_workout"`
		Sets []setIn `json:"sets"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Sets) == 0 {
		return nil, badInput("sets can't be empty")
	}
	for i := range in.Sets {
		if err := in.Sets[i].check(); err != nil {
			return nil, err
		}
	}
	exerciseID, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: exerciseID, UserID: &t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badInput("no exercise %s in this user's library; search first, or propose a new exercise", exerciseID)
	}
	if err != nil {
		return nil, err
	}

	var changes []*milov1.Change
	var tg target
	if in.NewWorkout != nil {
		started, err := t.parseLocal(in.NewWorkout.StartedAt, "started_at")
		if err != nil {
			return nil, err
		}
		ended := started.Add(time.Hour)
		if in.NewWorkout.EndedAt != "" {
			if ended, err = t.parseLocal(in.NewWorkout.EndedAt, "ended_at"); err != nil {
				return nil, err
			}
		}
		id := newID()
		nw := &newWorkout{name: in.NewWorkout.Name, startedAt: started, exercises: map[uuid.UUID]uuid.UUID{}, setCounts: map[uuid.UUID]int32{}}
		t.newWorkouts[id] = nw
		changes = append(changes, change(&milov1.Workout{Id: id.String(), Name: in.NewWorkout.Name, StartedAt: ts(started), EndedAt: ts(ended), UpdatedAt: ts(t.now)}))
		tg = target{workoutID: id, name: nw.name, startedAt: started, fresh: nw}
	} else if tg, err = t.resolveWorkout(ctx, in.WorkoutID); err != nil {
		return nil, err
	}

	weID, pos, weChanges, err := t.addExercise(ctx, tg, exerciseID, true)
	if err != nil {
		return nil, err
	}
	changes = append(changes, weChanges...)
	lines := make([]*milov1.CardLine, 0, len(in.Sets))
	for i, s := range in.Sets {
		changes = append(changes, change(s.row(newID(), weID, pos+int32(i), t.now)))
		lines = append(lines, &milov1.CardLine{Label: fmt.Sprintf("Set %d", pos+int32(i)), After: s.text(ex.Measurement)})
	}
	if tg.fresh != nil {
		tg.fresh.setCounts[weID] = pos + int32(len(in.Sets)) - 1
	}

	subtitle := ex.Name
	if tg.workoutID.String() != t.mctx.GetWorkoutId() {
		subtitle += ", " + nameOr(tg.name, "workout") + " " + dayText(tg.startedAt.In(t.loc), t.now)
	}
	card := &milov1.Card{
		Kind: milov1.CardKind_CARD_KIND_LOGGED, Title: fmt.Sprintf("Logged %d %s", len(in.Sets), plural(len(in.Sets), "set", "sets")),
		Subtitle: subtitle, Lines: lines, Changes: changes, AutoApply: true,
		WorkoutId: tg.workoutID.String(), ExerciseId: exerciseID.String(),
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "workout_id": tg.workoutID.String(), "logged": len(in.Sets), "shown": "Applied on the phone straight away; the user can undo it."}, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

type editIn struct {
	Op                string   `json:"op"`
	SetID             string   `json:"set_id"`
	WorkoutExerciseID string   `json:"workout_exercise_id"`
	WorkoutID         string   `json:"workout_id"`
	ExerciseID        string   `json:"exercise_id"`
	Reps              *int32   `json:"reps"`
	Weight            *string  `json:"weight"`
	Unit              string   `json:"unit"`
	PerSide           *bool    `json:"per_side"`
	DurationS         *int32   `json:"duration_s"`
	DistanceM         *float64 `json:"distance_m"`
	Done              *bool    `json:"done"`
	Name              string   `json:"name"`
	Measurement       string   `json:"measurement"`
	Unilateral        *bool    `json:"unilateral"`
	Equipment         string   `json:"equipment"`
	PrimaryMuscles    []string `json:"primary_muscles"`
	MET               *float64 `json:"met"`
	Confirm           bool     `json:"confirm"`
}

// exerciseEdits tracks one workout exercise's sets before and after the card, for its lines and volume.
type exerciseEdits struct {
	we          dbq.WorkoutExercise
	measurement string
	name        string
	before      []dbq.SetsForRow
	after       map[uuid.UUID]*dbq.SetsForRow // nil value = removed
	added       []dbq.SetsForRow
}

func (t *turn) proposeChange(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		Title    string   `json:"title"`
		Subtitle string   `json:"subtitle"`
		Edits    []editIn `json:"edits"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Edits) == 0 {
		return nil, badInput("edits can't be empty")
	}
	var changes []*milov1.Change
	var base []*milov1.RowVersion
	var lines []*milov1.CardLine
	byWE := map[uuid.UUID]*exerciseEdits{}
	var weOrder []uuid.UUID
	var firstWorkout uuid.UUID

	loadWE := func(weID uuid.UUID) (*exerciseEdits, error) {
		if e, ok := byWE[weID]; ok {
			return e, nil
		}
		we, err := t.s.q.GetUserWorkoutExercise(ctx, dbq.GetUserWorkoutExerciseParams{ID: weID, UserID: t.userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, badInput("no workout exercise %s in this user's log", weID)
		}
		if err != nil {
			return nil, err
		}
		ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: we.ExerciseID, UserID: &t.userID})
		if err != nil {
			return nil, err
		}
		sets, err := t.s.q.SetsFor(ctx, dbq.SetsForParams{UserID: t.userID, WeIds: []uuid.UUID{weID}})
		if err != nil {
			return nil, err
		}
		e := &exerciseEdits{we: we, measurement: ex.Measurement, name: ex.Name, before: sets, after: map[uuid.UUID]*dbq.SetsForRow{}}
		byWE[weID] = e
		weOrder = append(weOrder, weID)
		if firstWorkout == uuid.Nil {
			firstWorkout = we.WorkoutID
		}
		return e, nil
	}

	for _, ed := range in.Edits {
		switch ed.Op {
		case "update_set", "delete_set":
			setID, err := parseUUID(ed.SetID, "set_id")
			if err != nil {
				return nil, err
			}
			s, err := t.s.q.GetUserSet(ctx, dbq.GetUserSetParams{ID: setID, UserID: t.userID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, badInput("no set %s in this user's log", setID)
			}
			if err != nil {
				return nil, err
			}
			e, err := loadWE(s.WorkoutExerciseID)
			if err != nil {
				return nil, err
			}
			base = append(base, &milov1.RowVersion{Table: "set", Id: s.ID.String(), UpdatedAt: ts(s.UpdatedAt)})
			row := dbq.SetsForRow(s)
			if ed.Op == "delete_set" {
				e.after[s.ID] = nil
				del := setRowProto(row, t.now)
				del.Deleted = true
				changes = append(changes, change(del))
				continue
			}
			if ed.Reps != nil {
				row.Reps = ed.Reps
			}
			if ed.Weight != nil {
				w := strings.TrimSpace(*ed.Weight)
				if w != "" && !weightPattern.MatchString(w) {
					return nil, badInput("weight %q must be a plain number", w)
				}
				row.Weight = w
			}
			if ed.Unit == "kg" || ed.Unit == "lb" {
				row.WeightUnit = ed.Unit
			}
			if ed.PerSide != nil {
				row.PerSide = *ed.PerSide
			}
			if ed.DurationS != nil {
				row.DurationS = ed.DurationS
			}
			if ed.DistanceM != nil {
				row.DistanceM = ed.DistanceM
			}
			if ed.Done != nil {
				row.Done = *ed.Done
			}
			e.after[s.ID] = &row
			changes = append(changes, change(setRowProto(row, t.now)))
		case "add_set":
			weID, err := parseUUID(ed.WorkoutExerciseID, "workout_exercise_id")
			if err != nil {
				return nil, err
			}
			e, err := loadWE(weID)
			if err != nil {
				return nil, err
			}
			unit := ed.Unit
			if unit == "" {
				unit = "kg"
				if len(e.before) > 0 {
					unit = e.before[len(e.before)-1].WeightUnit
				}
			}
			row := dbq.SetsForRow{ID: newID(), WorkoutExerciseID: weID, Position: int32(len(e.before) + len(e.added) + 1), Kind: "normal",
				Reps: ed.Reps, WeightUnit: unit, DurationS: ed.DurationS, DistanceM: ed.DistanceM, Done: true}
			if ed.Weight != nil {
				row.Weight = strings.TrimSpace(*ed.Weight)
				if row.Weight != "" && !weightPattern.MatchString(row.Weight) {
					return nil, badInput("weight %q must be a plain number", row.Weight)
				}
			}
			if ed.PerSide != nil {
				row.PerSide = *ed.PerSide
			}
			e.added = append(e.added, row)
			changes = append(changes, change(setRowProto(row, t.now)))
		case "rename_workout":
			workoutID, err := parseUUID(ed.WorkoutID, "workout_id")
			if err != nil {
				return nil, err
			}
			w, err := t.s.q.GetUserWorkout(ctx, dbq.GetUserWorkoutParams{ID: workoutID, UserID: t.userID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, badInput("no workout %s in this user's log", workoutID)
			}
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(ed.Name) == "" {
				return nil, badInput("rename_workout needs a name")
			}
			base = append(base, &milov1.RowVersion{Table: "workout", Id: w.ID.String(), UpdatedAt: ts(w.UpdatedAt)})
			row := workoutRowProto(w, t.now)
			row.Name = strings.TrimSpace(ed.Name)
			changes = append(changes, change(row))
			lines = append(lines, &milov1.CardLine{Label: "Workout name", Before: w.Name, After: row.Name})
			if firstWorkout == uuid.Nil {
				firstWorkout = w.ID
			}
		case "update_exercise":
			exerciseID, err := parseUUID(ed.ExerciseID, "exercise_id")
			if err != nil {
				return nil, err
			}
			ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: exerciseID, UserID: &t.userID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, badInput("no exercise %s in this user's library", exerciseID)
			}
			if err != nil {
				return nil, err
			}
			if ex.UserID == nil {
				return nil, badInput("%s is built in and can't be edited; propose a new exercise instead", ex.Name)
			}
			base = append(base, &milov1.RowVersion{Table: "exercise", Id: ex.ID.String(), UpdatedAt: ts(ex.UpdatedAt)})
			row := exerciseRowProto(ex, t.now)
			if n := strings.TrimSpace(ed.Name); n != "" && n != ex.Name {
				lines = append(lines, &milov1.CardLine{Label: "Name", Before: ex.Name, After: n})
				row.Name = n
			}
			if m, ok := measurements[ed.Measurement]; ok && ed.Measurement != ex.Measurement {
				lines = append(lines, &milov1.CardLine{Label: "Counted", Before: countedText(ex.Measurement, ex.Unilateral), After: countedText(ed.Measurement, ex.Unilateral)})
				row.Measurement = m
			}
			if ed.Unilateral != nil && *ed.Unilateral != ex.Unilateral {
				lines = append(lines, &milov1.CardLine{Label: "One side at a time", Before: yesNo(ex.Unilateral), After: yesNo(*ed.Unilateral)})
				row.Unilateral = *ed.Unilateral
			}
			if ed.Equipment != "" && ed.Equipment != ex.Equipment {
				lines = append(lines, &milov1.CardLine{Label: "Equipment", Before: ex.Equipment, After: ed.Equipment})
				row.Equipment = ed.Equipment
			}
			if len(ed.PrimaryMuscles) > 0 {
				lines = append(lines, &milov1.CardLine{Label: "Works", Before: strings.Join(ex.PrimaryMuscles, ", "), After: strings.Join(ed.PrimaryMuscles, ", ")})
				row.PrimaryMuscles = ed.PrimaryMuscles
			}
			if ed.MET != nil {
				row.Met = ed.MET
			}
			if ed.Confirm && ex.Approximate {
				lines = append(lines, &milov1.CardLine{Label: "Status", Before: "Approximate", After: "Confirmed"})
				row.Approximate = false
			}
			changes = append(changes, change(row))
		default:
			return nil, badInput("unknown op %q", ed.Op)
		}
	}

	for _, weID := range weOrder {
		lines = append(lines, t.setLines(byWE[weID])...)
	}
	if len(changes) == 0 {
		return nil, badInput("these edits change nothing")
	}

	subtitle := in.Subtitle
	if subtitle == "" && firstWorkout != uuid.Nil {
		if w, err := t.s.q.GetUserWorkout(ctx, dbq.GetUserWorkoutParams{ID: firstWorkout, UserID: t.userID}); err == nil {
			subtitle = nameOr(w.Name, "Workout") + ", " + dayText(w.StartedAt.In(t.loc), t.now)
		}
	}
	card := &milov1.Card{
		Kind: milov1.CardKind_CARD_KIND_CHANGE, Title: in.Title, Subtitle: subtitle, Lines: lines,
		Changes: changes, Base: base, WorkoutId: idString(firstWorkout),
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": "The user sees the before/after card. Nothing changes until they tap Apply."}, nil
}

func idString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// setLines shows changed sets one by one, untouched ones as one line, and the exercise's volume before and after.
func (t *turn) setLines(e *exerciseEdits) []*milov1.CardLine {
	var lines []*milov1.CardLine
	var unchanged []int
	var beforeSets, afterSets []dbq.SetsForRow
	for i, s := range e.before {
		n := i + 1
		beforeSets = append(beforeSets, s)
		after, touched := e.after[s.ID]
		switch {
		case !touched:
			unchanged = append(unchanged, n)
			afterSets = append(afterSets, s)
		case after == nil:
			lines = append(lines, &milov1.CardLine{Label: fmt.Sprintf("Set %d", n), Before: setTextRow(e.measurement, s), After: "Removed"})
		default:
			afterSets = append(afterSets, *after)
			lines = append(lines, &milov1.CardLine{Label: fmt.Sprintf("Set %d", n), Before: setTextRow(e.measurement, s), After: setTextRow(e.measurement, *after)})
		}
	}
	for i, s := range e.added {
		afterSets = append(afterSets, s)
		lines = append(lines, &milov1.CardLine{Label: fmt.Sprintf("Set %d", len(e.before)+i+1), Before: "", After: setTextRow(e.measurement, s)})
	}
	if len(unchanged) > 0 {
		label := fmt.Sprintf("Set %d", unchanged[0])
		if len(unchanged) > 1 {
			label = fmt.Sprintf("Sets %d to %d", unchanged[0], unchanged[len(unchanged)-1])
			if unchanged[len(unchanged)-1]-unchanged[0] != len(unchanged)-1 {
				parts := make([]string, len(unchanged))
				for i, n := range unchanged {
					parts[i] = fmt.Sprint(n)
				}
				label = "Sets " + strings.Join(parts, ", ")
			}
		}
		text := "unchanged"
		if summary := exerciseSummary(e.measurement, pick(e.before, unchanged)); summary != "" && allSame(e.measurement, pick(e.before, unchanged)) {
			text = setTextRow(e.measurement, e.before[unchanged[0]-1]) + ", unchanged"
		}
		lines = append([]*milov1.CardLine{{Label: label, After: text}}, lines...)
	}
	before := maths.VolumeKg(setInputs(e.measurement, beforeSets))
	after := maths.VolumeKg(setInputs(e.measurement, afterSets))
	if e.measurement == maths.RepsLoad && before != after {
		lines = append(lines, &milov1.CardLine{Label: "Volume for this exercise", Before: thousands(int64(displayKg(before, t.displayUnit) + 0.5)), After: kgText(after, t.displayUnit)})
	}
	return lines
}

func displayKg(kg float64, unit string) float64 {
	if unit == "lb" {
		return kg / maths.KgPerLb
	}
	return kg
}

func pick(sets []dbq.SetsForRow, ns []int) []dbq.SetsForRow {
	out := make([]dbq.SetsForRow, 0, len(ns))
	for _, n := range ns {
		out = append(out, sets[n-1])
	}
	return out
}

func allSame(measurement string, sets []dbq.SetsForRow) bool {
	for _, s := range sets[1:] {
		if setTextRow(measurement, s) != setTextRow(measurement, sets[0]) {
			return false
		}
	}
	return true
}

func setRowProto(s dbq.SetsForRow, now time.Time) *milov1.Set {
	kinds := map[string]milov1.SetKind{"normal": milov1.SetKind_SET_KIND_NORMAL, "warmup": milov1.SetKind_SET_KIND_WARMUP, "drop": milov1.SetKind_SET_KIND_DROP, "failure": milov1.SetKind_SET_KIND_FAILURE}
	return &milov1.Set{
		Id: s.ID.String(), WorkoutExerciseId: s.WorkoutExerciseID.String(), Position: s.Position, Kind: kinds[s.Kind],
		Reps: s.Reps, Weight: trimNum(s.Weight), WeightUnit: unitProto(s.WeightUnit), PerSide: s.PerSide,
		DurationS: s.DurationS, DistanceM: s.DistanceM, Rpe: s.Rpe, Done: s.Done, UpdatedAt: ts(now),
	}
}

func workoutRowProto(w dbq.Workout, now time.Time) *milov1.Workout {
	row := &milov1.Workout{Id: w.ID.String(), Name: w.Name, Notes: w.Notes, StartedAt: ts(w.StartedAt), UpdatedAt: ts(now)}
	if w.EndedAt != nil {
		row.EndedAt = ts(*w.EndedAt)
	}
	if w.TemplateID != nil {
		row.TemplateId = w.TemplateID.String()
	}
	if w.HealthUuid != nil {
		row.HealthUuid = w.HealthUuid.String()
	}
	return row
}

func exerciseRowProto(e dbq.Exercise, now time.Time) *milov1.Exercise {
	return &milov1.Exercise{
		Id: e.ID.String(), Name: e.Name, Aliases: e.Aliases, PrimaryMuscles: e.PrimaryMuscles,
		SecondaryMuscles: e.SecondaryMuscles, MovementPattern: e.MovementPattern, Equipment: e.Equipment,
		Measurement: measurements[e.Measurement], Unilateral: e.Unilateral, Met: e.Met, Description: e.Description,
		Approximate: e.Approximate, UpdatedAt: ts(now),
	}
}

type templateItem struct {
	ExerciseID string `json:"exercise_id"`
	GroupLabel string `json:"group_label"`
}

func templateItems(raw []byte) []templateItem {
	var items []templateItem
	_ = json.Unmarshal(raw, &items)
	return items
}

func (t *turn) proposeTemplate(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
		Items []struct {
			ExerciseID string `json:"exercise_id"`
			GroupLabel string `json:"group_label"`
			Note       string `json:"note"`
		} `json:"items"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Items) == 0 {
		return nil, badInput("a template needs a name and at least one exercise")
	}
	tpl := &milov1.Template{Id: newID().String(), Name: strings.TrimSpace(in.Name), Notes: in.Notes, UpdatedAt: ts(t.now)}
	var lines []*milov1.CardLine
	groupCount := map[string]int{}
	for _, it := range in.Items {
		groupCount[it.GroupLabel]++
	}
	seen := map[string]int{}
	position := 0
	for _, it := range in.Items {
		id, err := parseUUID(it.ExerciseID, "exercise_id")
		if err != nil {
			return nil, err
		}
		ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: id, UserID: &t.userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, badInput("no exercise %s in this user's library", id)
		}
		if err != nil {
			return nil, err
		}
		tpl.Items = append(tpl.Items, &milov1.TemplateItem{ExerciseId: id.String(), GroupLabel: it.GroupLabel})
		label := ""
		if it.GroupLabel != "" && groupCount[it.GroupLabel] > 1 {
			if seen[it.GroupLabel] == 0 {
				position++
			}
			label = fmt.Sprintf("%d%c", position, 'a'+seen[it.GroupLabel])
			seen[it.GroupLabel]++
		} else {
			position++
			label = fmt.Sprint(position)
		}
		lines = append(lines, &milov1.CardLine{Label: label, After: ex.Name, Before: it.Note})
	}
	card := &milov1.Card{
		Kind: milov1.CardKind_CARD_KIND_TEMPLATE_DRAFT, Title: tpl.Name,
		Subtitle: fmt.Sprintf("Draft template, %d %s", len(tpl.Items), plural(len(tpl.Items), "exercise", "exercises")),
		Lines: lines, Changes: []*milov1.Change{change(tpl)},
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": "The user sees the draft and saves or edits it."}, nil
}

func (t *turn) showChart(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		ExerciseID string `json:"exercise_id"`
		Metric     string `json:"metric"`
		Caption    string `json:"caption"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	titles := map[string]string{"top_set": "top set", "volume": "volume", "e1rm": "estimated 1RM"}
	if titles[in.Metric] == "" {
		return nil, badInput("metric must be top_set, volume or e1rm")
	}
	ex, sessions, err := t.exerciseSessions(ctx, id, 60)
	if err != nil {
		return nil, err
	}
	body := t.bodyKg(ctx)
	chart := &milov1.Chart{Title: ex.Name + ", " + titles[in.Metric], Metric: in.Metric, Unit: unitProto(t.displayUnit), Caption: in.Caption}
	var best *milov1.ChartPoint
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		sum := maths.Summarize(setInputs(ex.Measurement, s.sets), body)
		var value float64
		var label string
		switch in.Metric {
		case "top_set":
			if !sum.HasTop {
				continue
			}
			value = displayKg(sum.TopLoadKg, t.displayUnit)
			label = fmt.Sprintf("%s %s × %d", maths.FormatTenths(value), t.displayUnit, sum.TopReps)
		case "volume":
			value = displayKg(sum.VolumeKg, t.displayUnit)
			label = kgText(sum.VolumeKg, t.displayUnit)
		case "e1rm":
			if !sum.HasE1RM {
				continue
			}
			value = displayKg(sum.E1RMKg, t.displayUnit)
			label = maths.FormatTenths(value) + " " + t.displayUnit
		}
		p := &milov1.ChartPoint{At: ts(s.at), Value: value, Label: label}
		chart.Points = append(chart.Points, p)
		if best == nil || p.Value > best.Value {
			best = p
		}
	}
	if len(chart.Points) == 0 {
		return nil, badInput("no %s data for %s yet", titles[in.Metric], ex.Name)
	}
	card := &milov1.Card{
		Kind: milov1.CardKind_CARD_KIND_CHART, Title: chart.Title, ExerciseId: id.String(), Chart: chart,
		Subtitle: best.Label + " on " + dayText(best.At.AsTime().In(t.loc), t.now),
	}
	cardID, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": cardID, "points": len(chart.Points), "best": card.Subtitle}, nil
}

func (t *turn) showWorkout(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		WorkoutID string `json:"workout_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseUUID(in.WorkoutID, "workout_id")
	if err != nil {
		return nil, err
	}
	w, err := t.s.q.GetUserWorkout(ctx, dbq.GetUserWorkoutParams{ID: id, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badInput("no workout %s in this user's log", id)
	}
	if err != nil {
		return nil, err
	}
	details, err := t.loadDetails(ctx, []dbq.Workout{w})
	if err != nil {
		return nil, err
	}
	d := details[0]
	sub := dayText(w.StartedAt.In(t.loc), t.now)
	if mins := minutesBetween(w.StartedAt, w.EndedAt); mins > 0 {
		sub += fmt.Sprintf(", %d min", mins)
	}
	sub += fmt.Sprintf(", %d sets", d.doneSets())
	lines := make([]*milov1.CardLine, 0, len(d.exercises))
	for _, e := range d.exercises {
		lines = append(lines, &milov1.CardLine{Label: e.row.ExerciseName, After: exerciseSummary(e.row.Measurement, e.sets)})
	}
	card := &milov1.Card{Kind: milov1.CardKind_CARD_KIND_WORKOUT, Title: nameOr(w.Name, "Workout"), Subtitle: sub, Lines: lines, WorkoutId: id.String()}
	cardID, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": cardID}, nil
}
