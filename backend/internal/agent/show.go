package agent

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type chartInput struct {
	Metric      string   `json:"metric" jsonschema:"enum=top_set,enum=volume,enum=e1rm,enum=body_weight"`
	ExerciseID  string   `json:"exercise_id,omitempty" jsonschema_description:"The exercise to chart; not needed for body_weight."`
	CompareWith []string `json:"compare_with,omitempty" jsonschema_description:"Up to two more exercise_ids to draw on the same chart, to compare lifts."`
	DateFrom    string   `json:"date_from,omitempty" jsonschema_description:"YYYY-MM-DD. Chart from this day on; leave out for the whole history."`
	Caption     string   `json:"caption,omitempty" jsonschema_description:"One line under the chart, only for something its title and numbers don't already say."`
}

var metricTitles = map[string]string{"top_set": "top set", "volume": "volume", "e1rm": "estimated 1RM"}

// showChart charts one exercise, a few to compare, or body weight, over time in
// the user's display unit. The result gives Milo the same numbers to talk about.
func (t *turn) showChart(ctx context.Context, in chartInput) (any, error) {
	since := time.Time{}
	if in.DateFrom != "" {
		var err error
		if since, err = t.startOfDay(in.DateFrom); err != nil {
			return nil, err
		}
	}
	if in.Metric == "body_weight" {
		return t.showBodyWeight(ctx, since, in.Caption)
	}
	title, ok := metricTitles[in.Metric]
	if !ok {
		return nil, badInput("metric must be top_set, volume, e1rm or body_weight")
	}
	if len(in.CompareWith) > 2 {
		return nil, badInput("compare_with takes at most two exercises")
	}
	body := t.bodyKg(ctx)
	var series []*milo.ChartSeries
	var told []map[string]any
	for _, raw := range append([]string{in.ExerciseID}, in.CompareWith...) {
		id, err := parseUUID(raw, "exercise_id")
		if err != nil {
			return nil, err
		}
		ex, sessions, err := t.sessions(ctx, id, 60)
		if err != nil {
			return nil, err
		}
		line := &milo.ChartSeries{Name: ex.Name, ExerciseId: id.String()}
		for _, s := range slices.Backward(sessions) { // oldest first
			if s.at.Before(since) {
				continue
			}
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
			line.Points = append(line.Points, p)
		}
		if len(line.Points) == 0 {
			return nil, badInput("no %s data for %s in those dates", title, ex.Name)
		}
		series = append(series, line)
		told = append(told, t.lineSummary(ex.Name, line.Points))
	}

	chart := &milo.Chart{Metric: in.Metric, Unit: rows.Parse[milo.WeightUnit](t.displayUnit), Caption: in.Caption}
	card := &milo.Card{Chart: chart, ExerciseId: series[0].ExerciseId}
	if len(series) == 1 {
		best := slices.MaxFunc(series[0].Points, func(a, b *milo.ChartPoint) int { return cmp.Compare(a.Value, b.Value) })
		chart.Title, chart.Points = series[0].Name+", "+title, series[0].Points
		card.Subtitle = best.Label + " on " + t.day(best.At.AsTime())
	} else {
		names := make([]string, len(series))
		for i, s := range series {
			names[i] = shortTitle(s.Name)
		}
		chart.Title, chart.Series = strings.Join(names, " vs ")+", "+title, series
		card.ExerciseId = "" // no one exercise to open
	}
	card.Title = chart.Title
	return t.emitChart(ctx, card, map[string]any{"lines": told})
}

// lineSummary tells Milo what a charted line shows: where it began, its best and where it is now.
func (t *turn) lineSummary(name string, points []*milo.ChartPoint) map[string]any {
	at := func(p *milo.ChartPoint) string { return p.Label + " on " + t.day(p.At.AsTime()) }
	best := slices.MaxFunc(points, func(a, b *milo.ChartPoint) int { return cmp.Compare(a.Value, b.Value) })
	return map[string]any{"name": name, "points": len(points), "first": at(points[0]), "best": at(best), "latest": at(points[len(points)-1])}
}

// showBodyWeight charts body weight readings: the last year, or from a given day.
func (t *turn) showBodyWeight(ctx context.Context, since time.Time, caption string) (any, error) {
	readings, err := t.s.q.BodyMeasurementsFor(ctx, query.BodyMeasurementsForParams{UserID: t.userID, Limit: 400})
	if err != nil {
		return nil, err
	}
	if since.IsZero() {
		since = t.now.AddDate(-1, 0, 0)
	}
	chart := &milo.Chart{Title: "Body weight", Metric: "body_weight", Unit: rows.Parse[milo.WeightUnit](t.displayUnit), Caption: caption}
	for _, m := range slices.Backward(readings) { // oldest first
		if m.WeightKg != nil && m.MeasuredAt.After(since) {
			chart.Points = append(chart.Points, &milo.ChartPoint{At: ts(m.MeasuredAt), Value: displayKg(*m.WeightKg, t.displayUnit), Label: weightIn(*m.WeightKg, t.displayUnit)})
		}
	}
	if len(chart.Points) == 0 {
		return nil, badInput("no body weight readings in those dates")
	}
	latest := chart.Points[len(chart.Points)-1]
	card := &milo.Card{Title: chart.Title, Chart: chart, Subtitle: latest.Label + " on " + t.day(latest.At.AsTime())}
	return t.emitChart(ctx, card, map[string]any{"lines": []map[string]any{t.lineSummary("Body weight", chart.Points)}})
}

// shownNote is what Milo is told after a card that only displays.
const shownNote = "The user sees this as a card. Say what it means in a sentence or two; don't repeat its numbers."

// emitChart shows a chart card and hands Milo what it drew.
func (t *turn) emitChart(ctx context.Context, card *milo.Card, told map[string]any) (any, error) {
	card.Kind = milo.CardKind_CARD_KIND_CHART
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	told["card_id"], told["shown"] = id, shownNote
	return told, nil
}

type trainingChartInput struct {
	Metric   string `json:"metric" jsonschema:"enum=workouts,enum=sets,enum=volume,enum=minutes"`
	By       string `json:"by,omitempty" jsonschema:"enum=week,enum=month" jsonschema_description:"Defaults to week, or month for more than half a year."`
	DateFrom string `json:"date_from,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to twelve weeks before date_to."`
	DateTo   string `json:"date_to,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to today."`
	Caption  string `json:"caption,omitempty" jsonschema_description:"One line under the chart, only for something its title and numbers don't already say."`
}

// showTraining draws how much the user trained, week by week or month by month,
// under the totals for the stretch.
func (t *turn) showTraining(ctx context.Context, in trainingChartInput) (any, error) {
	from, to, err := t.span(in.DateFrom, in.DateTo, 84)
	if err != nil {
		return nil, err
	}
	tr, err := t.trainingBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	if tr.total.workouts == 0 {
		return nil, badInput("no finished workouts from %s to %s", t.day(from), t.day(to))
	}
	periods, every := tr.weeks, "week"
	if in.By == "month" || (in.By == "" && len(tr.weeks) > 26) {
		periods, every = tr.months, "month"
	}
	var value func(tally) (float64, string)
	var title string
	switch in.Metric {
	case "workouts":
		title, value = "Workouts a "+every, func(p tally) (float64, string) { return float64(p.workouts), count(p.workouts, "workout", "workouts") }
	case "sets":
		title, value = "Sets a "+every, func(p tally) (float64, string) { return float64(p.sets), count(p.sets, "set", "sets") }
	case "volume":
		title, value = "Volume a "+every, func(p tally) (float64, string) {
			return displayKg(p.volumeKg, t.displayUnit), kgText(p.volumeKg, t.displayUnit)
		}
	case "minutes":
		title, value = "Minutes a "+every, func(p tally) (float64, string) { return float64(p.minutes), fmt.Sprintf("%d min", p.minutes) }
	default:
		return nil, badInput("metric must be workouts, sets, volume or minutes")
	}
	chart := &milo.Chart{Title: title, Metric: in.Metric, Unit: rows.Parse[milo.WeightUnit](t.displayUnit), Caption: in.Caption, Bars: true}
	told := make([]map[string]any, len(periods))
	for i, p := range periods {
		v, label := value(p.tally)
		chart.Points = append(chart.Points, &milo.ChartPoint{At: ts(p.start), Value: v, Label: label})
		told[i] = map[string]any{"start": p.start.Format(time.DateOnly), in.Metric: label}
	}
	longest, _, current := tr.streaks()
	stats := []*milo.Stat{
		{Value: strconv.Itoa(tr.total.workouts), Label: plural(tr.total.workouts, "workout", "workouts")},
		{Value: trimNum(fmt.Sprintf("%.1f", float64(tr.total.workouts)/float64(len(tr.weeks)))), Label: "a week"},
		{Value: fmt.Sprintf("%d min", tr.total.minutes/tr.total.workouts), Label: "average"},
	}
	card := &milo.Card{Title: title, Subtitle: t.day(from) + " to " + t.day(to), Chart: chart, Stats: stats}
	return t.emitChart(ctx, card, map[string]any{
		"from": t.day(from), "to": t.day(to), "workouts": tr.total.workouts, "average_minutes": tr.total.minutes / tr.total.workouts,
		"volume": kgText(tr.total.volumeKg, t.displayUnit), "longest_run_of_weeks": longest, "current_run_of_weeks": current, every + "s": told,
	})
}

type musclesInput struct {
	DateFrom string `json:"date_from,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to four weeks before date_to."`
	DateTo   string `json:"date_to,omitempty" jsonschema_description:"YYYY-MM-DD. Defaults to today."`
}

// showMuscles draws the working sets each muscle got over a stretch, and lists
// the muscles trained in the months before it that got none.
func (t *turn) showMuscles(ctx context.Context, in musclesInput) (any, error) {
	from, to, err := t.span(in.DateFrom, in.DateTo, 28)
	if err != nil {
		return nil, err
	}
	tr, err := t.trainingBetween(ctx, from, to)
	if err != nil {
		return nil, err
	}
	earlier, err := t.trainingBetween(ctx, from.AddDate(0, -6, 0), from.AddDate(0, 0, -1))
	if err != nil {
		return nil, err
	}
	names := tr.musclesBySets()
	if len(names) == 0 {
		return nil, badInput("no finished workouts from %s to %s", t.day(from), t.day(to))
	}
	most := tr.muscles[names[0]].sets
	var lines []*milo.CardLine
	var told []map[string]any
	for _, m := range names {
		mt := tr.muscles[m]
		lines = append(lines, &milo.CardLine{Label: sentence(m), After: count(mt.sets, "set", "sets"), Share: float64(mt.sets) / float64(most)})
		told = append(told, map[string]any{"muscle": m, "sets": mt.sets, "last_trained": t.day(mt.last)})
	}
	// Trained before, not in this stretch: most recently dropped first. The card
	// names the ones that were a habit, not every muscle touched once.
	missed := slices.DeleteFunc(earlier.musclesBySets(), func(m string) bool { return tr.muscles[m] != nil })
	slices.SortFunc(missed, func(a, b string) int { return earlier.muscles[b].last.Compare(earlier.muscles[a].last) })
	shown := 0
	for _, m := range missed {
		mt := earlier.muscles[m]
		if mt.sets >= missedSets && shown < missedShown {
			lines = append(lines, &milo.CardLine{Label: sentence(m), After: "0 sets", Note: "last " + dateText(mt.last.In(t.loc), t.now.In(t.loc)), Trend: -1})
			shown++
		}
		told = append(told, map[string]any{"muscle": m, "sets": 0, "last_trained": t.day(mt.last), "sets_in_six_months_before": mt.sets})
	}
	card := &milo.Card{
		Kind: milo.CardKind_CARD_KIND_BREAKDOWN, Title: "Sets by muscle", Lines: lines,
		Subtitle: fmt.Sprintf("%s to %s, %s", t.day(from), t.day(to), count(tr.total.workouts, "workout", "workouts")),
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": shownNote, "from": t.day(from), "to": t.day(to), "workouts": tr.total.workouts, "muscles": told}, nil
}

// A muscle left out counts as missed on the card if it got this many sets in
// the six months before; the card lists at most missedShown of them.
const (
	missedSets  = 12
	missedShown = 6
)

// shortTitle drops the equipment in brackets: "Chest Press (Machine)" → "Chest Press".
func shortTitle(name string) string {
	if i := strings.Index(name, " ("); i > 0 {
		return name[:i]
	}
	return name
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
