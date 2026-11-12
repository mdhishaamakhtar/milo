package agent_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/agent"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// TestLiveEvals runs Milo against the real Claude API. It costs money, so it
// only runs with MILO_LIVE=1 (key from the repo-root .env).
func TestLiveEvals(t *testing.T) {
	t.Parallel()
	if os.Getenv("MILO_LIVE") != "1" {
		t.Skip("set MILO_LIVE=1 to run against the Claude API")
	}
	_ = godotenv.Load("../../../.env")
	key := os.Getenv("ANTHROPIC_API_KEY_MILO")
	if key == "" {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	if key == "" {
		t.Fatal("no API key")
	}
	env := testkit.NewAPI(t, config.Config{}, agent.NewAnthropicModel(key))
	ctx, userID := env.SignUp(t, "hishaam")
	defer func() {
		var in, out, cr, cw int64
		_ = env.Pool.QueryRow(context.Background(), "SELECT COALESCE(sum(input_tokens),0), COALESCE(sum(output_tokens),0), COALESCE(sum(cache_read_tokens),0), COALESCE(sum(cache_write_tokens),0) FROM agent_usage WHERE user_id = $1", userID).Scan(&in, &out, &cr, &cw)
		// Claude Haiku 5.5 prices per million tokens, for prompts up to 100K.
		cost := float64(in)*0.10/1e6 + float64(out)*0.50/1e6 + float64(cr)*0.01/1e6 + float64(cw)*0.125/1e6
		t.Logf("usage: %d in, %d out, %d cache read, %d cache write ≈ $%.3f", in, out, cr, cw, cost)
	}()

	t.Run("unknown movement becomes a new exercise", func(t *testing.T) {
		workoutID := openWorkout(t, env, ctx)
		ev := send(t, env, ctx, "", "landmine rotation, barbell in the corner, twist side to side. 20 kg, 12 each side", &milo.MiloContext{Screen: "workout", WorkoutId: workoutID.String(), Timezone: "Asia/Kolkata"})
		t.Logf("milo: %s", ev.text)
		if len(ev.errs) > 0 {
			t.Fatalf("errors: %v", ev.errs)
		}
		var card *milo.Card
		for _, c := range ev.cards {
			if c.GetKind() == milo.CardKind_CARD_KIND_NEW_EXERCISE {
				card = c
			}
		}
		if card == nil {
			t.Fatalf("no new exercise card; cards %v", ev.cards)
		}
		var unilateral, perSide bool
		for _, c := range card.GetChanges() {
			if e := c.GetExercise(); e != nil {
				unilateral = e.GetUnilateral()
			}
			if s := c.GetSet(); s != nil {
				perSide = s.GetPerSide()
			}
		}
		for _, l := range card.GetLines() {
			t.Logf("  %s | %s | %s", l.GetLabel(), l.GetBefore(), l.GetAfter())
		}
		if !unilateral || !perSide {
			t.Fatalf("unilateral %v, per-side set %v", unilateral, perSide)
		}
	})

	t.Run("ask about a date, then fix it", func(t *testing.T) {
		shoulders(t, env, ctx)
		ev := send(t, env, ctx, "", "What did I do for shoulders on 6 October? I think the landmine press is logged wrong.", &milo.MiloContext{Screen: "today", Timezone: "UTC"})
		t.Logf("readings %v; milo: %s", ev.readings, ev.text)
		if len(ev.errs) > 0 || len(ev.readings) == 0 {
			t.Fatalf("errors %v readings %v", ev.errs, ev.readings)
		}
		ev2 := send(t, env, ctx, ev.done, "It was 5 sets, and the last two were 25 kg", &milo.MiloContext{Timezone: "UTC"})
		t.Logf("milo: %s", ev2.text)
		var card *milo.Card
		for _, c := range ev2.cards {
			if c.GetKind() == milo.CardKind_CARD_KIND_CHANGE {
				card = c
			}
		}
		if card == nil {
			t.Fatalf("no change card; errors %v", ev2.errs)
		}
		var lines []string
		for _, l := range card.GetLines() {
			lines = append(lines, l.GetLabel()+" | "+l.GetBefore()+" | "+l.GetAfter())
			t.Logf("  %s", lines[len(lines)-1])
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "Set 6 | 20 kg × 15 | Removed") || !strings.Contains(joined, "25 kg × 15") {
			t.Fatalf("change card doesn't remove set 6 and make sets 25 kg")
		}
	})

	t.Run("known exercise logs straight away", func(t *testing.T) {
		workoutID := openWorkout(t, env, ctx)
		ev := send(t, env, ctx, "", "bench press 60 kg for 10, then 62.5 for 8", &milo.MiloContext{Screen: "workout", WorkoutId: workoutID.String(), Timezone: "Asia/Kolkata"})
		t.Logf("milo: %s", ev.text)
		var logged *milo.Card
		for _, c := range ev.cards {
			if c.GetKind() == milo.CardKind_CARD_KIND_DONE {
				logged = c
			}
		}
		if logged == nil || len(logged.GetLines()) != 2 {
			t.Fatalf("logged card = %v, errors %v", logged, ev.errs)
		}
	})

	// cardsOf picks the cards of one kind out of a turn.
	cardsOf := func(ev events, kind milo.CardKind) []*milo.Card {
		var out []*milo.Card
		for _, c := range ev.cards {
			if c.GetKind() == kind {
				out = append(out, c)
			}
		}
		return out
	}
	describe := func(t *testing.T, ev events) {
		t.Helper()
		t.Logf("milo: %s", ev.text)
		for _, c := range ev.cards {
			t.Logf("  card %s: %q / %q, auto %v, %v", c.GetKind(), c.GetTitle(), c.GetSubtitle(), c.GetAutoApply(), rowsOf(c))
		}
	}

	t.Run("start a workout and log into it in one message, then finish", func(t *testing.T) {
		// Finish whatever the earlier cases left running, as the phone would have.
		if _, err := env.Pool.Exec(context.Background(), "UPDATE workouts SET ended_at = now() WHERE ended_at IS NULL"); err != nil {
			t.Fatal(err)
		}
		ev := send(t, env, ctx, "", "Start a workout with bench press and barbell squat. I just did bench: 60 kg for 8, 8 and 7.", &milo.MiloContext{Screen: "today", Timezone: "Asia/Kolkata"})
		describe(t, ev)
		done := cardsOf(ev, milo.CardKind_CARD_KIND_DONE)
		if len(ev.errs) > 0 || len(done) != 2 || rowsOf(done[0])["workout"] != 1 || rowsOf(done[0])["workout_exercise"] != 2 || rowsOf(done[1])["set"] != 3 {
			t.Fatalf("want a started workout with two exercises, then three logged sets; errors %v", ev.errs)
		}
		for _, c := range done {
			apply(t, env, ctx, c)
		}
		ev = send(t, env, ctx, ev.done, "That's me done for today, wrap it up.", &milo.MiloContext{Screen: "today", Timezone: "Asia/Kolkata"})
		describe(t, ev)
		done = cardsOf(ev, milo.CardKind_CARD_KIND_DONE)
		if len(done) != 1 || !strings.HasPrefix(done[0].GetTitle(), "Finished") || rowsOf(done[0])["workout_exercise deleted"] != 1 {
			t.Fatalf("want one Finished card that drops the squat nobody did")
		}
		apply(t, env, ctx, done[0])
	})

	t.Run("deleting takes a tap; settings and a body reading don't", func(t *testing.T) {
		ev := send(t, env, ctx, "", "Delete the shoulders workout from 6 October. Also show my totals in pounds from now on, and I weighed 81.5 kg this morning.", &milo.MiloContext{Screen: "history", Timezone: "UTC"})
		describe(t, ev)
		change, done := cardsOf(ev, milo.CardKind_CARD_KIND_CHANGE), cardsOf(ev, milo.CardKind_CARD_KIND_DONE)
		if len(change) != 1 || !change[0].GetDestructive() || change[0].GetAutoApply() || rowsOf(change[0])["workout deleted"] != 1 {
			t.Fatalf("want one delete card that waits for a tap")
		}
		var settings, reading bool
		for _, c := range done {
			settings = settings || rowsOf(c)["settings"] == 1
			reading = reading || rowsOf(c)["body_measurement"] == 1
		}
		if !settings || !reading {
			t.Fatalf("want the unit switched and the reading logged straight away (settings %v, reading %v)", settings, reading)
		}
	})

	t.Run("a month in one read, and honesty about what it can't do", func(t *testing.T) {
		ev := send(t, env, ctx, "", "What have I trained most this past month, and am I neglecting anything?", &milo.MiloContext{Screen: "progress", Timezone: "UTC"})
		describe(t, ev)
		// It reads the month, or shows it: either way the answer comes from the log.
		if len(ev.errs) > 0 || len(ev.readings)+len(cardsOf(ev, milo.CardKind_CARD_KIND_BREAKDOWN))+len(cardsOf(ev, milo.CardKind_CARD_KIND_CHART)) == 0 {
			t.Fatalf("want an answer from the log: errors %v, readings %v", ev.errs, ev.readings)
		}
		ev = send(t, env, ctx, ev.done, "Thanks. Can you change my password to hunter2 and connect Apple Health?", &milo.MiloContext{Screen: "progress", Timezone: "UTC"})
		describe(t, ev)
		if len(ev.cards) != len(cardsOf(ev, milo.CardKind_CARD_KIND_SUGGESTIONS)) || !strings.Contains(ev.text, "Profile") {
			t.Fatalf("want no cards but suggested replies, and a pointer to Profile")
		}
	})
}
