package agent

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type chartInput struct {
	Metric     string `json:"metric" jsonschema:"enum=top_set,enum=volume,enum=e1rm,enum=body_weight"`
	ExerciseID string `json:"exercise_id,omitempty" jsonschema_description:"The exercise to chart; not needed for body_weight."`
	Caption    string `json:"caption,omitempty" jsonschema_description:"One line under the chart saying what it shows."`
}

var metricTitles = map[string]string{"top_set": "top set", "volume": "volume", "e1rm": "estimated 1RM"}

// showChart charts one exercise, or body weight, over time in the user's display unit.
func (t *turn) showChart(ctx context.Context, in chartInput) (any, error) {
	if in.Metric == "body_weight" {
		return t.showBodyWeight(ctx, in.Caption)
	}
	title, ok := metricTitles[in.Metric]
	if !ok {
		return nil, badInput("metric must be top_set, volume, e1rm or body_weight")
	}
	id, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	ex, sessions, err := t.sessions(ctx, id, 60)
	if err != nil {
		return nil, err
	}
	body := t.bodyKg(ctx)
	chart := &milo.Chart{Title: ex.Name + ", " + title, Metric: in.Metric, Unit: rows.Parse[milo.WeightUnit](t.displayUnit), Caption: in.Caption}
	for _, s := range slices.Backward(sessions) { // oldest first
		sum := maths.Summarize(setInputs(ex.Measurement, s.sets), body)
		p := &milo.ChartPoint{At: ts(s.at)}
		switch {
		case in.Metric == "top_set" && sum.HasTop:
			p.Value = displayKg(sum.TopLoadKg, t.displayUnit)
			p.Label = fmt.Sprintf("%s %s × %d", maths.FormatTenths(p.Value), t.displayUnit, sum.TopReps)
		case in.Metric == "volume":
			p.Value = displayKg(sum.VolumeKg, t.displayUnit)
			p.Label = kgText(sum.VolumeKg, t.displayUnit)
		case in.Metric == "e1rm" && sum.HasE1RM:
			p.Value = displayKg(sum.E1RMKg, t.displayUnit)
			p.Label = maths.FormatTenths(p.Value) + " " + t.displayUnit
		default:
			continue
		}
		chart.Points = append(chart.Points, p)
	}
	if len(chart.Points) == 0 {
		return nil, badInput("no %s data for %s yet", title, ex.Name)
	}
	best := slices.MaxFunc(chart.Points, func(a, b *milo.ChartPoint) int { return cmp.Compare(a.Value, b.Value) })
	return t.emitChart(ctx, &milo.Card{Title: chart.Title, ExerciseId: id.String(), Chart: chart, Subtitle: best.Label + " on " + t.day(best.At.AsTime())})
}

// showBodyWeight charts the last year of body weight readings.
func (t *turn) showBodyWeight(ctx context.Context, caption string) (any, error) {
	readings, err := t.s.q.BodyMeasurementsFor(ctx, query.BodyMeasurementsForParams{UserID: t.userID, Limit: 400})
	if err != nil {
		return nil, err
	}
	chart := &milo.Chart{Title: "Body weight", Metric: "body_weight", Unit: rows.Parse[milo.WeightUnit](t.displayUnit), Caption: caption}
	for _, m := range slices.Backward(readings) { // oldest first
		if m.WeightKg != nil && m.MeasuredAt.After(t.now.AddDate(-1, 0, 0)) {
			chart.Points = append(chart.Points, &milo.ChartPoint{At: ts(m.MeasuredAt), Value: displayKg(*m.WeightKg, t.displayUnit), Label: weightIn(*m.WeightKg, t.displayUnit)})
		}
	}
	if len(chart.Points) == 0 {
		return nil, badInput("no body weight readings in the last year")
	}
	latest := chart.Points[len(chart.Points)-1]
	return t.emitChart(ctx, &milo.Card{Title: chart.Title, Chart: chart, Subtitle: latest.Label + " on " + t.day(latest.At.AsTime())})
}

func (t *turn) emitChart(ctx context.Context, card *milo.Card) (any, error) {
	card.Kind = milo.CardKind_CARD_KIND_CHART
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "points": len(card.Chart.Points), "headline": card.Subtitle}, nil
}

type workoutInput struct {
	WorkoutID string `json:"workout_id"`
}

// showWorkout shows one workout as a card the user can open.
func (t *turn) showWorkout(ctx context.Context, in workoutInput) (any, error) {
	id, err := parseUUID(in.WorkoutID, "workout_id")
	if err != nil {
		return nil, err
	}
	w, err := t.userWorkout(ctx, id)
	if err != nil {
		return nil, err
	}
	details, err := t.loadDetails(ctx, []query.Workout{w})
	if err != nil {
		return nil, err
	}
	d := details[0]
	subtitle := t.day(w.StartedAt)
	if mins := minutesBetween(w.StartedAt, w.EndedAt); mins > 0 {
		subtitle += fmt.Sprintf(", %d min", mins)
	}
	subtitle += fmt.Sprintf(", %d sets", d.doneSets())
	lines := make([]*milo.CardLine, len(d.exercises))
	for i, e := range d.exercises {
		lines[i] = &milo.CardLine{Label: e.row.ExerciseName, After: exerciseSummary(e.row.Measurement, e.sets)}
	}
	cardID, err := t.emitCard(ctx, &milo.Card{Kind: milo.CardKind_CARD_KIND_WORKOUT, Title: nameOr(w.Name, "Workout"), Subtitle: subtitle, Lines: lines, WorkoutId: id.String()})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": cardID}, nil
}
