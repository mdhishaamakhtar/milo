package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
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
		Model:     anthropic.Model(s.modelName),
		MaxTokens: maxTokens,
		// The tools and the system prompt are the same for everyone, so they are cached
		// on their own: a new conversation reads them instead of paying to write them again.
		System:       []anthropic.TextBlockParam{{Text: systemPrompt, CacheControl: anthropic.NewCacheControlEphemeralParam()}},
		Tools:        toolParams,
		Messages:     msgs,
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortMedium},
		// Caches the conversation so far.
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
		words := reply{say: func(d string) {
			said.WriteString(d)
			_ = t.emit(&milo.SendResponse{Event: &milo.SendResponse_TextDelta{TextDelta: &milo.TextDelta{Text: d}}})
		}}
		msg, err := s.model.Stream(ctx, s.params(append(history, pending...)), words.write)
		suggestions := words.finish()
		if err != nil {
			// What was said before the stream broke stays in the chat when it is reopened.
			// The phone hanging up cancels ctx, so the save can't use it.
			if said.Len() > 0 {
				t.saveText(context.WithoutCancel(ctx), "assistant", said.String())
			}
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
			if len(suggestions) > 0 {
				if _, err := t.emitCard(ctx, &milo.Card{Kind: milo.CardKind_CARD_KIND_SUGGESTIONS, Lines: suggestions}); err != nil {
					return err
				}
			}
			// Until a card is applied, the chat list shows how Milo's first answer began.
			_ = s.q.SetConversationSummaryIfEmpty(ctx, query.SetConversationSummaryIfEmptyParams{ID: t.convID, Summary: truncate(said.String(), summaryLength)})
			return s.persistMessages(ctx, t.convID, t.userID, pending)
		}
		var uses []anthropic.ToolUseBlock
		for _, block := range msg.Content {
			if use, ok := block.AsAny().(anthropic.ToolUseBlock); ok {
				uses = append(uses, use)
			}
		}
		pending = append(pending, anthropic.NewUserMessage(t.runTools(ctx, uses)...))
	}
	return turnError{msg: "That took Milo too many steps. Try a narrower question.", retryable: true}
}

// suggestMark starts the last line of an answer when it offers what to say
// next: ">> Chart my leg press | Start a pull day". The line is Milo's to
// write and the app's to draw as buttons, so it never reaches the user as text.
const suggestMark = ">>"

// reply passes Milo's words on as they arrive, holding back the suggestions line.
type reply struct {
	say  func(string)
	held string // not passed on yet: line breaks that may end the answer, and the suggestions line
}

func (r *reply) write(delta string) {
	r.held += delta
	if strings.HasPrefix(strings.TrimLeft(r.held, "\n"), suggestMark) {
		return // the suggestions line has begun; the rest of the reply is it
	}
	// What follows the answer waits until it is known not to be that line: the line
	// itself, a ">" that may become its mark, and the line breaks before either.
	settled := r.held
	if cut := strings.Index(settled, "\n"+suggestMark); cut >= 0 {
		settled = settled[:cut]
	} else if last := settled[strings.LastIndex(settled, "\n")+1:]; strings.HasPrefix(suggestMark, last) {
		settled = settled[:len(settled)-len(last)]
	}
	settled = strings.TrimRight(settled, "\n")
	if settled != "" {
		r.say(settled)
		r.held = r.held[len(settled):]
	}
}

// finish ends the reply: it returns the suggestions if the reply closed with
// them, and passes on anything else still held.
func (r *reply) finish() []*milo.CardLine {
	rest := strings.TrimSpace(r.held)
	r.held = ""
	line, ok := strings.CutPrefix(rest, suggestMark)
	if !ok {
		if rest != "" {
			r.say(rest)
		}
		return nil
	}
	var out []*milo.CardLine
	for _, s := range strings.Split(strings.SplitN(line, "\n", 2)[0], "|") {
		if s = strings.TrimSpace(s); s != "" && len(out) < 3 {
			out = append(out, &milo.CardLine{Label: truncate(s, 40)})
		}
	}
	return out
}

// runTools runs one reply's tool calls and returns their results in order.
// Calls that only read run at once; anything that changes the turn runs in turn.
func (t *turn) runTools(ctx context.Context, uses []anthropic.ToolUseBlock) []anthropic.ContentBlockParamUnion {
	results := make([]anthropic.ContentBlockParamUnion, len(uses))
	run := func(i int) {
		out, isErr := t.runTool(ctx, uses[i].Name, []byte(uses[i].JSON.Input.Raw()))
		results[i] = anthropic.NewToolResultBlock(uses[i].ID, out, isErr)
	}
	if slices.ContainsFunc(uses, func(u anthropic.ToolUseBlock) bool { return !toolsByName[u.Name].reads }) {
		for i := range uses {
			run(i)
		}
		return results
	}
	var wg sync.WaitGroup
	for i := range uses {
		wg.Go(func() { run(i) })
	}
	wg.Wait()
	return results
}

// Model streams one Messages API call. onText receives text as it arrives;
// the returned message is the whole response. Tests use a fake.
type Model interface {
	Stream(ctx context.Context, params anthropic.MessageNewParams, onText func(string)) (*anthropic.Message, error)
}

type anthropicModel struct {
	client anthropic.Client
}

// NewAnthropicModel calls the Claude API with the given key.
func NewAnthropicModel(apiKey string) Model {
	return &anthropicModel{client: anthropic.NewClient(option.WithAPIKey(apiKey))}
}

func (m *anthropicModel) Stream(ctx context.Context, params anthropic.MessageNewParams, onText func(string)) (*anthropic.Message, error) {
	stream := m.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	msg := anthropic.Message{}
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			return nil, err
		}
		if delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok && text.Text != "" {
				onText(text.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return &msg, nil
}
