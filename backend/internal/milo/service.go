// Package milo is the agent: it reads the user's log and answers in
// conversation, proposing every change as a card the phone applies.
package milo

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	_ "time/tzdata" // user time zones on minimal images

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/gen/milo/v1/milov1connect"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

// Service implements milov1connect.MiloServiceHandler.
type Service struct {
	pool          *pgxpool.Pool
	q             *dbq.Queries
	model         Model // nil when no API key is configured
	modelName     string
	dailyTokenCap int64
	now           func() time.Time
	log           *slog.Logger
}

type Options struct {
	Model         Model
	ModelName     string
	DailyTokenCap int64
	Now           func() time.Time
	Logger        *slog.Logger
}

func NewService(pool *pgxpool.Pool, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Service{pool: pool, q: dbq.New(pool), model: o.Model, modelName: o.ModelName, dailyTokenCap: o.DailyTokenCap, now: o.Now, log: o.Logger}
}

var _ milov1connect.MiloServiceHandler = (*Service)(nil)

func (s *Service) Send(ctx context.Context, req *milov1.SendRequest, stream milov1connect.MiloServiceSendServerStream) error {
	userID := auth.UserID(ctx)
	text := strings.TrimSpace(req.GetText())
	if text == "" {
		return connect.NewError(connect.CodeInvalidArgument, "Say something to Milo.")
	}
	if len(text) > 8000 {
		return connect.NewError(connect.CodeInvalidArgument, "That's too long for one message.")
	}

	first := req.GetConversationId() == ""
	var convID uuid.UUID
	if first {
		conv, err := s.q.CreateConversation(ctx, dbq.CreateConversationParams{ID: newID(), UserID: userID, Title: truncate(text, 48)})
		if err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		convID = conv.ID
	} else {
		id, err := uuid.Parse(req.GetConversationId())
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, "conversation_id is not a UUID")
		}
		conv, err := s.q.GetConversation(ctx, dbq.GetConversationParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, "No such conversation.")
		}
		if err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		convID = conv.ID
	}

	loc := time.UTC
	if tz := req.GetContext().GetTimezone(); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	displayUnit := "kg"
	if st, err := s.q.GetSettingsFor(ctx, userID); err == nil {
		displayUnit = st.DisplayUnit
	}
	mctx := req.GetContext()
	if mctx == nil {
		mctx = &milov1.MiloContext{}
	}
	t := &turn{
		s: s, userID: userID, convID: convID, mctx: mctx, loc: loc, now: s.now(), displayUnit: displayUnit,
		emit:        stream.Send,
		newWorkouts: map[uuid.UUID]*newWorkout{},
	}
	t.saveText(ctx, "user", text)

	var err error
	if s.model == nil {
		err = turnError{msg: "Milo isn't set up on this server yet (no API key)."}
	} else {
		err = s.run(ctx, t, text, first)
	}
	if err != nil {
		var te turnError
		if !errors.As(err, &te) {
			s.log.Error("milo turn failed", "err", err, t.logAttr())
			te = turnError{msg: "Something went wrong on Milo's side. Try again.", retryable: true}
		} else if te.cause != nil {
			s.log.Warn("milo turn ended early", "err", te.cause, t.logAttr())
		}
		t.saveText(ctx, "assistant", te.msg)
		if sendErr := stream.Send(&milov1.SendResponse{Event: &milov1.SendResponse_Error{Error: &milov1.MiloError{Message: te.msg, Retryable: te.retryable}}}); sendErr != nil {
			return sendErr
		}
	}
	return stream.Send(&milov1.SendResponse{Event: &milov1.SendResponse_Done{Done: &milov1.Done{ConversationId: convID.String()}}})
}

func (s *Service) ListConversations(ctx context.Context, req *milov1.ListConversationsRequest) (*milov1.ListConversationsResponse, error) {
	limit := req.GetLimit()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.q.ListConversations(ctx, dbq.ListConversationsParams{UserID: auth.UserID(ctx), Limit: limit})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	out := make([]*milov1.Conversation, len(rows))
	for i, c := range rows {
		out[i] = conversationProto(c)
	}
	return &milov1.ListConversationsResponse{Conversations: out}, nil
}

func conversationProto(c dbq.MiloConversation) *milov1.Conversation {
	return &milov1.Conversation{Id: c.ID.String(), Title: c.Title, Summary: c.Summary, UpdatedAt: timestamppb.New(c.UpdatedAt)}
}

var cardStatusFromDB = map[string]milov1.CardStatus{
	"pending":   milov1.CardStatus_CARD_STATUS_PENDING,
	"applied":   milov1.CardStatus_CARD_STATUS_APPLIED,
	"dismissed": milov1.CardStatus_CARD_STATUS_DISMISSED,
	"undone":    milov1.CardStatus_CARD_STATUS_UNDONE,
}

func (s *Service) GetConversation(ctx context.Context, req *milov1.GetConversationRequest) (*milov1.GetConversationResponse, error) {
	userID := auth.UserID(ctx)
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is not a UUID")
	}
	conv, err := s.q.GetConversation(ctx, dbq.GetConversationParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "No such conversation.")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	rows, err := s.q.ListMiloItems(ctx, dbq.ListMiloItemsParams{ConversationID: id, UserID: userID})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	items := make([]*milov1.MiloItem, 0, len(rows))
	for _, r := range rows {
		item := &milov1.MiloItem{Id: r.ID, CreatedAt: timestamppb.New(r.CreatedAt)}
		switch r.Kind {
		case "user", "assistant":
			var p textPayload
			_ = json.Unmarshal(r.Payload, &p)
			if r.Kind == "user" {
				item.Item = &milov1.MiloItem_UserText{UserText: p.Text}
			} else {
				item.Item = &milov1.MiloItem_AssistantText{AssistantText: p.Text}
			}
		case "reading":
			var p readingPayload
			_ = json.Unmarshal(r.Payload, &p)
			item.Item = &milov1.MiloItem_Reading{Reading: &milov1.Reading{Summary: p.Summary}}
		case "card":
			var card milov1.Card
			if err := protojson.Unmarshal(r.Payload, &card); err != nil {
				continue
			}
			if r.CardStatus != nil {
				card.Status = cardStatusFromDB[*r.CardStatus]
			}
			item.Item = &milov1.MiloItem_Card{Card: &card}
		}
		items = append(items, item)
	}
	return &milov1.GetConversationResponse{Conversation: conversationProto(conv), Items: items}, nil
}

var cardStatusToDB = map[milov1.CardStatus]string{
	milov1.CardStatus_CARD_STATUS_PENDING:   "pending",
	milov1.CardStatus_CARD_STATUS_APPLIED:   "applied",
	milov1.CardStatus_CARD_STATUS_DISMISSED: "dismissed",
	milov1.CardStatus_CARD_STATUS_UNDONE:    "undone",
}

// summaryFor is what an applied card says in the conversation list.
func summaryFor(card *milov1.Card) string {
	switch card.GetKind() {
	case milov1.CardKind_CARD_KIND_NEW_EXERCISE:
		return "Added " + card.GetTitle() + " as a new exercise"
	case milov1.CardKind_CARD_KIND_TEMPLATE_DRAFT:
		return "Saved the " + card.GetTitle() + " template"
	case milov1.CardKind_CARD_KIND_LOGGED:
		return card.GetTitle()
	case milov1.CardKind_CARD_KIND_CHANGE:
		return "Changed " + card.GetTitle()
	}
	return ""
}

func (s *Service) SetCardStatus(ctx context.Context, req *milov1.SetCardStatusRequest) (*milov1.SetCardStatusResponse, error) {
	cardID, err := uuid.Parse(req.GetCardId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "card_id is not a UUID")
	}
	status, ok := cardStatusToDB[req.GetStatus()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, "status must be set")
	}
	row, err := s.q.SetCardStatus(ctx, dbq.SetCardStatusParams{Status: &status, CardID: &cardID, UserID: auth.UserID(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "No such card.")
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	if status == "applied" {
		var card milov1.Card
		if protojson.Unmarshal(row.Payload, &card) == nil {
			if sum := summaryFor(&card); sum != "" {
				_ = s.q.SetConversationSummary(ctx, dbq.SetConversationSummaryParams{ID: row.ConversationID, Summary: sum})
			}
		}
	}
	return &milov1.SetCardStatusResponse{}, nil
}
