package agent_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/agent"
)

// TestLiveEvals runs Milo against the real Claude API. It costs money, so it
// only runs with MILO_LIVE=1 (key from the repo-root .env).
func TestLiveEvals(t *testing.T) {
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
	env := testkit.NewAPI(t, config.Config{Model: "claude-opus-5-5"}, agent.NewAnthropicModel(key))
	ctx, userID := env.SignUp(t, "hishaam")
	defer func() {
		var in, out, cr, cw int64
		_ = env.Pool.QueryRow(context.Background(), "SELECT COALESCE(sum(input_tokens),0), COALESCE(sum(output_tokens),0), COALESCE(sum(cache_read_tokens),0), COALESCE(sum(cache_write_tokens),0) FROM agent_usage WHERE user_id = $1", userID).Scan(&in, &out, &cr, &cw)
		cost := float64(in)*4/1e6 + float64(out)*20/1e6 + float64(cr)*0.2/1e6 + float64(cw)*5/1e6
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
			if c.GetKind() == milo.CardKind_CARD_KIND_LOGGED {
				logged = c
			}
		}
		if logged == nil || len(logged.GetLines()) != 2 {
			t.Fatalf("logged card = %v, errors %v", logged, ev.errs)
		}
	})
}
