package agent_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// Each tool, driven the way the model drives it. The phone applies cards, so
// tests that span turns apply them through sync, as the phone would.

// call has Milo use tools in one message and returns what each was told back and the cards shown.
func call(t *testing.T, env *testkit.API, ctx context.Context, model *fakeModel, mctx *milo.MiloContext, uses ...toolUse) ([]string, []*milo.Card) {
	t.Helper()
	first := len(model.calls)
	for _, u := range uses {
		model.then(use(t, u.name, u.input))
	}
	model.then(say(t, "Done."))
	if mctx == nil {
		mctx = &milo.MiloContext{Timezone: "UTC"}
	}
	ev := send(t, env, ctx, "", "do it", mctx)
	if len(ev.errs) > 0 {
		t.Fatalf("turn failed: %v", ev.errs)
	}
	results := make([]string, len(uses))
	for i := range uses {
		results[i] = lastToolResult(t, model.calls[first+i+1])
	}
	return results, ev.cards
}

// userText is everything the model was sent as text in a request's last message.
func userText(t *testing.T, p anthropic.MessageNewParams) string {
	t.Helper()
	raw, _ := json.Marshal(p.Messages[len(p.Messages)-1])
	var m struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(raw, &m)
	var b strings.Builder
	for _, c := range m.Content {
		b.WriteString(c.Text + "\n")
	}
	return b.String()
}

// between is the text after the first start and before the next end.
func between(s, start, end string) string {
	_, rest, _ := strings.Cut(s, start)
	found, _, _ := strings.Cut(rest, end)
	return found
}

type toolUse struct {
	name  string
	input map[string]any
}

// apply does what the phone does with a card: writes its rows and syncs them.
func apply(t *testing.T, env *testkit.API, ctx context.Context, card *milo.Card) {
	t.Helper()
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: card.GetChanges()}); err != nil {
		t.Fatalf("apply %q: %v", card.GetTitle(), err)
	}
}

// rowsOf counts a card's changes by table, tombstones apart: "set", "set deleted".
func rowsOf(card *milo.Card) map[string]int {
	out := map[string]int{}
	add := func(table string, deleted bool) {
		if deleted {
			table += " deleted"
		}
		out[table]++
	}
	for _, c := range card.GetChanges() {
		switch r := c.GetRow().(type) {
		case *milo.Change_Workout:
			add("workout", r.Workout.GetDeleted())
		case *milo.Change_WorkoutExercise:
			add("workout_exercise", r.WorkoutExercise.GetDeleted())
		case *milo.Change_Set:
			add("set", r.Set.GetDeleted())
		case *milo.Change_Exercise:
			add("exercise", r.Exercise.GetDeleted())
		case *milo.Change_Template:
			add("template", r.Template.GetDeleted())
		case *milo.Change_BodyMeasurement:
			add("body_measurement", r.BodyMeasurement.GetDeleted())
		case *milo.Change_Settings:
			add("settings", false)
		case *milo.Change_ExercisePref:
			add("exercise_pref", r.ExercisePref.GetDeleted())
		}
	}
	return out
}

func wantRows(t *testing.T, card *milo.Card, want map[string]int) {
	t.Helper()
	got := rowsOf(card)
	for table, n := range want {
		if got[table] != n {
			t.Fatalf("%q changes %v, want %v", card.GetTitle(), got, want)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%q changes %v, want %v", card.GetTitle(), got, want)
	}
}

func linesOf(card *milo.Card) string {
	var out []string
	for _, l := range card.GetLines() {
		out = append(out, l.GetLabel()+"|"+l.GetBefore()+"|"+l.GetAfter())
	}
	return strings.Join(out, "\n")
}

// running syncs a workout in progress: bench with one ticked and one waiting set, and squat not started.
func running(t *testing.T, env *testkit.API, ctx context.Context) (workoutID, benchWE, squatWE uuid.UUID) {
	t.Helper()
	workoutID, benchWE, squatWE = uuid.New(), uuid.New(), uuid.New()
	at := timestamppb.New(time.Now().Add(-30 * time.Minute))
	set := func(we uuid.UUID, position int32, done bool) *milo.Change {
		return &milo.Change{Row: &milo.Change_Set{Set: &milo.Set{
			Id: uuid.NewString(), WorkoutExerciseId: we.String(), Position: position, Kind: milo.SetKind_SET_KIND_NORMAL,
			Reps: proto.Int32(8), Weight: "60", WeightUnit: milo.WeightUnit_WEIGHT_UNIT_KG, Done: done, UpdatedAt: at,
		}}}
	}
	we := func(id, exercise uuid.UUID, position int32) *milo.Change {
		return &milo.Change{Row: &milo.Change_WorkoutExercise{WorkoutExercise: &milo.WorkoutExercise{
			Id: id.String(), WorkoutId: workoutID.String(), ExerciseId: exercise.String(), Position: position, UpdatedAt: at,
		}}}
	}
	changes := []*milo.Change{
		{Row: &milo.Change_Workout{Workout: &milo.Workout{Id: workoutID.String(), Name: "Push day", StartedAt: at, UpdatedAt: at}}},
		we(benchWE, builtin(t, env, "Bench Press (Barbell)"), 1), set(benchWE, 1, true), set(benchWE, 2, false),
		we(squatWE, builtin(t, env, "Squat (Barbell)"), 2), set(squatWE, 1, false),
	}
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: changes}); err != nil {
		t.Fatal(err)
	}
	return workoutID, benchWE, squatWE
}

func TestStartWorkoutFillsInLastTimeAndLoggingTicksIt(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	shoulders(t, env, ctx) // six sets of landmine press at 20 kg × 15, finished
	press := builtin(t, env, "Single Arm Landmine Press (Barbell)")

	results, cards := call(t, env, ctx, model, nil,
		toolUse{"start_workout", map[string]any{"name": "Shoulders", "exercise_ids": []string{press.String()}}},
		toolUse{"log_sets", map[string]any{"exercise_id": press.String(), "sets": []map[string]any{{"reps": 12, "weight": "25"}}}},
	)
	if len(cards) != 2 {
		t.Fatalf("got %d cards; results %v", len(cards), results)
	}
	started, logged := cards[0], cards[1]
	if started.GetKind() != milo.CardKind_CARD_KIND_DONE || !started.GetAutoApply() || started.GetTitle() != "Started Shoulders" {
		t.Fatalf("start card = %v", started)
	}
	wantRows(t, started, map[string]int{"workout": 1, "workout_exercise": 1, "set": 6})
	var waiting []*milo.Set
	for _, c := range started.GetChanges() {
		if w := c.GetWorkout(); w != nil && w.GetEndedAt() != nil {
			t.Fatal("a started workout has an end time")
		}
		if s := c.GetSet(); s != nil {
			if s.GetDone() || s.GetWeight() != "20" || s.GetReps() != 15 {
				t.Fatalf("set from last time = %v, want 20 kg × 15 unticked", s)
			}
			waiting = append(waiting, s)
		}
	}
	// Logging in the same message ticks the first waiting set instead of adding a seventh.
	wantRows(t, logged, map[string]int{"set": 1})
	ticked := logged.GetChanges()[0].GetSet()
	if ticked.GetId() != waiting[0].GetId() || !ticked.GetDone() || ticked.GetWeight() != "25" || ticked.GetReps() != 12 {
		t.Fatalf("logged set = %v, want the first waiting set ticked at 25 × 12", ticked)
	}
	if logged.GetWorkoutId() != started.GetWorkoutId() || logged.GetLines()[0].GetLabel() != "Set 1" {
		t.Fatalf("logged into %q as %q", logged.GetWorkoutId(), logged.GetLines()[0].GetLabel())
	}
}

func TestOnlyOneWorkoutRuns(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, _, _ := running(t, env, ctx)

	results, cards := call(t, env, ctx, model, nil, toolUse{"start_workout", map[string]any{}})
	if len(cards) != 0 || !strings.HasPrefix(results[0], "ERROR") || !strings.Contains(results[0], workoutID.String()) {
		t.Fatalf("starting a second workout: cards %d, result %s", len(cards), results[0])
	}
	// The model is told about the running workout up front, too.
	if intro := userText(t, model.calls[0]); !strings.Contains(intro, `Running workout: "Push day"`) || !strings.Contains(intro, "1 of 2 sets ticked") {
		t.Fatalf("context doesn't describe the running workout:\n%s", intro)
	}
}

func TestLoggingWithNoIDUsesTheRunningWorkout(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, benchWE, _ := running(t, env, ctx)
	bench := builtin(t, env, "Bench Press (Barbell)")

	_, cards := call(t, env, ctx, model, &milo.MiloContext{Screen: "today", Timezone: "UTC"},
		toolUse{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []map[string]any{{"reps": 7, "weight": "62.5"}, {"reps": 6, "weight": "62.5"}}}})
	card := cards[0]
	if card.GetWorkoutId() != workoutID.String() {
		t.Fatalf("logged into %s, want the running workout", card.GetWorkoutId())
	}
	// One waiting set is ticked, one is added; no second bench is created.
	wantRows(t, card, map[string]int{"set": 2})
	if got := linesOf(card); got != "Set 2||62.5 kg × 7\nSet 3||62.5 kg × 6" {
		t.Fatalf("lines:\n%s", got)
	}
	for _, c := range card.GetChanges() {
		if c.GetSet().GetWorkoutExerciseId() != benchWE.String() {
			t.Fatal("sets went to a different exercise")
		}
	}
}

func TestFinishDropsWhatWasNeverTicked(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, _, squatWE := running(t, env, ctx)

	results, cards := call(t, env, ctx, model, nil, toolUse{"finish_workout", map[string]any{"notes": "Felt strong"}})
	if len(cards) != 1 {
		t.Fatalf("no card: %v", results)
	}
	card := cards[0]
	// Bench keeps its ticked set and loses the waiting one; squat was never started and goes entirely.
	wantRows(t, card, map[string]int{"workout": 1, "workout_exercise deleted": 1, "set deleted": 2})
	if !strings.HasPrefix(card.GetTitle(), "Finished Push day") || !strings.Contains(card.GetSubtitle(), "1 set, 480 kg") {
		t.Fatalf("card %q / %q", card.GetTitle(), card.GetSubtitle())
	}
	for _, c := range card.GetChanges() {
		if w := c.GetWorkout(); w != nil && (w.GetEndedAt() == nil || w.GetNotes() != "Felt strong" || w.GetId() != workoutID.String()) {
			t.Fatalf("workout = %v", w)
		}
		if we := c.GetWorkoutExercise(); we != nil && we.GetId() != squatWE.String() {
			t.Fatal("the wrong exercise was dropped")
		}
	}

	apply(t, env, ctx, card)
	results, _ = call(t, env, ctx, model, nil, toolUse{"finish_workout", map[string]any{}})
	if !strings.HasPrefix(results[0], "ERROR") {
		t.Fatalf("finished twice: %s", results[0])
	}
}

func TestProposeChangeEditsExercisesAndTimes(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, benchWE, squatWE := running(t, env, ctx)
	_, finished := call(t, env, ctx, model, nil,
		toolUse{"log_sets", map[string]any{"exercise_id": builtin(t, env, "Squat (Barbell)").String(), "sets": []map[string]any{{"reps": 5, "weight": "100"}}}},
	)
	apply(t, env, ctx, finished[0])
	_, finished = call(t, env, ctx, model, nil, toolUse{"finish_workout", map[string]any{}})
	apply(t, env, ctx, finished[0])
	incline := builtin(t, env, "Incline Bench Press (Barbell)")

	results, cards := call(t, env, ctx, model, nil, toolUse{"propose_change", map[string]any{
		"title":    "Push day",
		"workouts": []map[string]any{{"workout_id": workoutID.String(), "name": "Push A", "notes": "Shoulder felt fine"}},
		"exercises": []map[string]any{
			{"op": "update", "workout_exercise_id": squatWE.String(), "position": 1, "notes": "Belt on"},
			{"op": "update", "workout_exercise_id": benchWE.String(), "exercise_id": incline.String()},
		},
	}})
	if len(cards) != 1 {
		t.Fatalf("no card: %v", results)
	}
	card := cards[0]
	if card.GetAutoApply() || card.GetKind() != milo.CardKind_CARD_KIND_CHANGE || card.GetAppliedTitle() != "Push day updated" {
		t.Fatalf("card = %v", card)
	}
	wantRows(t, card, map[string]int{"workout": 1, "workout_exercise": 2})
	want := "Workout name|Push day|Push A\nNote|None|Shoulder felt fine\nNote on squat|None|Belt on\nSquat (Barbell)|2nd|1st\nExercise|Bench Press (Barbell)|Incline Bench Press (Barbell)"
	if got := linesOf(card); got != want {
		t.Fatalf("lines:\n%s\nwant:\n%s", got, want)
	}
	positions := map[string]int32{}
	for _, c := range card.GetChanges() {
		if we := c.GetWorkoutExercise(); we != nil {
			positions[we.GetId()] = we.GetPosition()
			if we.GetId() == benchWE.String() && we.GetExerciseId() != incline.String() {
				t.Fatal("bench wasn't swapped for incline")
			}
		}
	}
	if positions[squatWE.String()] != 1 || positions[benchWE.String()] != 2 {
		t.Fatalf("positions = %v", positions)
	}
	if len(card.GetBase()) != 3 {
		t.Fatalf("base has %d versions, want the workout and both exercises", len(card.GetBase()))
	}

	// Removing an exercise takes its sets with it.
	_, cards = call(t, env, ctx, model, nil, toolUse{"propose_change", map[string]any{
		"title": "Push day", "exercises": []map[string]any{{"op": "remove", "workout_exercise_id": squatWE.String()}},
	}})
	wantRows(t, cards[0], map[string]int{"workout_exercise deleted": 1, "set deleted": 1})
	if got := linesOf(cards[0]); got != "Squat (Barbell)|1 × 5 at 100 kg|Removed" {
		t.Fatalf("lines: %s", got)
	}

	// A workout can't end before it starts.
	results, _ = call(t, env, ctx, model, nil, toolUse{"propose_change", map[string]any{
		"title": "Push day", "workouts": []map[string]any{{"workout_id": workoutID.String(), "ended_at": "2020-01-01T10:00"}},
	}})
	if !strings.HasPrefix(results[0], "ERROR") {
		t.Fatalf("accepted an end before the start: %s", results[0])
	}
}

func TestProposeDeleteWorkout(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, _ := shoulders(t, env, ctx)

	_, cards := call(t, env, ctx, model, nil, toolUse{"propose_delete", map[string]any{"what": "workout", "id": workoutID.String()}})
	card := cards[0]
	if card.GetAutoApply() || !card.GetDestructive() || card.GetApplyLabel() != "Delete workout" || card.GetAppliedTitle() != "Deleted shoulders 6th oct" {
		t.Fatalf("card = %v", card)
	}
	wantRows(t, card, map[string]int{"workout deleted": 1, "workout_exercise deleted": 1, "set deleted": 6})
	// The workout is dated 6 Oct 2026: from 2027 its day carries the year.
	if sub := card.GetSubtitle(); !strings.HasPrefix(sub, "Tue 6 Oct") || !strings.HasSuffix(sub, ", 1 exercise, 6 sets") {
		t.Fatalf("subtitle = %q", card.GetSubtitle())
	}

	// Built-in exercises can't be deleted.
	results, _ := call(t, env, ctx, model, nil, toolUse{"propose_delete", map[string]any{"what": "exercise", "id": builtin(t, env, "Squat (Barbell)").String()}})
	if !strings.HasPrefix(results[0], "ERROR") {
		t.Fatalf("offered to delete a built-in exercise: %s", results[0])
	}
}

func TestTemplatesAreProposedChangedAndCarryNewExercises(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench, squat := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)")

	// A new exercise proposed in the same message can go straight into a template.
	model.then(
		use(t, "propose_new_exercise", map[string]any{
			"name": "Cable Y Raise", "primary_muscles": []string{"shoulders"}, "measurement": "reps_load", "unilateral": false, "met": 3.5,
			"works_summary": "Shoulders", "equipment_summary": "Cable", "movement_summary": "Raise",
		}),
		func(p anthropic.MessageNewParams) (*anthropic.Message, error) {
			id := between(lastToolResult(t, p), `exercise_id\":\"`, `\"`)
			return use(t, "propose_template", map[string]any{"name": "Upper", "items": []map[string]any{
				{"exercise_id": bench.String(), "group_label": "a"}, {"exercise_id": id, "group_label": "a"}, {"exercise_id": squat.String()},
			}})(p)
		},
		say(t, "Here's a draft."),
	)
	ev := send(t, env, ctx, "", "build me an upper day with cable y raises", &milo.MiloContext{Timezone: "UTC"})
	if len(ev.cards) != 2 {
		t.Fatalf("got %d cards, errors %v", len(ev.cards), ev.errs)
	}
	draft := ev.cards[1]
	wantRows(t, draft, map[string]int{"exercise": 1, "template": 1})
	if got := linesOf(draft); got != "1a||Bench Press (Barbell)\n1b||Cable Y Raise\n2||Squat (Barbell)" {
		t.Fatalf("lines:\n%s", got)
	}
	apply(t, env, ctx, draft)
	var templateID string
	for _, c := range draft.GetChanges() {
		if tpl := c.GetTemplate(); tpl != nil {
			templateID = tpl.GetId()
		}
	}

	// Changing it keeps the id, carries the stored version, and marks what's new.
	deadlift := builtin(t, env, "Deadlift (Barbell)")
	_, cards := call(t, env, ctx, model, nil, toolUse{"propose_template", map[string]any{
		"template_id": templateID, "name": "Upper A",
		"items": []map[string]any{{"exercise_id": bench.String()}, {"exercise_id": deadlift.String()}},
	}})
	card := cards[0]
	if len(card.GetBase()) != 1 || card.GetApplyLabel() != "Save changes" || card.GetChanges()[0].GetTemplate().GetId() != templateID {
		t.Fatalf("card = %v", card)
	}
	if got := linesOf(card); got != "1||Bench Press (Barbell)\n2|new|Deadlift (Barbell)" {
		t.Fatalf("lines:\n%s", got)
	}

	// Starting from the template brings its exercises in order.
	_, cards = call(t, env, ctx, model, nil, toolUse{"start_workout", map[string]any{"template_id": templateID}})
	wantRows(t, cards[0], map[string]int{"workout": 1, "workout_exercise": 3, "set": 3})
	if cards[0].GetTitle() != "Started Upper" {
		t.Fatalf("title = %q", cards[0].GetTitle())
	}
}

func TestMergeExerciseMovesItsHistory(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID := openWorkout(t, env, ctx)
	bench := builtin(t, env, "Bench Press (Barbell)")

	_, cards := call(t, env, ctx, model, &milo.MiloContext{WorkoutId: workoutID.String(), Timezone: "UTC"}, toolUse{"propose_new_exercise", map[string]any{
		"name": "Flat Barbell Press", "primary_muscles": []string{"chest"}, "measurement": "reps_load", "unilateral": false, "met": 5,
		"works_summary": "Chest", "equipment_summary": "Barbell", "movement_summary": "Press",
		"sets": []map[string]any{{"reps": 8, "weight": "60"}},
	}})
	apply(t, env, ctx, cards[0])
	mine := cards[0].GetExerciseId()
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: []*milo.Change{{Row: &milo.Change_Template{Template: &milo.Template{
		Id: uuid.NewString(), Name: "Push", Items: []*milo.TemplateItem{{ExerciseId: bench.String()}, {ExerciseId: mine}}, UpdatedAt: timestamppb.Now(),
	}}}}}); err != nil {
		t.Fatal(err)
	}

	_, cards = call(t, env, ctx, model, nil, toolUse{"propose_exercise_change", map[string]any{"exercise_id": mine, "merge_into": bench.String()}})
	card := cards[0]
	wantRows(t, card, map[string]int{"workout_exercise": 1, "template": 1, "exercise deleted": 1})
	if card.GetChanges()[0].GetWorkoutExercise().GetExerciseId() != bench.String() || card.GetApplyLabel() != "Merge" {
		t.Fatalf("card = %v", card)
	}
	if got := linesOf(card); got != "Workouts||1 session moves to bench press\nTemplates||1 template updated\nFlat Barbell Press|Yours|Removed" {
		t.Fatalf("lines:\n%s", got)
	}
}

// Changing an exercise of your own shows each change as before and after.
func TestChangingYourOwnExerciseShowsEachChange(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	id := uuid.New()
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: []*milo.Change{{Row: &milo.Change_Exercise{Exercise: &milo.Exercise{
		Id: id.String(), Name: "Landmine Rotation", Measurement: milo.Measurement_MEASUREMENT_REPS_LOAD, PrimaryMuscles: []string{"obliques"},
		Approximate: true, UpdatedAt: timestamppb.Now(),
	}}}}}); err != nil {
		t.Fatal(err)
	}

	_, cards := call(t, env, ctx, model, nil, toolUse{"propose_exercise_change", map[string]any{
		"exercise_id": id.String(), "name": "Landmine Twist", "measurement": "timed", "unilateral": true, "equipment": "barbell",
		"primary_muscles": []string{"obliques", "abs"}, "met": 4.5, "confirm": true,
	}})
	card := cards[0]
	want := "Name|Landmine Rotation|Landmine Twist\nCounted|Reps at a weight|Time\nOne side at a time|No|Yes\nEquipment|None|barbell\nWorks|obliques|obliques, abs\nStatus|Approximate|Confirmed"
	if got := linesOf(card); got != want {
		t.Fatalf("lines:\n%s\nwant:\n%s", got, want)
	}
	e := card.GetChanges()[0].GetExercise()
	if card.GetKind() != milo.CardKind_CARD_KIND_CHANGE || card.GetAppliedTitle() != "Landmine Twist updated" || e.GetApproximate() || e.GetMet() != 4.5 || len(card.GetBase()) != 1 {
		t.Fatalf("card %v", card)
	}
}

func TestBodySettingsAndPreferencesApplyStraightAway(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	curl := builtin(t, env, "Bicep Curl (Dumbbell)")

	_, cards := call(t, env, ctx, model, nil,
		toolUse{"log_body", map[string]any{"weight": "180", "unit": "lb"}},
		toolUse{"set_exercise_preferences", map[string]any{"exercise_id": curl.String(), "unit": "lb", "rest_s": 45}},
		toolUse{"update_settings", map[string]any{"display_unit": "lb", "rest_default_s": 90}},
	)
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	for _, c := range cards {
		if c.GetKind() != milo.CardKind_CARD_KIND_DONE || !c.GetAutoApply() {
			t.Fatalf("%q isn't applied straight away", c.GetTitle())
		}
	}
	if kg := cards[0].GetChanges()[0].GetBodyMeasurement().GetWeightKg(); kg < 81.6 || kg > 81.7 || cards[0].GetTitle() != "Logged 81.6 kg" {
		t.Fatalf("reading %v kg, title %q", kg, cards[0].GetTitle())
	}
	st := cards[2].GetChanges()[0].GetSettings()
	if st.GetDisplayUnit() != milo.WeightUnit_WEIGHT_UNIT_LB || st.GetRestDefaultS() != 90 || cards[2].GetSubtitle() != "Totals and charts in pounds, Rest timer 1:30" {
		t.Fatalf("settings %v, subtitle %q", st, cards[2].GetSubtitle())
	}
	pref := cards[1].GetChanges()[0].GetExercisePref()
	if pref.GetUnit() != milo.WeightUnit_WEIGHT_UNIT_LB || pref.GetRestS() != 45 || cards[1].GetSubtitle() != "Logged in pounds, rest 45 s" {
		t.Fatalf("pref %v, subtitle %q", pref, cards[1].GetSubtitle())
	}

	apply(t, env, ctx, cards[0])
	results, cards := call(t, env, ctx, model, nil, toolUse{"get_profile", map[string]any{}}, toolUse{"show_chart", map[string]any{"metric": "body_weight"}})
	if !strings.Contains(results[0], `weight\":\"81.6 kg`) || len(cards) != 1 || len(cards[0].GetChart().GetPoints()) != 1 {
		t.Fatalf("profile %s, cards %d", results[0], len(cards))
	}
}

func TestTrainingSummaryCountsWorkingSets(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench := builtin(t, env, "Bench Press (Barbell)")

	_, cards := call(t, env, ctx, model, nil, toolUse{"log_sets", map[string]any{
		"exercise_id": bench.String(), "new_workout": map[string]any{"name": "Push", "started_at": time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02T15:04")},
		"sets": []map[string]any{{"reps": 10, "weight": "40", "kind": "warmup"}, {"reps": 8, "weight": "60"}, {"reps": 8, "weight": "60"}},
	}})
	if got := cards[0].GetLines()[0].GetAfter(); got != "Warm-up, 40 kg × 10" {
		t.Fatalf("warm-up line = %q", got)
	}
	apply(t, env, ctx, cards[0])

	results, _ := call(t, env, ctx, model, nil, toolUse{"training_summary", map[string]any{}})
	for _, want := range []string{`"workouts":1`, `"volume":"960 kg"`, `"muscle":"chest","sets":2`, `"name":"Bench Press (Barbell)","sessions":1,"sets":2`} {
		if !strings.Contains(strings.ReplaceAll(results[0], `\"`, `"`), want) {
			t.Fatalf("summary is missing %s:\n%s", want, results[0])
		}
	}
}

func TestAddingExercisesToTheRunningWorkout(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, _, _ := running(t, env, ctx)
	dip, curl, hammer := builtin(t, env, "Chest Dip"), builtin(t, env, "Bicep Curl (Dumbbell)"), builtin(t, env, "Hammer Curl (Dumbbell)")

	_, cards := call(t, env, ctx, model, nil,
		toolUse{"add_exercises", map[string]any{"exercise_ids": []string{dip.String()}}},
		toolUse{"add_exercises", map[string]any{"exercise_ids": []string{curl.String(), hammer.String()}, "superset": true}},
	)
	one, pair := cards[0], cards[1]
	if one.GetTitle() != "Added Chest Dip" || one.GetSubtitle() != "To Push day" || one.GetWorkoutId() != workoutID.String() || !one.GetAutoApply() {
		t.Fatalf("one exercise: %q / %q", one.GetTitle(), one.GetSubtitle())
	}
	if pair.GetTitle() != "Added 2 exercises as a superset" || pair.GetSubtitle() != "Bicep Curl (Dumbbell), Hammer Curl (Dumbbell)" {
		t.Fatalf("superset: %q / %q", pair.GetTitle(), pair.GetSubtitle())
	}
	// After bench and squat, in order; the pair share a superset group, the dip has none.
	var positions []int32
	groups := map[string]string{}
	for _, c := range append(one.GetChanges(), pair.GetChanges()...) {
		if we := c.GetWorkoutExercise(); we != nil {
			positions = append(positions, we.GetPosition())
			groups[we.GetExerciseId()] = we.GetGroupLabel()
		}
	}
	if !slices.Equal(positions, []int32{3, 4, 5}) || groups[dip.String()] != "" || groups[curl.String()] == "" || groups[curl.String()] != groups[hammer.String()] {
		t.Fatalf("positions %v, groups %v", positions, groups)
	}
}

func TestDoingAPastWorkoutAgainOrStartingFromATemplate(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	press := builtin(t, env, "Single Arm Landmine Press (Barbell)")
	templateID, gone := uuid.New(), uuid.New()
	at := timestamppb.New(time.Now().Add(-48 * time.Hour))
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: []*milo.Change{{Row: &milo.Change_Template{Template: &milo.Template{
		Id: templateID.String(), Name: "Shoulders", UpdatedAt: at,
		// An exercise the user has since deleted is skipped, not an error.
		Items: []*milo.TemplateItem{{ExerciseId: press.String()}, {ExerciseId: gone.String()}},
	}}}}}); err != nil {
		t.Fatal(err)
	}
	past, _ := shoulders(t, env, ctx)
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: []*milo.Change{{Row: &milo.Change_Workout{Workout: &milo.Workout{
		Id: past.String(), Name: "shoulders 6th oct", TemplateId: templateID.String(),
		StartedAt: timestamppb.New(time.Date(2026, 10, 6, 7, 0, 0, 0, time.UTC)), EndedAt: timestamppb.New(time.Date(2026, 10, 6, 8, 10, 0, 0, time.UTC)), UpdatedAt: at,
	}}}}}); err != nil {
		t.Fatal(err)
	}

	_, cards := call(t, env, ctx, model, nil, toolUse{"start_workout", map[string]any{"copy_workout_id": past.String()}})
	again := cards[0]
	if again.GetTitle() != "Started shoulders 6th oct" || again.GetSubtitle() != "1 exercise, sets filled in from last time" {
		t.Fatalf("again: %q / %q", again.GetTitle(), again.GetSubtitle())
	}
	if w := again.GetChanges()[0].GetWorkout(); w.GetTemplateId() != templateID.String() || w.GetEndedAt() != nil {
		t.Fatalf("copied workout %v, want it running and keeping its template", w)
	}

	// That card wasn't applied, so nothing is running yet: start again from the template.
	_, cards = call(t, env, ctx, model, nil, toolUse{"start_workout", map[string]any{"template_id": templateID.String()}})
	if fresh := cards[0]; fresh.GetTitle() != "Started Shoulders" || fresh.GetSubtitle() != "1 exercise, sets filled in from last time" {
		t.Fatalf("from a template: %q / %q", fresh.GetTitle(), fresh.GetSubtitle())
	}
}

func TestFinishingAWorkout(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")

	// A workout started in this message can't be finished in it, and one with nothing ticked isn't saved.
	results, cards := call(t, env, ctx, model, nil, toolUse{"start_workout", map[string]any{}}, toolUse{"finish_workout", map[string]any{}})
	if !strings.Contains(results[1], "only just started in this message") {
		t.Fatalf("finish in the same message: %s", results[1])
	}
	apply(t, env, ctx, cards[0])
	results, _ = call(t, env, ctx, model, nil, toolUse{"finish_workout", map[string]any{}})
	if !strings.Contains(results[0], "no sets are ticked") || !strings.Contains(results[0], "propose_delete") {
		t.Fatalf("finish with nothing ticked: %s", results[0])
	}

	// Stopped twenty minutes ago, and renamed on the way out.
	octx, _ := env.SignUp(t, "someone")
	running(t, env, octx)
	ended := time.Now().UTC().Add(-10 * time.Minute)
	_, cards = call(t, env, octx, model, nil, toolUse{"finish_workout", map[string]any{"ended_at": ended.Format("2006-01-02T15:04"), "name": "Chest"}})
	card := cards[0]
	w := card.GetChanges()[len(card.GetChanges())-1].GetWorkout()
	// The length depends on the second it ran, so only the end and the sets are checked.
	if card.GetTitle() != "Finished Chest" || !strings.HasSuffix(card.GetSubtitle(), " min, 1 set, 480 kg") || w.GetEndedAt().AsTime().Format("15:04") != ended.Format("15:04") {
		t.Fatalf("%q / %q, ended %v", card.GetTitle(), card.GetSubtitle(), w.GetEndedAt().AsTime())
	}
}

func TestSetsOfEveryKindAreLoggedAsSaid(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench := builtin(t, env, "Bench Press (Barbell)")

	_, cards := call(t, env, ctx, model, nil, toolUse{"log_sets", map[string]any{
		"exercise_id": bench.String(),
		"new_workout": map[string]any{"name": "Push", "started_at": "2026-11-01T10:00", "ended_at": "2026-11-01T10:45"},
		"sets":        []map[string]any{{"reps": 8, "weight": "60"}, {"reps": 10, "weight": "50", "kind": "drop"}, {"reps": 12, "weight": "40", "kind": "failure"}},
	}})
	card := cards[0]
	if got := linesOf(card); got != "Set 1||60 kg × 8\nSet 2||Drop set, 50 kg × 10\nSet 3||To failure, 40 kg × 12" {
		t.Fatalf("lines:\n%s", got)
	}
	// A session that already happened says which, since it isn't on screen. (After 2026 the year follows.)
	if !strings.HasPrefix(card.GetSubtitle(), "Bench Press (Barbell), Push Sun 1 Nov") {
		t.Fatalf("subtitle %q", card.GetSubtitle())
	}
	if w := card.GetChanges()[0].GetWorkout(); w.GetEndedAt().AsTime() != time.Date(2026, 11, 1, 10, 45, 0, 0, time.UTC) {
		t.Fatalf("workout %v", w)
	}
}

func TestSettingsChangeAndUndoAsSaid(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	curl := builtin(t, env, "Bicep Curl (Dumbbell)")

	_, cards := call(t, env, ctx, model, nil,
		toolUse{"log_body", map[string]any{"body_fat_pct": 18.5, "measured_at": "2026-11-01T07:30"}},
		toolUse{"update_settings", map[string]any{"height_cm": 180}},
		toolUse{"update_settings", map[string]any{"birth_date": "1995-04-02", "sex": "male"}},
		toolUse{"set_exercise_preferences", map[string]any{"exercise_id": curl.String(), "rest_s": 120}},
	)
	fat, height, profile, rest := cards[0], cards[1], cards[2], cards[3]
	if fat.GetTitle() != "Logged 18.5% body fat" || !strings.HasPrefix(fat.GetSubtitle(), "Body reading, Sun 1 Nov") {
		t.Fatalf("reading: %q / %q", fat.GetTitle(), fat.GetSubtitle())
	}
	// One change is the title; several are listed under "Settings updated".
	if height.GetTitle() != "Height 180 cm" || profile.GetTitle() != "Settings updated" || profile.GetSubtitle() != "Date of birth 1995-04-02, Sex male" {
		t.Fatalf("settings: %q, %q / %q", height.GetTitle(), profile.GetTitle(), profile.GetSubtitle())
	}
	for _, c := range cards[1:] {
		apply(t, env, ctx, c)
	}

	_, cards = call(t, env, ctx, model, nil,
		toolUse{"update_settings", map[string]any{"birth_date": "", "sex": ""}},
		toolUse{"set_exercise_preferences", map[string]any{"exercise_id": curl.String(), "rest_s": 0}},
	)
	if cards[0].GetSubtitle() != "Date of birth None, Sex None" {
		t.Fatalf("clearing: %q", cards[0].GetSubtitle())
	}
	if pref := cards[1].GetChanges()[0].GetExercisePref(); pref.RestS != nil || cards[1].GetSubtitle() != "Rest back to the default" || rest.GetSubtitle() != "Rest 2 min" {
		t.Fatalf("rest: %v, %q after %q", pref, cards[1].GetSubtitle(), rest.GetSubtitle())
	}
}

// Fixing a set changes only the fields that were said, two edits to one set build on
// each other, and an added set follows on from the last one.
func TestFixingSetsChangesOnlyWhatWasSaid(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	_, setIDs := shoulders(t, env, ctx) // six sets of 20 kg × 15, ticked
	var weID uuid.UUID
	if err := env.Pool.QueryRow(context.Background(), "SELECT workout_exercise_id FROM sets WHERE id = $1", setIDs[0]).Scan(&weID); err != nil {
		t.Fatal(err)
	}

	_, cards := call(t, env, ctx, model, nil, toolUse{"propose_change", map[string]any{"title": "Fix shoulders", "sets": []map[string]any{
		{"op": "update", "set_id": setIDs[0].String(), "kind": "warmup", "per_side": true},
		{"op": "update", "set_id": setIDs[0].String(), "reps": 12, "unit": "lb", "done": false},
		{"op": "add", "workout_exercise_id": weID.String(), "weight": "25", "reps": 10, "duration_s": 40, "distance_m": 5.5},
	}}})
	card := cards[0]
	if len(card.GetBase()) != 1 || card.GetBase()[0].GetId() != setIDs[0].String() {
		t.Fatalf("base %v, want the one edited set once", card.GetBase())
	}
	var edited, added *milo.Set
	for _, c := range card.GetChanges() {
		if s := c.GetSet(); s.GetId() == setIDs[0].String() {
			edited = s
		} else if s != nil {
			added = s
		}
	}
	if edited.GetWeight() != "20" || edited.GetReps() != 12 || edited.GetWeightUnit() != milo.WeightUnit_WEIGHT_UNIT_LB ||
		!edited.GetPerSide() || edited.GetDone() || edited.GetKind() != milo.SetKind_SET_KIND_WARMUP {
		t.Fatalf("edited set %v", edited)
	}
	if added.GetPosition() != 7 || added.GetWeight() != "25" || added.GetReps() != 10 || !added.GetDone() ||
		added.GetDurationS() != 40 || added.GetDistanceM() != 5.5 || added.GetWeightUnit() != milo.WeightUnit_WEIGHT_UNIT_KG {
		t.Fatalf("added set %v", added)
	}
}
