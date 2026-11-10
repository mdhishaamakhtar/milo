package milo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
)

// toolError is a problem with the model's input; its message goes back to the model.
type toolError struct{ msg string }

func (e toolError) Error() string { return e.msg }

func badInput(format string, args ...any) error {
	return toolError{msg: fmt.Sprintf(format, args...)}
}

// turn is one user message being answered: who, where, and what Milo has proposed so far.
type turn struct {
	s           *Service
	userID      uuid.UUID
	convID      uuid.UUID
	mctx        *milov1.MiloContext
	loc         *time.Location
	now         time.Time
	displayUnit string
	emit        func(*milov1.SendResponse) error

	// Workouts created by log_sets earlier in this turn, so later calls can add to them.
	newWorkouts map[uuid.UUID]*newWorkout
}

type newWorkout struct {
	name      string
	startedAt time.Time
	// exercise id → workout exercise id created in this turn
	exercises map[uuid.UUID]uuid.UUID
	position  int32
	setCounts map[uuid.UUID]int32
}

func obj(props map[string]any, required ...string) anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{Properties: props, Required: required}
}

func tool(name, description string, schema anthropic.ToolInputSchemaParam) anthropic.ToolUnionParam {
	t := anthropic.ToolParam{Name: name, Description: anthropic.String(description), InputSchema: schema}
	return anthropic.ToolUnionParam{OfTool: &t}
}

var (
	str     = map[string]any{"type": "string"}
	strList = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	num     = map[string]any{"type": "number"}
	integer = map[string]any{"type": "integer"}
	boolean = map[string]any{"type": "boolean"}

	setSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reps":       integer,
			"weight":     map[string]any{"type": "string", "description": `Exact weight as said, e.g. "20" or "22.5". Omit for none.`},
			"unit":       map[string]any{"type": "string", "enum": []string{"kg", "lb"}},
			"per_side":   map[string]any{"type": "boolean", "description": "Reps were done on each side."},
			"duration_s": integer,
			"distance_m": num,
		},
	}
	measurementEnum = map[string]any{"type": "string", "enum": []string{"reps_load", "bodyweight", "assisted", "timed", "distance"}}
)

var tools = []anthropic.ToolUnionParam{
	tool("search_exercises",
		"Find exercises in the user's library (their own and the built-in ones) by name, alias or a short description. Returns ids, how each is measured and when it was last done.",
		obj(map[string]any{"query": map[string]any{"type": "string", "description": `A few words, e.g. "landmine", "lat pulldown", "chest press".`}}, "query")),
	tool("get_workouts",
		"Read workouts with their exercises and sets. Pass workout_ids, or a date, or a date range (dates are YYYY-MM-DD in the user's time zone). With none, returns the most recent ones.",
		obj(map[string]any{
			"workout_ids": strList,
			"date":        str,
			"date_from":   str,
			"date_to":     str,
			"limit":       map[string]any{"type": "integer", "description": "At most 20. Default 5."},
		})),
	tool("exercise_history",
		"Read one exercise's sessions, newest first, with each session's sets, top set, volume and estimated 1RM, plus the new bests over time.",
		obj(map[string]any{"exercise_id": str, "limit": map[string]any{"type": "integer", "description": "Sessions to read, at most 40. Default 12."}}, "exercise_id")),
	tool("list_templates", "Read the user's templates and the exercises in each.", obj(map[string]any{})),
	tool("propose_new_exercise",
		"Propose a new exercise for a movement the user's library doesn't have, optionally with the sets they did (added to a workout). Shown as a card the user adds or edits; it is marked Approximate until they confirm it.",
		obj(map[string]any{
			"name":              map[string]any{"type": "string", "description": `A clear name, e.g. "Landmine Rotation".`},
			"aliases":           map[string]any{"type": "array", "items": str, "description": "Other names, including what the trainer called it."},
			"primary_muscles":   strList,
			"secondary_muscles": strList,
			"movement_pattern":  str,
			"equipment":         str,
			"measurement":       measurementEnum,
			"unilateral":        map[string]any{"type": "boolean", "description": "Done one side at a time and counted \"N each side\" (single-arm work, lunges, rotations). True whenever its sets are per_side."},
			"met":               map[string]any{"type": "number", "description": "Metabolic equivalent for calories, 2 to 12."},
			"description":       str,
			"works_summary":     map[string]any{"type": "string", "description": `For the card, e.g. "Obliques and core, some shoulders".`},
			"equipment_summary": map[string]any{"type": "string", "description": `For the card, e.g. "Barbell in a landmine".`},
			"movement_summary":  map[string]any{"type": "string", "description": `For the card, e.g. "Rotation, standing".`},
			"workout_id":        map[string]any{"type": "string", "description": "Workout to add the sets to. Defaults to the open workout."},
			"sets":              map[string]any{"type": "array", "items": setSchema},
		}, "name", "primary_muscles", "measurement", "unilateral", "met", "works_summary", "equipment_summary", "movement_summary")),
	tool("log_sets",
		"Log sets of a known exercise. They are applied straight away on the user's phone, with Undo. Use workout_id, or new_workout for a session that isn't in the log yet (then reuse the returned workout_id for its other exercises), or leave both out for the open workout.",
		obj(map[string]any{
			"exercise_id": str,
			"workout_id":  str,
			"new_workout": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":       str,
					"started_at": map[string]any{"type": "string", "description": "Local time, YYYY-MM-DDTHH:MM."},
					"ended_at":   map[string]any{"type": "string", "description": "Local time, YYYY-MM-DDTHH:MM."},
				},
				"required": []string{"name", "started_at"},
			},
			"sets": map[string]any{"type": "array", "items": setSchema},
		}, "exercise_id", "sets")),
	tool("propose_change",
		"Propose edits to logged data as one before/after card: update_set, delete_set, add_set, rename_workout, update_exercise (the user's own exercises only). Nothing changes until the user applies it.",
		obj(map[string]any{
			"title":    map[string]any{"type": "string", "description": `Card title, e.g. the exercise or workout name.`},
			"subtitle": map[string]any{"type": "string", "description": `e.g. "Shoulders, Tue 6 Oct".`},
			"edits": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"op":                  map[string]any{"type": "string", "enum": []string{"update_set", "delete_set", "add_set", "rename_workout", "update_exercise"}},
					"set_id":              str,
					"workout_exercise_id": str,
					"workout_id":          str,
					"exercise_id":         str,
					"reps":                integer,
					"weight":              str,
					"unit":                map[string]any{"type": "string", "enum": []string{"kg", "lb"}},
					"per_side":            boolean,
					"duration_s":          integer,
					"distance_m":          num,
					"done":                boolean,
					"name":                str,
					"measurement":         measurementEnum,
					"unilateral":          boolean,
					"equipment":           str,
					"primary_muscles":     strList,
					"met":                 num,
					"confirm":             map[string]any{"type": "boolean", "description": "For update_exercise: the user confirmed it, so it is no longer Approximate."},
				},
				"required": []string{"op"},
			}},
		}, "title", "edits")),
	tool("propose_template",
		"Propose a template: an ordered list of exercises, supersets sharing a group label (4a and 4b both have group_label \"4\"). Shown as a draft card the user saves.",
		obj(map[string]any{
			"name":  str,
			"notes": str,
			"items": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"exercise_id": str,
					"group_label": str,
					"note":        map[string]any{"type": "string", "description": `A word or two for the card, e.g. "fresh", "volume".`},
				},
				"required": []string{"exercise_id"},
			}},
		}, "name", "items")),
	tool("show_chart",
		"Show a chart of one exercise over time: top_set (heaviest set), volume, or e1rm (estimated one-rep max).",
		obj(map[string]any{
			"exercise_id": str,
			"metric":      map[string]any{"type": "string", "enum": []string{"top_set", "volume", "e1rm"}},
			"caption":     str,
		}, "exercise_id", "metric")),
	tool("show_workout", "Show one workout as a card the user can open.", obj(map[string]any{"workout_id": str}, "workout_id")),
}

// runTool runs one tool call and returns its result for the model.
func (t *turn) runTool(ctx context.Context, name string, raw []byte) (string, bool) {
	var out any
	var err error
	switch name {
	case "search_exercises":
		out, err = t.searchExercises(ctx, raw)
	case "get_workouts":
		out, err = t.getWorkouts(ctx, raw)
	case "exercise_history":
		out, err = t.exerciseHistory(ctx, raw)
	case "list_templates":
		out, err = t.listTemplates(ctx)
	case "propose_new_exercise":
		out, err = t.proposeNewExercise(ctx, raw)
	case "log_sets":
		out, err = t.logSets(ctx, raw)
	case "propose_change":
		out, err = t.proposeChange(ctx, raw)
	case "propose_template":
		out, err = t.proposeTemplate(ctx, raw)
	case "show_chart":
		out, err = t.showChart(ctx, raw)
	case "show_workout":
		out, err = t.showWorkout(ctx, raw)
	default:
		err = badInput("there is no tool called %q", name)
	}
	if err != nil {
		var te toolError
		if errors.As(err, &te) {
			return te.msg, true
		}
		t.s.log.Error("milo tool failed", "tool", name, "err", err)
		return "That tool failed on the server. Try another way, or tell the user it didn't work.", true
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "Couldn't encode the result.", true
	}
	return string(b), false
}

func decode(raw []byte, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return badInput("the input isn't valid JSON for this tool: %v", err)
	}
	return nil
}

func parseUUID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, badInput("%s %q is not a valid id", what, s)
	}
	return id, nil
}

func (t *turn) logAttr() slog.Attr {
	return slog.String("conversation", t.convID.String())
}
