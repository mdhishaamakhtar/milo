package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

// What the app shows when a conversation is reopened, stored as it streams.

type textPayload struct {
	Text string `json:"text"`
}

type readingPayload struct {
	Summary string `json:"summary"`
}

func (t *turn) saveItem(ctx context.Context, kind string, payload []byte, cardID *uuid.UUID, status *string) error {
	_, err := t.s.q.InsertMiloItem(ctx, query.InsertMiloItemParams{
		ConversationID: t.convID, UserID: t.userID, Kind: kind, Payload: payload, CardID: cardID, CardStatus: status,
	})
	return err
}

func (t *turn) saveText(ctx context.Context, kind, text string) {
	b, _ := json.Marshal(textPayload{Text: text})
	if err := t.saveItem(ctx, kind, b, nil, nil); err != nil {
		t.s.log.Error("agent: save text", "err", err, "conversation", t.convID)
	}
}

// reading tells the user what Milo looked at.
func (t *turn) reading(ctx context.Context, summary string) {
	b, _ := json.Marshal(readingPayload{Summary: summary})
	if err := t.saveItem(ctx, "reading", b, nil, nil); err != nil {
		t.s.log.Error("agent: save reading", "err", err, "conversation", t.convID)
	}
	_ = t.emit(&milo.SendResponse{Event: &milo.SendResponse_Reading{Reading: &milo.Reading{Summary: summary}}})
}

// emitCard stores a card as pending and streams it to the app.
func (t *turn) emitCard(ctx context.Context, card *milo.Card) (string, error) {
	id := newID()
	card.Id = id.String()
	card.Status = milo.CardStatus_CARD_STATUS_PENDING
	card.AppliedTitle = cmp.Or(card.AppliedTitle, card.Title)
	b, err := protojson.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("encode card: %w", err)
	}
	status := "pending"
	if err := t.saveItem(ctx, "card", b, &id, &status); err != nil {
		return "", fmt.Errorf("save card: %w", err)
	}
	if err := t.emit(&milo.SendResponse{Event: &milo.SendResponse_Card{Card: card}}); err != nil {
		return "", err
	}
	return id.String(), nil
}

// persistMessages appends a finished turn to the model's history in one go, so
// a failed turn leaves no half-written exchange behind.
func (s *Service) persistMessages(ctx context.Context, convID, userID uuid.UUID, msgs []anthropic.MessageParam) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
		if err := q.InsertAgentMessage(ctx, query.InsertAgentMessageParams{ConversationID: convID, UserID: userID, Role: roleOf(m), Content: b}); err != nil {
			return err
		}
	}
	if err := q.MarkCardsReported(ctx, convID); err != nil {
		return err
	}
	if err := q.TouchConversation(ctx, convID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) addUsage(ctx context.Context, userID uuid.UUID, u anthropic.Usage) {
	err := s.q.AddUsage(ctx, query.AddUsageParams{
		UserID: userID, Day: s.now().UTC().Truncate(24 * time.Hour),
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CacheReadTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens,
	})
	if err != nil {
		s.log.Error("agent: record usage", "err", err)
	}
}

func roleOf(m anthropic.MessageParam) string {
	if m.OfAssistant != nil {
		return "assistant"
	}
	return "user"
}
