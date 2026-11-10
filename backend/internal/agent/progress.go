package agent

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
)

type progressInput struct {
	Rank     string `json:"rank,omitempty" jsonschema:"enum=improved,enum=records" jsonschema_description:"improved (the default) ranks lifts by how far the top set moved over the dates. records lists the best set on each lift."`
	DateFrom string `json:"date_from,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to twelve weeks before date_to for improved, and to the whole log for records."`
	DateTo   string `json:"date_to,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to today."`
}

// mark is one exercise's top set in one session: the heaviest working set,
// more reps breaking a tie.
type mark struct {
	at   time.Time
	set  query.Set
	text string  // as it was logged: "60 kg × 7", "50 lb × 12"
	kg   float64 // effective load
	reps int
	// The weight is help, as on an assisted pull-up: less of it is the harder set.
	assisted bool
}

// strength compares top sets that differ in both load and reps. It is Epley's
// formula without the rep limit, so it is only good for ranking, never shown.
func (m mark) strength() float64 { return m.kg * (1 + float64(m.reps)/30) }

// lift is one exercise over a stretch.
type lift struct {
	id          uuid.UUID
	name        string
	marks       []mark // oldest first
	first, best mark
	latest      mark
	change      float64 // of strength, first to the set shown after it
	trend       int32
}

const (
	progressShown = 6  // rows on the card
	progressTold  = 16 // rows Milo gets
)

// showProgress puts every lift of a stretch side by side on one card, ranked,
// and hands Milo the same table. It is the one read for questions that span
// lifts: what improved, what stalled, what the bests are.
func (t *turn) showProgress(ctx context.Context, in progressInput) (any, error) {
	records := in.Rank == "records"
	days := 84
	if records && in.DateFrom == "" {
		in.DateFrom = "2000-01-01"
	}
	from, to, err := t.span(in.DateFrom, in.DateTo, days)
	if err != nil {
		return nil, err
	}
	details, err := t.finishedBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	if len(details) == 0 {
		return nil, badInput("no finished workouts from %s to %s", t.day(from), t.day(to))
	}
	if first := details[0].w.StartedAt; first.After(from) {
		from = first // the log starts later than the dates asked for
	}

	body := t.bodyKg(ctx)
	byID := map[uuid.UUID]*lift{}
	var lifts []*lift
	for _, d := range details {
		for _, e := range d.exercises {
			m, ok := topMark(e, d.w.StartedAt, body)
			if !ok {
				continue
			}
			l := byID[e.row.ExerciseID]
			if l == nil {
				l = &lift{id: e.row.ExerciseID, name: e.row.ExerciseName}
				byID[l.id] = l
				lifts = append(lifts, l)
			}
			l.marks = append(l.marks, m)
		}
	}
	for _, l := range lifts {
		// A first session far under the next is a false start (the wrong pin, trying
		// the machine), not where the lift began: counting from it would flatter.
		if len(l.marks) > 2 && l.marks[0].strength() < 0.6*l.marks[1].strength() {
			l.marks = l.marks[1:]
		}
		l.first, l.latest = l.marks[0], l.marks[len(l.marks)-1]
		l.best = slices.MaxFunc(l.marks, func(a, b mark) int { return cmp.Compare(a.strength(), b.strength()) })
	}

	card := &milo.Card{Kind: milo.CardKind_CARD_KIND_PROGRESS}
	var told []map[string]any
	if records {
		// The lifts done most come first: those are the ones a best means something on.
		slices.SortStableFunc(lifts, func(a, b *lift) int { return cmp.Compare(len(b.marks), len(a.marks)) })
		card.Title, card.Subtitle = "Best sets", "The lifts you do most"
		for i, l := range lifts[:min(len(lifts), progressTold)] {
			heaviest := slices.MaxFunc(l.marks, func(a, b mark) int { return cmp.Or(cmp.Compare(a.kg, b.kg), cmp.Compare(a.reps, b.reps)) })
			if i < progressShown {
				card.Lines = append(card.Lines, &milo.CardLine{Label: l.name, After: heaviest.text, Note: dateText(heaviest.at.In(t.loc), t.now.In(t.loc)), ExerciseId: l.id.String(), Spark: l.spark()})
			}
			told = append(told, map[string]any{"exercise": l.name, "exercise_id": l.id.String(), "sessions": len(l.marks), "heaviest": heaviest.text + " on " + t.day(heaviest.at)})
		}
	} else {
		lifts = slices.DeleteFunc(lifts, func(l *lift) bool { return len(l.marks) < 2 })
		if len(lifts) == 0 {
			return nil, badInput("no lift was done twice from %s to %s, so there is nothing to compare; try earlier dates", t.day(from), t.day(to))
		}
		up, down := 0, 0
		for _, l := range lifts {
			switch {
			case l.best.strength() > l.first.strength()*1.01:
				l.trend, l.change = 1, l.best.strength()/l.first.strength()-1
				up++
			case l.latest.strength() < l.first.strength()*0.97:
				l.trend, l.change = -1, l.latest.strength()/l.first.strength()-1
				down++
			}
		}
		slices.SortStableFunc(lifts, func(a, b *lift) int { return cmp.Compare(b.change, a.change) })
		card.Title = "Progress since " + dateText(from.In(t.loc), t.now.In(t.loc))
		card.Subtitle = fmt.Sprintf("%d up, %d steady, %d down", up, len(lifts)-up-down, down)
		for i, l := range lifts[:min(len(lifts), progressTold)] {
			after := l.best
			if l.trend <= 0 {
				after = l.latest
			}
			if i < progressShown {
				card.Lines = append(card.Lines, &milo.CardLine{
					Label: l.name, Before: l.first.text, After: after.text, Note: moved(l.first, after), Trend: l.trend,
					ExerciseId: l.id.String(), Spark: l.spark(),
				})
			}
			row := map[string]any{
				"exercise": l.name, "exercise_id": l.id.String(), "sessions": len(l.marks), "trend": [3]string{"down", "steady", "up"}[l.trend+1],
				"first": l.first.text + " on " + t.day(l.first.at), "best": l.best.text + " on " + t.day(l.best.at),
				"latest": l.latest.text + " on " + t.day(l.latest.at),
			}
			told = append(told, row)
		}
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"card_id": id, "from": t.day(from), "to": t.day(to), "lifts": told,
		"shown": fmt.Sprintf("The user sees the first %d as a card, each with a small chart. Say what it means in a sentence or two; don't repeat its numbers.", len(card.Lines)),
	}, nil
}

// topMark finds an exercise's top set in a session. Timed and distance work has none.
func topMark(e exerciseDetail, at time.Time, bodyKg float64) (mark, bool) {
	var top mark
	found := false
	for _, s := range e.sets {
		if !s.Done || s.Kind == maths.Warmup || s.Reps == nil || *s.Reps <= 0 {
			continue
		}
		kg, ok := maths.EffectiveLoadKg(e.row.Measurement, s.Weight, s.WeightUnit, bodyKg)
		if !ok {
			continue
		}
		if reps := int(*s.Reps); !found || kg > top.kg || (kg == top.kg && reps > top.reps) {
			top, found = mark{at: at, set: s, text: setTextRow(e.row.Measurement, s), kg: kg, reps: reps}, true
			if e.row.Measurement == maths.Assisted && s.Weight != "" {
				top.assisted, top.text = true, fmt.Sprintf("%d reps, %s help", reps, weightText(s.Weight, s.WeightUnit))
			}
		}
	}
	return top, found
}

// moved says how a top set changed, in the terms it was logged in: "+15 kg", "+3 reps", "−5 lb".
func moved(from, to mark) string {
	sign := func(v float64) string {
		if v < 0 {
			return "−"
		}
		return "+"
	}
	if from.set.WeightUnit == to.set.WeightUnit && from.set.Weight != to.set.Weight {
		a, okA := maths.ToKg(from.set.Weight, "kg") // the numbers as typed, whatever the unit
		b, okB := maths.ToKg(to.set.Weight, "kg")
		if okA && okB {
			text := fmt.Sprintf("%s%s %s", sign(b-a), maths.FormatTenths(max(b-a, a-b)), to.set.WeightUnit)
			if to.assisted {
				text += " help"
			}
			return text
		}
	}
	if d := to.kg - from.kg; d > 0.05 || d < -0.05 {
		return fmt.Sprintf("%s%s kg", sign(d), maths.FormatTenths(max(d, -d)))
	}
	if d := to.reps - from.reps; d != 0 {
		return fmt.Sprintf("%s%s", sign(float64(d)), count(max(d, -d), "rep", "reps"))
	}
	return "same"
}

// spark is the lift's sessions as a small line, oldest first.
func (l *lift) spark() []float64 {
	out := make([]float64, len(l.marks))
	for i, m := range l.marks {
		out[i] = m.strength()
	}
	return out
}
