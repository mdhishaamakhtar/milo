package agent

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

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
