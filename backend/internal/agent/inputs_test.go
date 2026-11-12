package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// useAll is one model message asking for several tools at once, as Claude does.
func useAll(t *testing.T, uses []toolUse) func(anthropic.MessageNewParams) (*anthropic.Message, error) {
	return func(anthropic.MessageNewParams) (*anthropic.Message, error) {
		blocks := make([]map[string]any, len(uses))
		for i, u := range uses {
			blocks[i] = map[string]any{"type": "tool_use", "id": "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", ""), "name": u.name, "input": u.input}
		}
		return message(t, "tool_use", blocks...), nil
	}
}

// toolResults is the text of every tool_result in a request's last message, in order;
// an error starts "ERROR: ".
func toolResults(t *testing.T, p anthropic.MessageNewParams) []string {
	t.Helper()
	raw, _ := json.Marshal(p.Messages[len(p.Messages)-1])
	var m struct {
		Content []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range m.Content {
		if c.Type != "tool_result" {
			continue
		}
		text := ""
		for _, b := range c.Content {
			text += b.Text
		}
		if c.IsError {
			text = "ERROR: " + text
		}
		out = append(out, text)
	}
	return out
}

// ownExercise syncs an exercise the user made.
func ownExercise(t *testing.T, env *testkit.API, ctx context.Context, name string, m milo.Measurement) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := env.Sync.Sync(ctx, &milo.SyncRequest{Changes: []*milo.Change{{Row: &milo.Change_Exercise{Exercise: &milo.Exercise{
		Id: id.String(), Name: name, Measurement: m, PrimaryMuscles: []string{"obliques"}, UpdatedAt: timestamppb.Now(),
	}}}}}); err != nil {
		t.Fatal(err)
	}
	return id
}

// A tool given something it can't use tells Milo what was wrong and how to put it
// right, so Milo can try again in the same turn. Each case is a mistake a model makes.
func TestToolsTellMiloWhatWasWrong(t *testing.T) {
	t.Parallel()
	model := &fakeModel{}
	env := testkit.NewAPI(t, config.Config{}, model)
	ctx, _ := env.SignUp(t, "hishaam")
	bench, squat, plank := builtin(t, env, "Bench Press (Barbell)"), builtin(t, env, "Squat (Barbell)"), builtin(t, env, "Plank")
	finished, finishedSets := shoulders(t, env, ctx) // 6 Oct 2026, 07:00 to 08:10 UTC
	runningID, benchWE, _ := running(t, env, ctx)    // started half an hour ago
	mine := ownExercise(t, env, ctx, "Landmine Rotation", milo.Measurement_MEASUREMENT_REPS_LOAD)

	local := func(d time.Duration) string { return time.Now().UTC().Add(d).Format("2006-01-02T15:04") }
	newExercise := func(name, measurement string, met float64, muscles ...string) map[string]any {
		return map[string]any{"name": name, "measurement": measurement, "met": met, "primary_muscles": muscles}
	}
	cases := []struct {
		tool  string
		input map[string]any
		want  string
	}{
		// Ids that don't resolve.
		{"log_sets", map[string]any{"exercise_id": "bench", "sets": []any{map[string]any{"reps": 5}}}, `exercise_id "bench" is not a valid id`},
		{"log_sets", map[string]any{"exercise_id": uuid.NewString(), "sets": []any{map[string]any{"reps": 5}}}, "search first, or propose a new exercise"},
		{"add_exercises", map[string]any{"workout_id": uuid.NewString(), "exercise_ids": []string{bench.String()}}, "in this user's log"},
		{"propose_template", map[string]any{"template_id": uuid.NewString(), "name": "Pull"}, "list_templates shows the ones they have"},
		{"propose_change", map[string]any{"title": "Fix", "exercises": []any{map[string]any{"op": "remove", "workout_exercise_id": uuid.NewString()}}}, "no workout exercise"},
		{"propose_change", map[string]any{"title": "Fix", "sets": []any{map[string]any{"op": "update", "set_id": uuid.NewString()}}}, "no set"},
		{"propose_delete", map[string]any{"what": "body_reading", "id": uuid.NewString()}, "get_profile lists them"},
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": "lots"}, "the input isn't valid for log_sets"},

		// Running a workout.
		{"start_workout", map[string]any{}, "a workout is already running"},
		{"add_exercises", map[string]any{"exercise_ids": []string{}}, "exercise_ids can't be empty"},
		{"add_exercises", map[string]any{"exercise_ids": []string{bench.String()}, "superset": true}, "a superset needs at least two exercises"},
		{"add_exercises", map[string]any{"workout_id": finished.String(), "exercise_ids": []string{bench.String()}}, "use log_sets with its workout_id"},
		{"finish_workout", map[string]any{"workout_id": finished.String()}, "is already finished"},
		{"finish_workout", map[string]any{"ended_at": "yesterday"}, `ended_at "yesterday" must look like 2026-11-09T18:30`},
		{"finish_workout", map[string]any{"ended_at": local(-2 * time.Hour)}, "ended_at must be after the start"},

		// Logging sets.
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []any{}}, "sets can't be empty"},
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []any{map[string]any{"weight": "forty", "reps": 8}}}, "must be a plain number like 20 or 22.5"},
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []any{map[string]any{"weight": "20", "unit": "stone"}}}, "unit must be kg or lb"},
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []any{map[string]any{"reps": 5000}}}, "reps 5000 is out of range"},
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []any{map[string]any{"reps": 5, "kind": "rest-pause"}}}, "kind must be normal, warmup, drop or failure"},
		{"log_sets", map[string]any{"exercise_id": bench.String(), "sets": []any{map[string]any{"reps": 5}},
			"new_workout": map[string]any{"name": "Push", "started_at": "2026-11-01T10:00", "ended_at": "2026-11-01T09:00"}}, "ended_at must be after started_at"},

		// Body, settings and an exercise's own settings.
		{"log_body", map[string]any{}, "give a weight, a body fat percentage, or both"},
		{"log_body", map[string]any{"weight": "8"}, "doesn't look like a body weight"},
		{"log_body", map[string]any{"body_fat_pct": 90}, "body fat 90.0% is out of range"},
		{"log_body", map[string]any{"weight": "80", "measured_at": local(48 * time.Hour)}, "a reading can't be in the future"},
		{"update_settings", map[string]any{"display_unit": "stone"}, "display_unit must be kg or lb"},
		{"update_settings", map[string]any{"rest_default_s": 5}, "rest_default_s must be between 10 and 900"},
		{"update_settings", map[string]any{"height_cm": 20}, "height_cm must be between 50 and 260"},
		{"update_settings", map[string]any{"birth_date": "2099-01-01"}, "must be a past date"},
		{"update_settings", map[string]any{"display_unit": "kg"}, "nothing here differs from their current settings"},
		{"set_exercise_preferences", map[string]any{"exercise_id": bench.String(), "rest_s": 5}, "rest_s must be between 10 and 900 seconds"},
		{"set_exercise_preferences", map[string]any{"exercise_id": bench.String()}, "is already set that way"},

		// Changing what was logged.
		{"propose_change", map[string]any{"title": "Nothing"}, "these edits change nothing"},
		{"propose_change", map[string]any{"title": "Fix", "workouts": []any{map[string]any{"workout_id": runningID.String(), "ended_at": local(0)}}}, "finish_workout ends it"},
		{"propose_change", map[string]any{"title": "Fix", "workouts": []any{map[string]any{"workout_id": finished.String(), "started_at": "2026-10-06T09:00"}}}, "the workout would end before it starts"},
		{"propose_change", map[string]any{"title": "Fix", "workouts": []any{map[string]any{"workout_id": finished.String(), "started_at": local(48 * time.Hour), "ended_at": local(49 * time.Hour)}}}, "a workout can't start in the future"},
		{"propose_change", map[string]any{"title": "Fix", "exercises": []any{map[string]any{"op": "swap", "workout_exercise_id": benchWE.String()}}}, "exercise op must be update or remove"},
		{"propose_change", map[string]any{"title": "Fix", "exercises": []any{map[string]any{"op": "update", "workout_exercise_id": benchWE.String(), "exercise_id": plank.String()}}}, "is counted differently from"},
		{"propose_change", map[string]any{"title": "Fix", "exercises": []any{map[string]any{"op": "update", "workout_exercise_id": benchWE.String(), "position": 9}}}, "position 9 is outside the workout's 2 exercises"},
		{"propose_change", map[string]any{"title": "Fix", "sets": []any{map[string]any{"op": "update", "set_id": finishedSets[0].String(), "kind": "giant"}}}, "kind must be normal, warmup, drop or failure"},
		{"propose_change", map[string]any{"title": "Fix", "sets": []any{map[string]any{"op": "copy", "set_id": finishedSets[0].String()}}}, "set op must be update, delete or add"},
		{"propose_change", map[string]any{"title": "Fix", "sets": []any{map[string]any{"op": "update", "set_id": finishedSets[0].String(), "weight": "heavy"}}}, `weight "heavy" must be a plain number`},

		// The exercise library and templates.
		{"propose_new_exercise", newExercise("", "reps_load", 4, "chest"), "name is required"},
		{"propose_new_exercise", newExercise("Cable Woodchop", "swings", 4, "obliques"), "measurement must be one of"},
		{"propose_new_exercise", newExercise("Cable Woodchop", "reps_load", 40, "obliques"), "met 40.0 is out of range"},
		{"propose_new_exercise", newExercise("Cable Woodchop", "reps_load", 4), "primary_muscles needs at least one muscle"},
		{"propose_new_exercise", newExercise("bench press (barbell)", "reps_load", 4, "chest"), "already exists"},
		{"propose_exercise_change", map[string]any{"exercise_id": bench.String(), "name": "My bench"}, "is built in and can't be changed"},
		{"propose_exercise_change", map[string]any{"exercise_id": mine.String()}, "these edits change nothing about Landmine Rotation"},
		{"propose_exercise_change", map[string]any{"exercise_id": mine.String(), "met": 40}, "met 40.0 is out of range"},
		{"propose_exercise_change", map[string]any{"exercise_id": mine.String(), "merge_into": mine.String()}, "merge_into is the same exercise"},
		{"propose_exercise_change", map[string]any{"exercise_id": mine.String(), "merge_into": plank.String()}, "are counted differently, so their sets can't be merged"},
		{"propose_template", map[string]any{"name": ""}, "a template needs a name and at least one exercise"},
		{"propose_delete", map[string]any{"what": "template", "ids": []string{uuid.NewString(), uuid.NewString()}}, "ids is for workouts"},
		{"propose_delete", map[string]any{"what": "plan", "id": uuid.NewString()}, "what must be workout, template, exercise or body_reading"},
		{"propose_delete", map[string]any{"what": "exercise", "id": bench.String()}, "is built in and can't be deleted"},

		// Reading and showing.
		{"search_exercises", map[string]any{"query": "  "}, "query can't be empty"},
		{"list_workouts", map[string]any{"date_from": "2026-11-01", "date_to": "2026-10-01"}, "date_from is after date_to"},
		{"list_workouts", map[string]any{"date_from": "1 Nov"}, `date "1 Nov" must be YYYY-MM-DD`},
		{"show_chart", map[string]any{"metric": "speed", "exercise_id": bench.String()}, "metric must be top_set, volume, e1rm or body_weight"},
		{"show_chart", map[string]any{"metric": "top_set", "exercise_id": bench.String(), "compare_with": []string{squat.String(), plank.String(), mine.String()}}, "compare_with takes at most two exercises"},
		{"show_chart", map[string]any{"metric": "top_set", "exercise_id": squat.String()}, "data for Squat (Barbell) in those dates"},
		{"show_chart", map[string]any{"metric": "body_weight"}, "no body weight readings in those dates"},
		{"show_training", map[string]any{"metric": "laps", "date_from": "2026-10-01", "date_to": "2026-10-31"}, "metric must be workouts, sets, volume or minutes"},
		{"show_training", map[string]any{"metric": "workouts", "date_from": "2020-01-01", "date_to": "2020-02-01"}, "no finished workouts from"},
		{"show_muscles", map[string]any{"date_from": "2020-01-01", "date_to": "2020-02-01"}, "no finished workouts from"},
		{"show_progress", map[string]any{"date_from": "2026-10-01", "date_to": "2026-10-31"}, "no lift was done twice"},
	}

	uses := make([]toolUse, len(cases))
	for i, c := range cases {
		uses[i] = toolUse{c.tool, c.input}
	}
	first := len(model.calls)
	model.then(useAll(t, uses), say(t, "Done."))
	ev := send(t, env, ctx, "", "do it", &milo.MiloContext{Timezone: "UTC"})
	if len(ev.errs) > 0 || len(ev.cards) > 0 {
		t.Fatalf("errors %v, cards %v: every call should have been refused", ev.errs, ev.cards)
	}
	results := toolResults(t, model.calls[first+1])
	for i, c := range cases {
		if !strings.HasPrefix(results[i], "ERROR:") || !strings.Contains(results[i], c.want) {
			t.Errorf("%s %v:\n got  %s\n want an error saying %q", c.tool, c.input, results[i], c.want)
		}
	}
}
