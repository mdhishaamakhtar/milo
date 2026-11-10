package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"
	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
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
		Tools:        toolParams,
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
	used, err := s.q.TokensUsedOn(ctx, query.TokensUsedOnParams{UserID: t.userID, Day: s.now().UTC().Truncate(24 * time.Hour)})
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
			_ = t.emit(&milo.SendResponse{Event: &milo.SendResponse_TextDelta{TextDelta: &milo.TextDelta{Text: d}}})
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
