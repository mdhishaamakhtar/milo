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

// The order is part of the cached prompt; keep it stable.
var tools = []tool{
	newTool("search_exercises",
		"Find exercises in the user's library (their own and the built-in ones) by name, alias or a short description. Returns ids, how each is measured and when it was last done.",
		(*turn).searchExercises),
	newTool("get_workouts",
		"Read workouts with their exercises and sets. Pass workout_ids, or a date, or a date range (dates are YYYY-MM-DD in the user's time zone). With none, returns the most recent ones.",
		(*turn).getWorkouts),
	newTool("exercise_history",
		"Read one exercise's sessions, newest first, with each session's sets, top set, volume and estimated 1RM, plus the new bests over time.",
		(*turn).exerciseHistory),
	newTool("list_templates", "Read the user's templates and the exercises in each.", (*turn).listTemplates),
	newTool("propose_new_exercise",
		"Propose a new exercise for a movement the user's library doesn't have, optionally with the sets they did (added to a workout). Shown as a card the user adds or edits; it is marked Approximate until they confirm it.",
		(*turn).proposeNewExercise),
	newTool("log_sets",
		"Log sets of a known exercise. They are applied straight away on the user's phone, with Undo. Use workout_id, or new_workout for a session that isn't in the log yet (then reuse the returned workout_id for its other exercises), or leave both out for the open workout.",
		(*turn).logSets),
	newTool("propose_change",
		"Propose edits to logged data as one before/after card: update_set, delete_set, add_set, rename_workout, update_exercise (the user's own exercises only). Nothing changes until the user applies it.",
		(*turn).proposeChange),
	newTool("propose_template",
		`Propose a template: an ordered list of exercises, supersets sharing a group label (4a and 4b both have group_label "4"). Shown as a draft card the user saves.`,
		(*turn).proposeTemplate),
	newTool("show_chart",
		"Show a chart of one exercise over time: top_set (heaviest set), volume, or e1rm (estimated one-rep max).",
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
