package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/invopop/jsonschema"
)

// A tool is a name, a description and a function taking a typed input. The
// input's JSON schema is generated from its struct, so the two can't drift.
type tool struct {
	param anthropic.ToolUnionParam
	run   func(t *turn, ctx context.Context, input []byte) (any, error)
}

func newTool[In any](name, description string, fn func(t *turn, ctx context.Context, in In) (any, error)) tool {
	p := anthropic.ToolParam{Name: name, Description: anthropic.String(description), InputSchema: schemaOf[In]()}
	return tool{
		param: anthropic.ToolUnionParam{OfTool: &p},
		run: func(t *turn, ctx context.Context, raw []byte) (any, error) {
			var in In
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, badInput("the input isn't valid for %s: %v", name, err)
			}
			return fn(t, ctx, in)
		},
	}
}

var reflector = jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true, Anonymous: true}

func schemaOf[In any]() anthropic.ToolInputSchemaParam {
	raw, err := json.Marshal(reflector.Reflect(new(In)))
	if err != nil {
		panic(err)
	}
	var s struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		panic(err)
	}
	return anthropic.ToolInputSchemaParam{Properties: s.Properties, Required: s.Required}
}

// Milo's tools. Their names say what happens: a plain verb is done on the phone
// straight away (with Undo), propose_ shows a card that waits for a tap, show_
// only displays, and the rest read.
//
// The order is part of the cached prompt; keep it stable.
var tools = []tool{
	// Reading.
	newTool("search_exercises",
		"Find exercises in the user's library (their own and the built-in ones) by name, alias or a short description. Returns ids, how each is measured and when it was last done.",
		(*turn).searchExercises),
	newTool("get_workouts",
		"Read workouts with their exercises and sets, including the ids that edits need. Pass workout_ids, or a date, or a date range (dates are YYYY-MM-DD in the user's time zone). With none, returns the most recent ones.",
		(*turn).getWorkouts),
	newTool("exercise_history",
		"Read one exercise's sessions, newest first, with each session's sets, top set, volume and estimated 1RM, plus the new bests over time.",
		(*turn).exerciseHistory),
	newTool("training_summary",
		"Add up a stretch of training in one read: workouts and sets per week, volume, sets per muscle, and the exercises done most. Use it for questions about consistency, balance or a whole month, instead of reading workouts one by one.",
		(*turn).trainingSummary),
	newTool("list_templates", "Read the user's templates and the exercises in each.", (*turn).listTemplates),
	newTool("get_profile",
		"Read the user's settings (display unit, default rest, height, birth date, sex) and their recent body weight and body fat readings.",
		(*turn).getProfile),

	// Done straight away, with Undo.
	newTool("start_workout",
		"Start a workout now: empty, from a template, as a repeat of a past workout, or with a list of exercises. Each exercise arrives with its sets from last time, unticked, ready to tick off. Only one workout runs at a time.",
		(*turn).startWorkout),
	newTool("add_exercises",
		"Add exercises to the running workout, each with its sets from last time, unticked. For when they are about to do something; if they tell you the sets they did, use log_sets instead.",
		(*turn).addExercises),
	newTool("log_sets",
		"Log sets the user did of a known exercise. In a running workout this ticks off the sets waiting there, in order, then adds more; the exercise is added if it isn't in the workout. Use new_workout for a session that already happened and isn't in the log (then reuse the returned workout_id for its other exercises).",
		(*turn).logSets),
	newTool("finish_workout",
		"Finish the running workout. Sets never ticked are dropped, and it is saved to Apple Health if they use that. Only when they say they are done.",
		(*turn).finishWorkout),
	newTool("log_body", "Log a body weight or body fat reading.", (*turn).logBody),
	newTool("update_settings",
		"Change the user's settings: the unit totals and charts are shown in, the default rest timer, height, birth date, sex.",
		(*turn).updateSettings),
	newTool("set_exercise_preferences",
		"Set the unit one exercise is logged in from now on, or its own rest timer. Works for built-in exercises too.",
		(*turn).setExercisePreferences),

	// Proposed: a card that waits for a tap.
	newTool("propose_new_exercise",
		"Propose a new exercise for a movement the user's library doesn't have, optionally with the sets they did (added to a workout). Shown as a card the user adds or edits; it is marked Approximate until they confirm it.",
		(*turn).proposeNewExercise),
	newTool("propose_change",
		"Propose edits to workouts already in the log, as one before/after card: fix, add or remove sets; remove, swap, move, note or superset an exercise; rename a workout, change its note or its times. Read the workout first for the ids.",
		(*turn).proposeChange),
	newTool("propose_template",
		"Propose a new template, or a change to one they have (pass template_id): an ordered list of exercises with no numbers. Shown as a card the user saves.",
		(*turn).proposeTemplate),
	newTool("propose_exercise_change",
		"Propose a change to one of the user's own exercises: its name, equipment, muscles, how it is counted, confirming an Approximate one, or merging it into another exercise it turned out to be.",
		(*turn).proposeExerciseChange),
	newTool("propose_delete",
		"Propose deleting a workout (or discarding the running one), a template, one of the user's own exercises, or a body reading. Shown as a card with a red button.",
		(*turn).proposeDelete),

	// Showing.
	newTool("show_chart",
		"Show a chart over time: one exercise's top_set (heaviest set), volume or e1rm (estimated one-rep max), or the user's body_weight.",
		(*turn).showChart),
	newTool("show_workout", "Show one workout as a card the user can open.", (*turn).showWorkout),
}

var (
	toolParams = func() []anthropic.ToolUnionParam {
		out := make([]anthropic.ToolUnionParam, len(tools))
		for i, t := range tools {
			out[i] = t.param
		}
		return out
	}()
	toolsByName = func() map[string]tool {
		out := map[string]tool{}
		for _, t := range tools {
			out[t.param.OfTool.Name] = t
		}
		return out
	}()
)

// toolError is a problem with the model's input; its message goes back to the model.
type toolError struct{ msg string }

func (e toolError) Error() string { return e.msg }

func badInput(format string, args ...any) error {
	return toolError{msg: fmt.Sprintf(format, args...)}
}

// runTool runs one tool call and returns its result for the model.
func (t *turn) runTool(ctx context.Context, name string, raw []byte) (string, bool) {
	tl, ok := toolsByName[name]
	if !ok {
		return fmt.Sprintf("There is no tool called %q.", name), true
	}
	out, err := tl.run(t, ctx, raw)
	if err != nil {
		var te toolError
		if errors.As(err, &te) {
			return te.msg, true
		}
		slog.Error("agent tool failed", "tool", name, "conversation", t.convID, "err", err)
		return "That tool failed on the server. Try another way, or tell the user it didn't work.", true
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "Couldn't encode the result.", true
	}
	return string(b), false
}
