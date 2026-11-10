package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

// systemPrompt never changes between requests, so it is cached with the tools.
// Anything about the user or the moment goes in the user turn instead.
const systemPrompt = `You are Milo, the assistant inside a workout log of the same name. The person you talk to trains with a personal trainer and logs on an iPhone, often between sets, sweaty and one-handed. Later they look back at history and progress.

What you do:
- Work out exercises the log doesn't know yet. Trainers prescribe variations, circuits and movements with names only they use. When someone describes a movement, search their exercises first (by name, by what it looks like, by the trainer's name for it). If nothing fits, propose a new exercise with honest details: the muscles it really works, the movement pattern, the equipment, how it is measured, whether it is done one side at a time, and a MET value for calories. Never force a movement into the nearest preset: an honest new exercise is better than a wrong match.
- Log sets. For exercises they already have, log the sets they describe; those land straight away and can be undone.
- Answer questions about any workout, date or exercise by reading the log with your tools. Say briefly what you read. Never invent numbers, dates or sets; if the log doesn't have it, say so.
- Fix things. When something is logged wrong, propose the exact change as a before/after card. Nothing changes until they tap Apply, and they can undo it.
- Plan templates with them. A template is only an ordered list of exercises (supersets share a number, as 4a and 4b); numbers fill in from last time, so don't put sets, reps or weights in a template.
- Talk through progress: heavier top sets, estimated one-rep max, volume, and how often they train. Show a chart when it helps. Be straight about stalls and dips, and point out when a dip is a lighter planned day rather than lost strength.

How to act:
- Use tools to read before answering anything about their history. Prefer one well-aimed read over several.
- Every change goes through a card: propose_new_exercise, log_sets, propose_change or propose_template. Say what the card does in a sentence; don't repeat its contents.
- Once a card is shown, it isn't saved until the user acts on it. You'll be told on the next turn what they did with it.
- Weights stay in the unit they were logged in (pound dumbbells stay in pounds). Per-side sets ("12 each side") are logged once with per_side true, not as two sets.
- Bodyweight movements are measured "bodyweight" (any weight is added load); assisted machines are "assisted" (the weight is the assistance); holds are "timed"; carries, runs and rows are "distance".
- Dates are in the user's time zone, given in the context with each message. "Today", "yesterday" and "on the 6th" mean their calendar.
- If a request is ambiguous in a way that changes what gets logged, ask one short question instead of guessing.

How to write:
- Short and plain. One to three sentences is usually right; they may be mid-set. No lists unless they ask for one, no headings, no emoji.
- Numbers the way a lifter says them: "60 kg × 7", "3 × 45 s", "20 kg × 12 each side".
- Don't flatter, don't cheerlead, don't pad. Honest numbers over flattering ones.`

// contextText tells Milo when and where the user is; the first turn also gets an overview of their log.
func (t *turn) contextText(ctx context.Context, first bool) (string, error) {
	local := t.now.In(t.loc)
	var b strings.Builder
	b.WriteString("<context>\n")
	fmt.Fprintf(&b, "Now: %s, time zone %s.\n", local.Format("Monday 2 January 2006, 15:04"), t.loc.String())
	if sc := t.mctx.GetScreen(); sc != "" {
		fmt.Fprintf(&b, "Screen: %s.\n", sc)
	}
	if id, err := uuid.Parse(t.mctx.GetWorkoutId()); err == nil {
		if w, err := t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: id, UserID: t.userID}); err == nil {
			state := "finished"
			if w.EndedAt == nil {
				state = "still running (the open workout)"
			}
			fmt.Fprintf(&b, "Workout on screen: %q (workout_id %s), started %s, %s.\n",
				nameOr(w.Name, "Workout"), w.ID, t.day(w.StartedAt)+" "+w.StartedAt.In(t.loc).Format("15:04"), state)
		} else {
			fmt.Fprintf(&b, "Workout on screen: workout_id %s (not synced yet).\n", id)
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

	b.WriteString("\n<log_overview>\n")
	if kg := t.bodyKg(ctx); kg > 0 {
		fmt.Fprintf(&b, "Latest body weight: %s kg.\n", trimNum(fmt.Sprintf("%.1f", kg)))
	}
	used, err := t.s.q.UsedExercises(ctx, t.userID)
	if err != nil {
		return "", err
	}
	if len(used) > 0 {
		b.WriteString("Exercises they have logged or made, most used first (name | exercise_id | times | measurement | other names):\n")
		for i, e := range used {
			if i == 150 {
				fmt.Fprintf(&b, "…and %d more (use search_exercises).\n", len(used)-150)
				break
			}
			fmt.Fprintf(&b, "- %s | %s | %d | %s", e.Name, e.ID, e.Times, e.Measurement)
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
	recent, err := t.s.q.RecentWorkouts(ctx, query.RecentWorkoutsParams{UserID: t.userID, Limit: 3})
	if err != nil {
		return "", err
	}
	details, err := t.loadDetails(ctx, recent)
	if err != nil {
		return "", err
	}
	if len(details) > 0 {
		b.WriteString("Their last workouts:\n")
		for _, d := range details {
			parts := make([]string, 0, len(d.exercises))
			for _, e := range d.exercises {
				parts = append(parts, e.row.ExerciseName+" "+exerciseSummary(e.row.Measurement, e.sets))
			}
			fmt.Fprintf(&b, "- %s, %s (workout_id %s): %s\n", nameOr(d.w.Name, "Workout"), t.day(d.w.StartedAt), d.w.ID, strings.Join(parts, "; "))
		}
	}
	b.WriteString("</log_overview>")
	return b.String(), nil
}

var statusText = map[string]string{
	"applied":   "applied it",
	"dismissed": "dismissed it",
	"undone":    "undid it",
	"pending":   "hasn't acted on it",
}

// cardUpdates reports what the user did with cards since Milo last spoke.
func (t *turn) cardUpdates(ctx context.Context) (string, error) {
	rows, err := t.s.q.UnreportedCards(ctx, t.convID)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<card_updates>\n")
	for _, r := range rows {
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
