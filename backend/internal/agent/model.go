package agent

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

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
