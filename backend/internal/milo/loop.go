package milo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

const (
	// maxModelCalls bounds one turn's tool loop.
	maxModelCalls = 10
	maxTokens     = 32000
)

// turnError is shown to the user as Milo's error line.
type turnError struct {
	msg       string
	retryable bool
	cause     error
}

func (e turnError) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

func (s *Service) params(msgs []anthropic.MessageParam) anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		Model:        anthropic.Model(s.modelName),
		MaxTokens:    maxTokens,
		System:       []anthropic.TextBlockParam{{Text: systemPrompt}},
		Tools:        tools,
		Messages:     msgs,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortMedium},
		// Caches tools, system prompt and the conversation so far.
		CacheControl: anthropic.NewCacheControlEphemeralParam(),
	}
}

// history replays the stored conversation exactly as it was sent and received,
// thinking blocks included.
func (s *Service) history(ctx context.Context, convID uuid.UUID) ([]anthropic.MessageParam, error) {
	rows, err := s.q.ListAgentMessages(ctx, convID)
	if err != nil {
		return nil, err
	}
	out := make([]anthropic.MessageParam, 0, len(rows)+8)
	for _, r := range rows {
		out = append(out, param.Override[anthropic.MessageParam](json.RawMessage(r.Content)))
	}
	return out, nil
}

// run answers one user message: model call, tools, repeat until Milo is done.
// The turn's messages are saved to history only when it finishes cleanly.
func (s *Service) run(ctx context.Context, t *turn, text string, first bool) error {
	used, err := s.q.TokensUsedOn(ctx, dbq.TokensUsedOnParams{UserID: t.userID, Day: s.now().UTC().Truncate(24 * time.Hour)})
	if err != nil {
		return err
	}
	if s.dailyTokenCap > 0 && used >= s.dailyTokenCap {
		return turnError{msg: "You've used today's Milo allowance. It resets at midnight UTC; hand logging still works."}
	}

	history, err := s.history(ctx, t.convID)
	if err != nil {
		return err
	}
	intro, err := t.contextText(ctx, first)
	if err != nil {
		return err
	}
	blocks := []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(intro)}
	if updates, err := t.cardUpdates(ctx); err != nil {
		return err
	} else if updates != "" {
		blocks = append(blocks, anthropic.NewTextBlock(updates))
	}
	blocks = append(blocks, anthropic.NewTextBlock(text))
	pending := []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)}

	for range maxModelCalls {
		var said strings.Builder
		msg, err := s.model.Stream(ctx, s.params(append(history, pending...)), func(d string) {
			said.WriteString(d)
			_ = t.emit(&milov1.SendResponse{Event: &milov1.SendResponse_TextDelta{TextDelta: &milov1.TextDelta{Text: d}}})
		})
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return turnError{msg: "Stopped.", retryable: true, cause: err}
			}
			return turnError{msg: "Milo lost the connection partway through. Try again.", retryable: true, cause: err}
		}
		s.addUsage(ctx, t.userID, msg.Usage)
		if said.Len() > 0 {
			t.saveText(ctx, "assistant", said.String())
		}

		switch msg.StopReason {
		case anthropic.StopReasonRefusal:
			return turnError{msg: "Milo can't help with that one."}
		case anthropic.StopReasonMaxTokens:
			return turnError{msg: "Milo's answer ran too long. Try asking for less at once.", retryable: true}
		}
		pending = append(pending, msg.ToParam())

		if msg.StopReason != anthropic.StopReasonToolUse {
			return s.persistMessages(ctx, t.convID, t.userID, pending)
		}
		var results []anthropic.ContentBlockParamUnion
		for _, block := range msg.Content {
			if use, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
				out, isErr := t.runTool(ctx, use.Name, []byte(use.JSON.Input.Raw()))
				results = append(results, anthropic.NewToolResultBlock(use.ID, out, isErr))
			}
		}
		pending = append(pending, anthropic.NewUserMessage(results...))
	}
	return turnError{msg: "That took Milo too many steps. Try a narrower question.", retryable: true}
}

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
		if w, err := t.s.q.GetUserWorkout(ctx, dbq.GetUserWorkoutParams{ID: id, UserID: t.userID}); err == nil {
			state := "finished"
			if w.EndedAt == nil {
				state = "still running (the open workout)"
			}
			fmt.Fprintf(&b, "Workout on screen: %q (workout_id %s), started %s, %s.\n",
				nameOr(w.Name, "Workout"), w.ID, dayText(w.StartedAt.In(t.loc), t.now)+" "+w.StartedAt.In(t.loc).Format("15:04"), state)
		} else {
			fmt.Fprintf(&b, "Workout on screen: workout_id %s (not synced yet).\n", id)
		}
	}
	if id, err := uuid.Parse(t.mctx.GetExerciseId()); err == nil {
		if ex, err := t.s.q.VisibleExercise(ctx, dbq.VisibleExerciseParams{ID: id, UserID: &t.userID}); err == nil {
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
	recent, err := t.s.q.RecentWorkouts(ctx, dbq.RecentWorkoutsParams{UserID: t.userID, Limit: 3})
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
			fmt.Fprintf(&b, "- %s, %s (workout_id %s): %s\n", nameOr(d.w.Name, "Workout"), dayText(d.w.StartedAt.In(t.loc), t.now), d.w.ID, strings.Join(parts, "; "))
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
		var card milov1.Card
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
