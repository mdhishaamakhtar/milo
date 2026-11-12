package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// The cards that answer a question by showing it, and the reads behind them.

// logged puts a finished workout in the log: one exercise, sets of weight × reps, some days ago.
func logged(t *testing.T, env *testkit.API, ctx context.Context, model *fakeModel, name string, exercise uuid.UUID, daysAgo int, weight string, reps ...int) *milo.Card {
	t.Helper()
	sets := make([]map[string]any, len(reps))
	for i, r := range reps {
		sets[i] = map[string]any{"reps": r, "weight": weight}
	}
	_, cards := call(t, env, ctx, model, nil, toolUse{"log_sets", map[string]any{
		"exercise_id": exercise.String(), "sets": sets,
		"new_workout": map[string]any{"name": name, "started_at": time.Now().UTC().AddDate(0, 0, -daysAgo).Format("2006-01-02T15:04")},
	}})
	apply(t, env, ctx, cards[0])
	return cards[0]
}

func TestProgressPutsEveryLiftOnOneCard(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench, squat := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)")
	logged(t, env, ctx, model, "Push", bench, 30, "50", 8, 8)
	logged(t, env, ctx, model, "Legs", squat, 29, "80", 5)
	logged(t, env, ctx, model, "Push", bench, 9, "60", 8)
	logged(t, env, ctx, model, "Legs", squat, 8, "80", 5)

	results, cards := call(t, env, ctx, model, nil, toolUse{"show_progress", map[string]any{}})
	if len(cards) != 1 || cards[0].GetKind() != milo.CardKind_CARD_KIND_PROGRESS || cards[0].GetSubtitle() != "1 up, 1 steady, 0 down" {
		t.Fatalf("cards %v", cards)
	}
	top := cards[0].GetLines()[0]
	if top.GetLabel() != "Bench Press (Barbell)" || top.GetBefore() != "50 kg × 8" || top.GetAfter() != "60 kg × 8" || top.GetNote() != "+10 kg" ||
		top.GetTrend() != 1 || len(top.GetSpark()) != 2 || top.GetExerciseId() != bench.String() {
		t.Fatalf("top line %v", top)
	}
	if steady := cards[0].GetLines()[1]; steady.GetNote() != "same" || steady.GetTrend() != 0 {
		t.Fatalf("steady line %v", steady)
	}
	// Milo is handed the same table, so it needn't read each lift.
	if !strings.Contains(results[0], `trend\":\"up`) || !strings.Contains(results[0], `first\":\"50 kg × 8 on `) {
		t.Fatalf("result %s", results[0])
	}

	_, cards = call(t, env, ctx, model, nil, toolUse{"show_progress", map[string]any{"rank": "records"}})
	if best := cards[0].GetLines()[0]; cards[0].GetTitle() != "Best sets" || best.GetAfter() != "60 kg × 8" || best.GetBefore() != "" {
		t.Fatalf("records %v", cards[0])
	}

	// Logging past the best says so.
	results, _ = call(t, env, ctx, model, nil, toolUse{"log_sets", map[string]any{
		"exercise_id": bench.String(), "sets": []map[string]any{{"reps": 5, "weight": "65"}},
		"new_workout": map[string]any{"name": "Push", "started_at": time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04")},
	}})
	if !strings.Contains(results[0], "Heaviest set yet on Bench Press (Barbell): 65 kg × 5 (before: 60 kg × 8") {
		t.Fatalf("no new best in %s", results[0])
	}
}

func TestTrainingAndMuscleCards(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench, squat := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)")
	logged(t, env, ctx, model, "Legs", squat, 60, "80", 5, 5, 5, 5) // before the last four weeks
	logged(t, env, ctx, model, "Push", bench, 9, "60", 8, 8)
	logged(t, env, ctx, model, "Push", bench, 2, "60", 8)

	results, cards := call(t, env, ctx, model, nil,
		toolUse{"show_training", map[string]any{"metric": "workouts"}},
		toolUse{"show_muscles", map[string]any{}},
		toolUse{"training_summary", map[string]any{}},
		toolUse{"list_workouts", map[string]any{"search": "leg"}})
	bars := cards[0]
	if bars.GetKind() != milo.CardKind_CARD_KIND_CHART || !bars.GetChart().GetBars() || bars.GetTitle() != "Workouts a week" ||
		len(bars.GetChart().GetPoints()) < 12 || bars.GetStats()[0].GetValue() != "3" || bars.GetStats()[0].GetLabel() != "workouts" {
		t.Fatalf("training card %v", bars)
	}
	muscles := cards[1]
	if muscles.GetKind() != milo.CardKind_CARD_KIND_BREAKDOWN || muscles.GetLines()[0].GetLabel() != "Chest" || muscles.GetLines()[0].GetShare() != 1 ||
		muscles.GetLines()[0].GetAfter() != "3 sets" {
		t.Fatalf("muscle card %v", muscles)
	}
	// Quads had sets two months ago and none since: Milo is told, though four sets is too few for the card.
	if !strings.Contains(results[1], `muscle\":\"quads\",\"sets\":0`) {
		t.Fatalf("missed muscles not in %s", results[1])
	}
	for _, want := range []string{`workouts_by_weekday`, `longest_run_of_weeks`, `current_run_of_weeks\":2`, `last_trained`} {
		if !strings.Contains(results[2], want) {
			t.Fatalf("summary is missing %s:\n%s", want, results[2])
		}
	}
	if !strings.Contains(results[3], `name\":\"Legs`) || strings.Contains(results[3], `name\":\"Push`) {
		t.Fatalf("list_workouts search: %s", results[3])
	}
}

func TestSuggestedRepliesBecomeButtons(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")

	// The answer's last line offers what to say next: the app gets buttons, not the line.
	model.then(say(t, "You haven't logged anything yet.\n\nA first workout is the place to start.\n\n>> Start a workout | Log yesterday"))
	ev := send(t, env, ctx, "", "how am I doing?", &milo.MiloContext{Timezone: "UTC"})
	if len(ev.errs) > 0 || ev.text != "You haven't logged anything yet.\n\nA first workout is the place to start." {
		t.Fatalf("errors %v, text %q", ev.errs, ev.text)
	}
	if len(ev.cards) != 1 || ev.cards[0].GetKind() != milo.CardKind_CARD_KIND_SUGGESTIONS || len(ev.cards[0].GetLines()) != 2 || ev.cards[0].GetLines()[1].GetLabel() != "Log yesterday" {
		t.Fatalf("cards %v", ev.cards)
	}
	// A reply reopened later reads the same way.
	conv, err := env.Milo.GetConversation(ctx, &milo.GetConversationRequest{Id: ev.done})
	if err != nil || len(conv.GetItems()) != 3 || strings.Contains(conv.GetItems()[1].GetAssistantText(), ">>") || conv.GetItems()[2].GetCard().GetKind() != milo.CardKind_CARD_KIND_SUGGESTIONS {
		t.Fatalf("stored conversation: %v, %v", err, conv.GetItems())
	}

	// A line that only looks like the mark part-way through stays in the answer.
	model.then(say(t, "Up > down.\n> not a mark"))
	if ev = send(t, env, ctx, ev.done, "and?", &milo.MiloContext{Timezone: "UTC"}); ev.text != "Up > down.\n> not a mark" || len(ev.cards) != 0 {
		t.Fatalf("text %q, cards %v", ev.text, ev.cards)
	}
}

func TestReadsInOneReplyAllAnswer(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench, squat := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)")
	logged(t, env, ctx, model, "Push", bench, 3, "60", 8)

	model.then(func(anthropic.MessageNewParams) (*anthropic.Message, error) {
		return message(t, "tool_use",
			map[string]any{"type": "tool_use", "id": "toolu_bench", "name": "exercise_history", "input": map[string]any{"exercise_id": bench.String()}},
			map[string]any{"type": "tool_use", "id": "toolu_squat", "name": "exercise_history", "input": map[string]any{"exercise_id": squat.String()}},
			map[string]any{"type": "tool_use", "id": "toolu_week", "name": "training_summary", "input": map[string]any{}}), nil
	}, say(t, "Done."))
	if ev := send(t, env, ctx, "", "how are bench and squat?", &milo.MiloContext{Timezone: "UTC"}); len(ev.errs) > 0 || len(ev.readings) != 3 {
		t.Fatalf("errors %v, readings %v", ev.errs, ev.readings)
	}
	// Results go back in the order they were asked for, whichever finished first.
	raw, _ := json.Marshal(model.calls[len(model.calls)-1].Messages)
	bi, si, wi := strings.Index(string(raw), `"tool_use_id":"toolu_bench"`), strings.Index(string(raw), `"tool_use_id":"toolu_squat"`), strings.Index(string(raw), `"tool_use_id":"toolu_week"`)
	if bi < 0 || bi > si || si > wi {
		t.Fatalf("results out of order (%d, %d, %d): %s", bi, si, wi, raw)
	}
}

func TestDeletingSeveralWorkoutsIsOneCard(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench := builtin(t, env, "Bench Press (Barbell)")
	a := logged(t, env, ctx, model, "Push", bench, 9, "60", 8, 8).GetWorkoutId()
	b := logged(t, env, ctx, model, "Push again", bench, 2, "60", 8).GetWorkoutId()

	_, cards := call(t, env, ctx, model, nil, toolUse{"propose_delete", map[string]any{"what": "workout", "ids": []string{a, b}}})
	if len(cards) != 1 || cards[0].GetTitle() != "Delete 2 workouts" || !cards[0].GetDestructive() || cards[0].GetAutoApply() ||
		cards[0].GetSubtitle() != "3 sets in all" || len(cards[0].GetLines()) != 2 || rowsOf(cards[0])["workout deleted"] != 2 {
		t.Fatalf("card %v, rows %v", cards[0], rowsOf(cards[0]))
	}
}

// Each chart draws what it says: a lift's volume or estimated best since a day, lifts
// compared on one chart, and training as bars by week or by month.
func TestChartsDrawWhatTheyAreAskedFor(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench, squat := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)")
	logged(t, env, ctx, model, "Push", bench, 40, "50", 8, 8)
	logged(t, env, ctx, model, "Push", bench, 20, "55", 8)
	logged(t, env, ctx, model, "Push", bench, 10, "60", 5)
	logged(t, env, ctx, model, "Legs", squat, 9, "80", 5)
	since := time.Now().UTC().AddDate(0, 0, -25).Format(time.DateOnly)

	_, cards := call(t, env, ctx, model, nil,
		toolUse{"show_chart", map[string]any{"metric": "volume", "exercise_id": bench.String(), "date_from": since}},
		toolUse{"show_chart", map[string]any{"metric": "e1rm", "exercise_id": bench.String()}},
		toolUse{"show_chart", map[string]any{"metric": "top_set", "exercise_id": bench.String(), "compare_with": []string{squat.String()}}},
		toolUse{"show_training", map[string]any{"metric": "sets"}},
		toolUse{"show_training", map[string]any{"metric": "minutes", "by": "month"}},
		toolUse{"show_training", map[string]any{"metric": "volume"}},
	)
	volume, e1rm, compared, sets, minutes, tonnage := cards[0], cards[1], cards[2], cards[3], cards[4], cards[5]

	// From 25 days back: the first session is left out.
	if volume.GetTitle() != "Bench Press (Barbell), volume" || len(volume.GetChart().GetPoints()) != 2 || volume.GetChart().GetPoints()[0].GetLabel() != "440 kg" {
		t.Fatalf("volume: %q %v", volume.GetTitle(), volume.GetChart().GetPoints())
	}
	if e1rm.GetTitle() != "Bench Press (Barbell), estimated 1RM" || len(e1rm.GetChart().GetPoints()) != 3 || !strings.HasPrefix(e1rm.GetSubtitle(), "70 kg on ") {
		t.Fatalf("e1rm: %q / %q", e1rm.GetTitle(), e1rm.GetSubtitle())
	}
	if compared.GetTitle() != "Bench Press vs Squat, top set" || len(compared.GetChart().GetSeries()) != 2 || compared.GetExerciseId() != "" {
		t.Fatalf("compared: %q, %d lines, opens %q", compared.GetTitle(), len(compared.GetChart().GetSeries()), compared.GetExerciseId())
	}
	for _, c := range []*milo.Card{sets, minutes, tonnage} {
		if !c.GetChart().GetBars() || len(c.GetStats()) != 3 || c.GetStats()[0].GetValue() != "4" {
			t.Fatalf("%q: bars %v, stats %v", c.GetTitle(), c.GetChart().GetBars(), c.GetStats())
		}
	}
	if sets.GetTitle() != "Sets a week" || minutes.GetTitle() != "Minutes a month" || tonnage.GetTitle() != "Volume a week" {
		t.Fatalf("titles %q, %q, %q", sets.GetTitle(), minutes.GetTitle(), tonnage.GetTitle())
	}
	if n := len(minutes.GetChart().GetPoints()); n < 3 || n > 4 {
		t.Fatalf("twelve weeks by month gave %d bars", n)
	}
}
