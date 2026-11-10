package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// fakeModel plays back scripted replies and records every request.
type fakeModel struct {
	mu    sync.Mutex
	steps []func(anthropic.MessageNewParams) (*anthropic.Message, error)
	calls []anthropic.MessageNewParams
}

func (f *fakeModel) then(steps ...func(anthropic.MessageNewParams) (*anthropic.Message, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, steps...)
}

func (f *fakeModel) Stream(_ context.Context, p anthropic.MessageNewParams, onText func(string)) (*anthropic.Message, error) {
	f.mu.Lock()
	f.calls = append(f.calls, p)
	if len(f.steps) == 0 {
		f.mu.Unlock()
		return nil, errors.New("fake model: no more steps")
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	f.mu.Unlock()
	msg, err := step(p)
	if msg != nil {
		for _, b := range msg.Content {
			if tb, ok := b.AsAny().(anthropic.TextBlock); ok {
				onText(tb.Text)
			}
		}
	}
	return msg, err
}

func message(t *testing.T, stop string, content ...map[string]any) *anthropic.Message {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"id": "msg_" + uuid.NewString(), "type": "message", "role": "assistant", "model": "claude-opus-5-5",
		"content": content, "stop_reason": stop, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20},
	})
	var m anthropic.Message
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

func say(t *testing.T, text string) func(anthropic.MessageNewParams) (*anthropic.Message, error) {
	return func(anthropic.MessageNewParams) (*anthropic.Message, error) {
		return message(t, "end_turn", map[string]any{"type": "text", "text": text}), nil
	}
}

func use(t *testing.T, name string, input any) func(anthropic.MessageNewParams) (*anthropic.Message, error) {
	return func(anthropic.MessageNewParams) (*anthropic.Message, error) {
		return message(t, "tool_use", map[string]any{"type": "tool_use", "id": "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", ""), "name": name, "input": input}), nil
	}
}

func fail() func(anthropic.MessageNewParams) (*anthropic.Message, error) {
	return func(anthropic.MessageNewParams) (*anthropic.Message, error) {
		return nil, errors.New("connection reset")
	}
}

// lastToolResult is the most recent tool_result the model was sent, as text.
func lastToolResult(t *testing.T, p anthropic.MessageNewParams) string {
	t.Helper()
	raw, _ := json.Marshal(p.Messages[len(p.Messages)-1])
	var m struct {
		Content []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Content any    `json:"content"`
		} `json:"content"`
	}
	_ = json.Unmarshal(raw, &m)
	for _, c := range m.Content {
		if c.Type == "tool_result" {
			b, _ := json.Marshal(c.Content)
			if c.IsError {
				return "ERROR: " + string(b)
			}
			return string(b)
		}
	}
	return ""
}

type events struct {
	text     string
	readings []string
	cards    []*milo.Card
	errs     []*milo.MiloError
	done     string
}

func send(t *testing.T, env *testkit.API, ctx context.Context, convID, text string, mctx *milo.MiloContext) events {
	t.Helper()
	stream, err := env.Milo.Send(ctx, &milo.SendRequest{ConversationId: convID, Text: text, Context: mctx})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var ev events
	for {
		res, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return ev
		}
		if err != nil {
			t.Fatal(err)
		}
		switch e := res.GetEvent().(type) {
		case *milo.SendResponse_TextDelta:
			ev.text += e.TextDelta.GetText()
		case *milo.SendResponse_Reading:
			ev.readings = append(ev.readings, e.Reading.GetSummary())
		case *milo.SendResponse_Card:
			ev.cards = append(ev.cards, e.Card)
		case *milo.SendResponse_Error:
			ev.errs = append(ev.errs, e.Error)
		case *milo.SendResponse_Done:
			ev.done = e.Done.GetConversationId()
		}
	}
}

func builtin(t *testing.T, env *testkit.API, name string) uuid.UUID {
	t.Helper()
	e, err := query.New(env.Pool).GetBuiltinExerciseByName(context.Background(), name)
	if err != nil {
		t.Fatalf("built-in %q: %v", name, err)
	}
	return e.ID
}

// shoulders logs the 6 Oct workout from the design: six identical sets of landmine press at 20 kg × 15.
func shoulders(t *testing.T, env *testkit.API, ctx context.Context) (workoutID uuid.UUID, setIDs []uuid.UUID) {
	t.Helper()
	exerciseID := builtin(t, env, "Single Arm Landmine Press (Barbell)")
	workoutID, weID := uuid.New(), uuid.New()
	start := time.Date(2026, 10, 6, 7, 0, 0, 0, time.UTC)
	end := start.Add(70 * time.Minute)
	changes := []*milo.Change{
		{Row: &milo.Change_Workout{Workout: &milo.Workout{Id: workoutID.String(), Name: "shoulders 6th oct", StartedAt: timestamppb.New(start), EndedAt: timestamppb.New(end), UpdatedAt: timestamppb.New(start)}}},
		{Row: &milo.Change_WorkoutExercise{WorkoutExercise: &milo.WorkoutExercise{Id: weID.String(), WorkoutId: workoutID.String(), ExerciseId: exerciseID.String(), Position: 1, UpdatedAt: timestamppb.New(start)}}},
	}
	for i := range 6 {
		id := uuid.New()
		setIDs = append(setIDs, id)
		changes = append(changes, &milo.Change{Row: &milo.Change_Set{Set: &milo.Set{
			Id: id.String(), WorkoutExerciseId: weID.String(), Position: int32(i + 1), Kind: milo.SetKind_SET_KIND_NORMAL,
			Reps: proto.Int32(15), Weight: "20", WeightUnit: milo.WeightUnit_WEIGHT_UNIT_KG, Done: true, UpdatedAt: timestamppb.New(start),
		}}})
	}
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: changes}); err != nil {
		t.Fatal(err)
	}
	return workoutID, setIDs
}

func openWorkout(t *testing.T, env *testkit.API, ctx context.Context) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now()
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: []*milo.Change{
		{Row: &milo.Change_Workout{Workout: &milo.Workout{Id: id.String(), Name: "Tuesday workout", StartedAt: timestamppb.New(now.Add(-20 * time.Minute)), UpdatedAt: timestamppb.New(now)}}},
	}}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLogSetsCardAutoApplies(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID := openWorkout(t, env, ctx)
	bench := builtin(t, env, "Bench Press (Barbell)")

	model.then(
		use(t, "log_sets", map[string]any{"exercise_id": bench.String(), "sets": []map[string]any{{"reps": 10, "weight": "60", "unit": "kg"}, {"reps": 8, "weight": "62.5", "unit": "kg"}}}),
		say(t, "Logged two sets of bench."),
	)
	ev := send(t, env, ctx, "", "bench 60 for 10 then 62.5 for 8", &milo.MiloContext{Screen: "workout", WorkoutId: workoutID.String(), Timezone: "Asia/Kolkata"})

	if len(ev.errs) > 0 {
		t.Fatalf("errors: %v", ev.errs)
	}
	if len(ev.cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(ev.cards))
	}
	card := ev.cards[0]
	if card.GetKind() != milo.CardKind_CARD_KIND_DONE || !card.GetAutoApply() || card.GetWorkoutId() != workoutID.String() {
		t.Fatalf("card = %v", card)
	}
	var we, sets int
	for _, c := range card.GetChanges() {
		if c.GetWorkoutExercise() != nil {
			we++
		}
		if s := c.GetSet(); s != nil {
			sets++
			if !s.GetDone() {
				t.Fatal("logged set isn't done")
			}
		}
	}
	if we != 1 || sets != 2 {
		t.Fatalf("changes: %d workout exercises and %d sets, want 1 and 2", we, sets)
	}
	if card.GetLines()[1].GetAfter() != "62.5 kg × 8" {
		t.Fatalf("line = %q", card.GetLines()[1].GetAfter())
	}
	if ev.text != "Logged two sets of bench." || ev.done == "" {
		t.Fatalf("text %q, done %q", ev.text, ev.done)
	}
	// Nothing was written to the user's log by Milo itself.
	var n int
	_ = env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM sets").Scan(&n)
	if n != 0 {
		t.Fatalf("Milo wrote %d sets to the database; the phone applies cards", n)
	}
}

func TestProposeNewExerciseIsApproximate(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID := openWorkout(t, env, ctx)

	model.then(
		use(t, "search_exercises", map[string]any{"query": "landmine rotation"}),
		use(t, "propose_new_exercise", map[string]any{
			"name": "Landmine Rotation", "aliases": []string{"landmine twist"}, "primary_muscles": []string{"obliques", "abs"},
			"secondary_muscles": []string{"front delts"}, "movement_pattern": "rotation", "equipment": "landmine",
			"measurement": "reps_load", "unilateral": false, "met": 4.5,
			"works_summary": "Obliques and core, some shoulders", "equipment_summary": "Barbell in a landmine", "movement_summary": "Rotation, standing",
			"sets": []map[string]any{{"reps": 12, "weight": "20", "unit": "kg", "per_side": true}},
		}),
		say(t, "Not in your exercises yet. Here's my read of it."),
	)
	ev := send(t, env, ctx, "", "landmine rotation, barbell in the corner, twist side to side. 20 kg, 12 each side", &milo.MiloContext{Screen: "workout", WorkoutId: workoutID.String()})

	if len(model.calls) != 3 {
		t.Fatalf("model called %d times, want 3", len(model.calls))
	}
	if res := lastToolResult(t, model.calls[1]); strings.Contains(res, "Landmine Rotation") || strings.HasPrefix(res, "ERROR") {
		t.Fatalf("search should find no Landmine Rotation built in, got %s", res)
	}
	if len(ev.cards) != 1 {
		t.Fatalf("got %d cards", len(ev.cards))
	}
	card := ev.cards[0]
	var ex *milo.Exercise
	var set *milo.Set
	for _, c := range card.GetChanges() {
		if c.GetExercise() != nil {
			ex = c.GetExercise()
		}
		if c.GetSet() != nil {
			set = c.GetSet()
		}
	}
	if ex == nil || !ex.GetApproximate() || !ex.GetUnilateral() || ex.GetBuiltin() {
		t.Fatalf("exercise = %v", ex)
	}
	if set == nil || !set.GetPerSide() || set.GetWeight() != "20" {
		t.Fatalf("set = %v", set)
	}
	want := map[string]string{"Works": "Obliques and core, some shoulders", "Counted": "Reps each side", "Set 1": "20 kg × 12 each side"}
	for _, l := range card.GetLines() {
		if w, ok := want[l.GetLabel()]; ok && l.GetAfter() != w {
			t.Fatalf("%s = %q, want %q", l.GetLabel(), l.GetAfter(), w)
		}
	}
	if card.GetWorkoutId() != workoutID.String() || card.GetNote() == "" {
		t.Fatalf("card workout %q note %q", card.GetWorkoutId(), card.GetNote())
	}
}

func TestProposeChangeCarriesBaseVersions(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, setIDs := shoulders(t, env, ctx)

	model.then(
		use(t, "get_workouts", map[string]any{"date": "2026-10-06"}),
		say(t, "The landmine press is down as six sets of 15 at 20 kg, all identical. What should it be?"),
	)
	ev := send(t, env, ctx, "", "What did I do for shoulders on 6 October? I think the landmine press is logged wrong.", &milo.MiloContext{Screen: "today", Timezone: "UTC"})
	if len(ev.readings) != 1 || ev.readings[0] != "Read shoulders 6th oct, Tue 6 Oct" {
		t.Fatalf("readings = %v", ev.readings)
	}
	if res := lastToolResult(t, model.calls[1]); !strings.Contains(res, setIDs[3].String()) || !strings.Contains(res, "20 kg × 15") {
		t.Fatalf("get_workouts result missing sets: %s", res)
	}

	model.then(
		use(t, "propose_change", map[string]any{
			"title": "Single Arm Landmine Press",
			"sets": []map[string]any{
				{"op": "update", "set_id": setIDs[3].String(), "weight": "25"},
				{"op": "update", "set_id": setIDs[4].String(), "weight": "25"},
				{"op": "delete", "set_id": setIDs[5].String()},
			},
		}),
		say(t, "Here is exactly what would change."),
	)
	ev2 := send(t, env, ctx, ev.done, "It was 5 sets, and the last two were 25 kg", &milo.MiloContext{Timezone: "UTC"})
	if len(ev2.cards) != 1 {
		t.Fatalf("got %d cards, errors %v", len(ev2.cards), ev2.errs)
	}
	card := ev2.cards[0]
	if card.GetKind() != milo.CardKind_CARD_KIND_CHANGE || card.GetSubtitle() != "shoulders 6th oct, Tue 6 Oct" {
		t.Fatalf("card = %v", card)
	}
	if len(card.GetBase()) != 3 || len(card.GetChanges()) != 3 {
		t.Fatalf("base %d, changes %d, want 3 and 3", len(card.GetBase()), len(card.GetChanges()))
	}
	var lines []string
	for _, l := range card.GetLines() {
		lines = append(lines, l.GetLabel()+"|"+l.GetBefore()+"|"+l.GetAfter())
	}
	want := []string{
		"Sets 1 to 3||20 kg × 15, unchanged",
		"Set 4|20 kg × 15|25 kg × 15",
		"Set 5|20 kg × 15|25 kg × 15",
		"Set 6|20 kg × 15|Removed",
		"Volume for this exercise|1,800|1,650 kg",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	_ = workoutID
}

func TestCardOutcomeReachesNextTurn(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	_, setIDs := shoulders(t, env, ctx)

	model.then(
		use(t, "propose_change", map[string]any{"title": "Landmine press", "sets": []map[string]any{{"op": "delete", "set_id": setIDs[5].String()}}}),
		say(t, "Here's the change."),
	)
	ev := send(t, env, ctx, "", "drop the sixth set", nil)
	if _, err := env.Milo.SetCardStatus(ctx, &milo.SetCardStatusRequest{CardId: ev.cards[0].GetId(), Status: milo.CardStatus_CARD_STATUS_APPLIED}); err != nil {
		t.Fatal(err)
	}

	model.then(say(t, "Done."))
	send(t, env, ctx, ev.done, "thanks", nil)
	last := model.calls[len(model.calls)-1]
	raw, _ := json.Marshal(last.Messages[len(last.Messages)-1])
	if !strings.Contains(string(raw), "applied it") {
		t.Fatalf("next turn didn't say the card was applied: %s", raw)
	}

	model.then(say(t, "Sure."))
	send(t, env, ctx, ev.done, "and again", nil)
	last = model.calls[len(model.calls)-1]
	raw, _ = json.Marshal(last.Messages[len(last.Messages)-1])
	if strings.Contains(string(raw), "card_updates") {
		t.Fatal("the same card update was reported twice")
	}

	list, err := env.Milo.ListConversations(ctx, &milo.ListConversationsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if list.GetConversations()[0].GetSummary() != "Landmine press updated" {
		t.Fatalf("summary = %q", list.GetConversations()[0].GetSummary())
	}
}

func TestAgentRejectsOverBudget(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{DailyTokenCap: 1000}, model)
	ctx, userID := env.SignUp(t, "hishaam")
	if _, err := env.Pool.Exec(context.Background(), "INSERT INTO agent_usage (user_id, day, input_tokens, output_tokens) VALUES ($1, (now() AT TIME ZONE 'UTC')::date, 900, 200)", userID); err != nil {
		t.Fatal(err)
	}
	ev := send(t, env, ctx, "", "hi", nil)
	if len(ev.errs) != 1 || !strings.Contains(ev.errs[0].GetMessage(), "allowance") {
		t.Fatalf("errors = %v", ev.errs)
	}
	if len(model.calls) != 0 {
		t.Fatal("the model was called over budget")
	}
}

func TestLoopStreamErrorKeepsHistoryConsistent(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench := builtin(t, env, "Bench Press (Barbell)")
	workoutID := openWorkout(t, env, ctx)

	model.then(use(t, "log_sets", map[string]any{"exercise_id": bench.String(), "sets": []map[string]any{{"reps": 5, "weight": "80", "unit": "kg"}}}), fail())
	ev := send(t, env, ctx, "", "bench 80 for 5", &milo.MiloContext{WorkoutId: workoutID.String()})
	if len(ev.errs) != 1 || !ev.errs[0].GetRetryable() || ev.done == "" {
		t.Fatalf("errors %v done %q", ev.errs, ev.done)
	}
	var n int
	_ = env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM agent_messages").Scan(&n)
	if n != 0 {
		t.Fatalf("a failed turn saved %d messages", n)
	}

	model.then(say(t, "Still here."))
	ev2 := send(t, env, ctx, ev.done, "you there?", nil)
	if len(ev2.errs) != 0 || ev2.text != "Still here." {
		t.Fatalf("next turn: %v %q", ev2.errs, ev2.text)
	}
	_ = env.Pool.QueryRow(context.Background(), "SELECT count(*) FROM agent_messages").Scan(&n)
	if n != 2 {
		t.Fatalf("after a clean turn there are %d messages, want 2", n)
	}
}

func TestHistoryIsAppendOnly(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")

	model.then(use(t, "list_templates", map[string]any{}), say(t, "You have none."))
	ev := send(t, env, ctx, "", "what templates do I have?", nil)
	read := func() []string {
		rows, err := env.Pool.Query(context.Background(), "SELECT content::text FROM agent_messages ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out = append(out, s)
		}
		return out
	}
	first := read()
	if len(first) != 4 {
		t.Fatalf("first turn saved %d messages, want 4 (user, tool use, tool result, answer)", len(first))
	}
	model.then(say(t, "Okay."))
	send(t, env, ctx, ev.done, "ok", nil)
	second := read()
	if len(second) != 6 {
		t.Fatalf("after two turns there are %d messages, want 6", len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("message %d changed between turns", i)
		}
	}
	// The second request replays the first turn exactly, then adds the new message.
	if got := len(model.calls[len(model.calls)-1].Messages); got != 5 {
		t.Fatalf("second turn sent %d messages, want 5", got)
	}
}

func TestReadToolsScopeToUser(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	alice, _ := env.SignUp(t, "alice")
	bob, _ := env.SignUp(t, "bob")
	workoutID, setIDs := shoulders(t, env, alice)

	model.then(
		use(t, "get_workouts", map[string]any{"workout_ids": []string{workoutID.String()}}),
		use(t, "propose_change", map[string]any{"title": "x", "sets": []map[string]any{{"op": "delete", "set_id": setIDs[0].String()}}}),
		say(t, "Nothing there."),
	)
	ev := send(t, env, bob, "", "show me that workout", nil)
	if res := lastToolResult(t, model.calls[1]); strings.Contains(res, workoutID.String()) {
		t.Fatalf("bob read alice's workout: %s", res)
	}
	if res := lastToolResult(t, model.calls[2]); !strings.HasPrefix(res, "ERROR") {
		t.Fatalf("bob could propose a change to alice's set: %s", res)
	}
	if len(ev.cards) != 0 {
		t.Fatal("bob got a card for alice's data")
	}
}

func TestGetConversationReplaysItems(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID, _ := shoulders(t, env, ctx)

	model.then(use(t, "show_workout", map[string]any{"workout_id": workoutID.String()}), say(t, "Five exercises in 70 minutes."))
	ev := send(t, env, ctx, "", "show me 6 Oct", nil)
	got, err := env.Milo.GetConversation(ctx, &milo.GetConversationRequest{Id: ev.done})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range got.GetItems() {
		switch it.GetItem().(type) {
		case *milo.MiloItem_UserText:
			kinds = append(kinds, "user")
		case *milo.MiloItem_AssistantText:
			kinds = append(kinds, "assistant")
		case *milo.MiloItem_Card:
			kinds = append(kinds, "card")
		case *milo.MiloItem_Reading:
			kinds = append(kinds, "reading")
		}
	}
	if strings.Join(kinds, ",") != "user,card,assistant" {
		t.Fatalf("items = %v", kinds)
	}
	if got.GetConversation().GetTitle() != "show me 6 Oct" {
		t.Fatalf("title = %q", got.GetConversation().GetTitle())
	}
}

func TestOneTurnAddingSeveralExercises(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	_, _ = shoulders(t, env, ctx) // an existing workout with one exercise at position 1
	workoutID := openWorkout(t, env, ctx)
	bench, squat := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)")
	set := []map[string]any{{"reps": 5, "weight": "100", "unit": "kg"}}

	model.then(
		use(t, "log_sets", map[string]any{"exercise_id": bench.String(), "sets": set}),
		use(t, "log_sets", map[string]any{"exercise_id": squat.String(), "sets": set}),
		use(t, "log_sets", map[string]any{"exercise_id": bench.String(), "sets": set}),
		say(t, "Logged."),
	)
	ev := send(t, env, ctx, "", "bench, squat, then bench again", &milo.MiloContext{WorkoutId: workoutID.String()})
	if len(ev.cards) != 3 {
		t.Fatalf("got %d cards, errors %v", len(ev.cards), ev.errs)
	}
	positions := map[int32]string{}
	weByExercise := map[string]string{}
	var setPositions []int32
	for _, card := range ev.cards {
		for _, c := range card.GetChanges() {
			if we := c.GetWorkoutExercise(); we != nil {
				if other, ok := positions[we.GetPosition()]; ok {
					t.Fatalf("position %d used twice (%s and %s)", we.GetPosition(), other, we.GetExerciseId())
				}
				positions[we.GetPosition()] = we.GetExerciseId()
				weByExercise[we.GetExerciseId()] = we.GetId()
			}
			if s := c.GetSet(); s != nil && s.GetWorkoutExerciseId() == weByExercise[bench.String()] {
				setPositions = append(setPositions, s.GetPosition())
			}
		}
	}
	if len(positions) != 2 {
		t.Fatalf("bench logged twice created %d workout exercises, want 2 in total", len(positions))
	}
	if len(setPositions) != 2 || setPositions[0] != 1 || setPositions[1] != 2 {
		t.Fatalf("bench set positions = %v, want [1 2]", setPositions)
	}
}

func TestConversationsAreSummarisedAndDeleted(t *testing.T) {
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	alice, _ := env.SignUp(t, "alice")
	bob, _ := env.SignUp(t, "bob")
	workoutID := openWorkout(t, env, alice)
	bench := builtin(t, env, "Bench Press (Barbell)")

	model.then(say(t, "You haven't logged anything this week yet, so there is nothing to compare."))
	ev := send(t, env, alice, "", "How has this week gone compared with the one before it, roughly speaking?", nil)
	list, _ := env.Milo.ListConversations(alice, &milo.ListConversationsRequest{})
	c := list.GetConversations()[0]
	if c.GetTitle() != "How has this week gone compared with the one before it…" || c.GetSummary() != "You haven't logged anything this week yet, so there is nothing to compare." {
		t.Fatalf("title %q, summary %q", c.GetTitle(), c.GetSummary())
	}

	// Once cards are applied, the summary says what was done, in order.
	set := []map[string]any{{"reps": 5, "weight": "100"}}
	model.then(use(t, "log_sets", map[string]any{"exercise_id": bench.String(), "sets": set}), use(t, "log_sets", map[string]any{"exercise_id": bench.String(), "sets": set}), say(t, "Logged."))
	ev = send(t, env, alice, ev.done, "bench 100 for 5, twice", &milo.MiloContext{WorkoutId: workoutID.String()})
	for _, card := range ev.cards {
		if _, err := env.Milo.SetCardStatus(alice, &milo.SetCardStatusRequest{CardId: card.GetId(), Status: milo.CardStatus_CARD_STATUS_APPLIED}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = env.Milo.ListConversations(alice, &milo.ListConversationsRequest{})
	if got := list.GetConversations()[0].GetSummary(); got != "Logged 1 set · Logged 1 set" {
		t.Fatalf("summary = %q", got)
	}

	// Only its owner can delete it, and then it is gone.
	_, err := env.Milo.DeleteConversation(bob, &milo.DeleteConversationRequest{Id: ev.done})
	testkit.WantCode(t, err, connect.CodeNotFound)
	if _, err := env.Milo.DeleteConversation(alice, &milo.DeleteConversationRequest{Id: ev.done}); err != nil {
		t.Fatal(err)
	}
	list, _ = env.Milo.ListConversations(alice, &milo.ListConversationsRequest{})
	if len(list.GetConversations()) != 0 {
		t.Fatalf("%d conversations left after deleting the only one", len(list.GetConversations()))
	}
}
