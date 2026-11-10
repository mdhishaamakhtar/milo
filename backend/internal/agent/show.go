package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type chartInput struct {
	ExerciseID string `json:"exercise_id"`
	Metric     string `json:"metric" jsonschema:"enum=top_set,enum=volume,enum=e1rm"`
	Caption    string `json:"caption,omitempty"`
}

var metricTitles = map[string]string{"top_set": "top set", "volume": "volume", "e1rm": "estimated 1RM"}

// showChart charts one exercise over time, in the user's display unit.
func (t *turn) showChart(ctx context.Context, in chartInput) (any, error) {
	id, err := parseUUID(in.ExerciseID, "exercise_id")
	if err != nil {
		return nil, err
	}
	title, ok := metricTitles[in.Metric]
	if !ok {
		return nil, badInput("metric must be top_set, volume or e1rm")
	}
	ex, sessions, err := t.sessions(ctx, id, 60)
	if err != nil {
		return nil, err
	}
	body := t.bodyKg(ctx)
	chart := &milo.Chart{Title: ex.Name + ", " + title, Metric: in.Metric, Unit: rows.Parse[milo.WeightUnit](t.displayUnit), Caption: in.Caption}
	var best *milo.ChartPoint
	for i := len(sessions) - 1; i >= 0; i-- { // oldest first
		sum := maths.Summarize(setInputs(ex.Measurement, sessions[i].sets), body)
		p := &milo.ChartPoint{At: ts(sessions[i].at)}
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
		if best == nil || p.Value > best.Value {
			best = p
		}
	}
	if best == nil {
		return nil, badInput("no %s data for %s yet", title, ex.Name)
	}
	card := &milo.Card{
		Kind: milo.CardKind_CARD_KIND_CHART, Title: chart.Title, ExerciseId: id.String(), Chart: chart,
		Subtitle: best.Label + " on " + t.day(best.At.AsTime()),
	}
	cardID, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": cardID, "points": len(chart.Points), "best": card.Subtitle}, nil
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
	w, err := t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: id, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, badInput("no workout %s in this user's log", id)
	}
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
