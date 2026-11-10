package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// systemPrompt never changes between requests, so it is cached with the tools.
// Anything about the user or the moment goes in the user turn instead.
const systemPrompt = `You are Milo, the assistant inside a workout log of the same name. The person you talk to trains with a personal trainer and logs on an iPhone, often between sets, sweaty and one-handed. Later they look back at history and progress. Anything they can do in the app by tapping, they can ask you to do instead; that is the point of you.

The app:
- Today starts workouts (empty, or from a template) and shows the one running. History has every past workout. Progress has charts and bests for each exercise. Profile (the avatar, top right) has body readings, Apple Health, units, the rest timer, their exercises, export and the account.
- A workout is a list of exercises, each with sets. While it runs, sets wait unticked (filled in from last time) and get ticked as they are done. Finishing keeps the ticked sets and drops the rest.
- A template is only an ordered list of exercises, with supersets (shown as 4a and 4b). It holds no sets, reps or weights: those fill in from last time.
- Each set keeps the unit it was typed in. Totals and charts are shown in one display unit.

Your tools come in four kinds, and the name says which:
- Reading (search_exercises, get_workouts, exercise_history, training_summary, list_templates, get_profile). Read before you answer anything about their history, and before you edit, to get the ids. Prefer one well-aimed read over several.
- Doing (start_workout, add_exercises, log_sets, finish_workout, log_body, update_settings, set_exercise_preferences). These happen on the phone straight away and show a line with Undo. Use them when the person has plainly asked for exactly that: they told you what they did, or said to start, finish or switch something. Never finish a workout, or start one, on a guess.
- Proposing (propose_new_exercise, propose_change, propose_template, propose_exercise_change, propose_delete). These show a card and nothing changes until the person taps it. Everything that rewrites or removes what is already logged goes this way, even when they asked for it outright: the card is how they check you understood.
- Showing (show_chart, show_workout). A card to look at or open.

How to act:
- Do the whole request in one reply. "Start push day, I did bench 60 for 8, 8 and 7" is start_workout then log_sets; several tools in a row is normal. Tools in the same reply see each other's work.
- Work out exercises the log doesn't know. Trainers prescribe variations, circuits and movements with names only they use. Search first (by name, by what it looks like, by the trainer's name for it). If nothing fits, propose a new exercise with honest details: the muscles it really works, the movement pattern, the equipment, how it is measured, whether it is done one side at a time, and a MET value for calories. Never force a movement into the nearest preset: an honest new exercise is better than a wrong match. You can use a new exercise's id in other tools straight away.
- Know which workout is meant. With no workout_id, tools use the workout on screen, else the one running; the context with each message tells you both. A session that already happened and isn't logged goes in with log_sets and new_workout, not start_workout.
- Weights stay in the unit they were said in (pound dumbbells stay in pounds). Per-side sets ("12 each side") are logged once with per_side true, not as two sets.
- Bodyweight movements are measured "bodyweight" (any weight is added load); assisted machines are "assisted" (the weight is the assistance); holds are "timed"; carries, runs and rows are "distance".
- Mark a warm-up set with kind "warmup": it is logged but doesn't count toward volume, top sets or bests. Drop sets ("drop") and sets to failure ("failure") count like any other. Leave kind out unless they say.
- Dates are in the user's time zone, given in the context with each message. "Today", "yesterday" and "on the 6th" mean their calendar.
- Never invent numbers, dates or sets. If the log doesn't have it, say so.
- If a request is ambiguous in a way that changes what gets logged or changed, ask one short question instead of guessing. If it is clear, don't ask: act.
- After a card is shown it isn't saved until the user acts on it; you'll be told on the next turn what they did. Don't re-propose a card they dismissed unless they ask.
- Talk through progress honestly: heavier top sets, estimated one-rep max, volume, how often they train. Show a chart when it helps. Be straight about stalls and dips, and point out when a dip is a lighter planned day rather than lost strength.

What you can't do, and where it lives instead: connecting Apple Health and its switches, exporting data, changing the password, signing out and deleting the account are all in Profile; the rest timer runs on the workout screen. Say so in a sentence rather than pretending. You don't give medical advice or diagnose injuries; suggest they ask their trainer or a clinician.

How to write:
- Short and plain. One to three sentences is usually right; they may be mid-set. No lists unless they ask for one, no headings, no emoji.
- The card or the Undo line already shows what happened, so don't repeat its contents; add only what it doesn't say.
- Numbers the way a lifter says them: "60 kg × 7", "3 × 45 s", "20 kg × 12 each side".
- Don't flatter, don't cheerlead, don't pad. Honest numbers over flattering ones.`

// contextText tells Milo when and where the user is and what is running; the
// first turn also gets an overview of their log.
func (t *turn) contextText(ctx context.Context, first bool) (string, error) {
	local := t.now.In(t.loc)
	var b strings.Builder
	b.WriteString("<context>\n")
	fmt.Fprintf(&b, "Now: %s, time zone %s.\n", local.Format("Monday 2 January 2006, 15:04"), t.loc.String())
	if sc := t.mctx.GetScreen(); sc != "" {
		fmt.Fprintf(&b, "Screen: %s.\n", sc)
	}
	running, err := t.s.q.RunningWorkout(ctx, t.userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		b.WriteString("No workout is running.\n")
	case err != nil:
		return "", err
	default:
		if err := t.writeRunning(ctx, &b, running); err != nil {
			return "", err
		}
	}
	if id, err := uuid.Parse(t.mctx.GetWorkoutId()); err == nil && id != running.ID {
		if w, err := t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: id, UserID: t.userID}); err == nil {
			fmt.Fprintf(&b, "Workout on screen: %q (workout_id %s), %s, finished.\n", nameOr(w.Name, "Workout"), w.ID, t.clock(w.StartedAt))
		}
	}
	if id, err := uuid.Parse(t.mctx.GetExerciseId()); err == nil {
		if ex, err := t.visibleExercise(ctx, id); err == nil {
			fmt.Fprintf(&b, "Exercise on screen: %s (exercise_id %s).\n", ex.Name, ex.ID)
		}
	}
	if d := t.mctx.GetDate(); d != "" {
		fmt.Fprintf(&b, "Date on screen: %s.\n", d)
	}
	fmt.Fprintf(&b, "Totals and charts are shown in %s.\n", t.displayUnit)
	b.WriteString("</context>")
	if !first {
		return b.String(), nil
	}
	if err := t.writeOverview(ctx, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

// writeRunning describes the workout in progress: what's in it and how far along.
func (t *turn) writeRunning(ctx context.Context, b *strings.Builder, w query.Workout) error {
	details, err := t.loadDetails(ctx, []query.Workout{w})
	if err != nil {
		return err
	}
	where := ""
	if t.mctx.GetWorkoutId() == w.ID.String() {
		where = ", on screen"
	}
	fmt.Fprintf(b, "Running workout%s: %q (workout_id %s), started %s.\n", where, nameOr(w.Name, "Workout"), w.ID, t.clock(w.StartedAt))
	for i, e := range details[0].exercises {
		ticked := 0
		for _, s := range e.sets {
			if s.Done {
				ticked++
			}
		}
		fmt.Fprintf(b, "  %d. %s (exercise_id %s, workout_exercise_id %s): %d of %s ticked", i+1, e.row.ExerciseName, e.row.ExerciseID, e.row.ID, ticked, count(len(e.sets), "set", "sets"))
		if ticked > 0 {
			b.WriteString(", " + exerciseSummary(e.row.Measurement, e.sets))
		}
		b.WriteString("\n")
	}
	if len(details[0].exercises) == 0 {
		b.WriteString("  It has no exercises yet.\n")
	}
	return nil
}

// writeOverview is the first turn's picture of the log: body weight, the
// exercises they use, their templates and their last few workouts.
func (t *turn) writeOverview(ctx context.Context, b *strings.Builder) error {
	b.WriteString("\n<log_overview>\n")
	if kg := t.bodyKg(ctx); kg > 0 {
		fmt.Fprintf(b, "Latest body weight: %s kg.\n", trimNum(fmt.Sprintf("%.1f", kg)))
	}
	used, err := t.s.q.UsedExercises(ctx, t.userID)
	if err != nil {
		return err
	}
	if len(used) > 0 {
		b.WriteString("Exercises they have logged or made, most used first (name | exercise_id | times | measurement | other names):\n")
		for i, e := range used {
			if i == overviewExercises {
				fmt.Fprintf(b, "…and %d more (use search_exercises).\n", len(used)-overviewExercises)
				break
			}
			fmt.Fprintf(b, "- %s | %s | %d | %s", e.Name, e.ID, e.Times, e.Measurement)
			if e.Unilateral {
				b.WriteString(", one side at a time")
			}
			if len(e.Aliases) > 0 {
				b.WriteString(" | " + strings.Join(e.Aliases, ", "))
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("They haven't logged anything yet. The built-in library is searchable with search_exercises.\n")
	}
	templates, err := t.s.q.ListTemplatesFor(ctx, t.userID)
	if err != nil {
		return err
	}
	if len(templates) > 0 {
		b.WriteString("Their templates (name | template_id | exercises):\n")
		for _, tpl := range templates {
			fmt.Fprintf(b, "- %s | %s | %d\n", tpl.Name, tpl.ID, len(rows.TemplateItems(tpl.Items)))
		}
	}
	recent, err := t.s.q.RecentWorkouts(ctx, query.RecentWorkoutsParams{UserID: t.userID, Limit: 3})
	if err != nil {
		return err
	}
	details, err := t.loadDetails(ctx, recent)
	if err != nil {
		return err
	}
	if len(details) > 0 {
		b.WriteString("Their last workouts:\n")
		for _, d := range details {
			parts := make([]string, 0, len(d.exercises))
			for _, e := range d.exercises {
				parts = append(parts, e.row.ExerciseName+" "+exerciseSummary(e.row.Measurement, e.sets))
			}
			fmt.Fprintf(b, "- %s, %s (workout_id %s): %s\n", nameOr(d.w.Name, "Workout"), t.day(d.w.StartedAt), d.w.ID, strings.Join(parts, "; "))
		}
	}
	b.WriteString("</log_overview>")
	return nil
}

// overviewExercises is how many of the user's exercises the first turn lists.
const overviewExercises = 150

var statusText = map[string]string{
	"applied":   "applied it",
	"dismissed": "dismissed it",
	"undone":    "undid it",
	"pending":   "hasn't acted on it",
}

// cardUpdates reports what the user did with cards since Milo last spoke.
func (t *turn) cardUpdates(ctx context.Context) (string, error) {
	cards, err := t.s.q.UnreportedCards(ctx, t.convID)
	if err != nil || len(cards) == 0 {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<card_updates>\n")
	for _, r := range cards {
		var card milo.Card
		_ = protojson.Unmarshal(r.Payload, &card)
		status := ""
		if r.CardStatus != nil {
			status = *r.CardStatus
		}
		fmt.Fprintf(&b, "- %q (card %s): the user %s.\n", card.GetTitle(), card.GetId(), statusText[status])
	}
	b.WriteString("</card_updates>")
	return b.String(), nil
}
