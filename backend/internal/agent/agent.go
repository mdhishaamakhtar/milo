// Package agent is Milo: it reads the user's log and answers in
// conversation, proposing every change as a card the phone applies.
package agent

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

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/gen/milo/miloconnect"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// Service implements miloconnect.MiloServiceHandler.
type Service struct {
	pool          *pgxpool.Pool
	q             *query.Queries
	model         Model // nil when no API key is configured
	modelName     string
	dailyTokenCap int64
	now           func() time.Time
}

type Options struct {
	Model         Model
	ModelName     string
	DailyTokenCap int64
	Now           func() time.Time
}

func NewService(pool *pgxpool.Pool, o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{pool: pool, q: query.New(pool), model: o.Model, modelName: o.ModelName, dailyTokenCap: o.DailyTokenCap, now: o.Now}
}

var _ miloconnect.MiloServiceHandler = (*Service)(nil)

func (s *Service) Send(ctx context.Context, req *milo.SendRequest, stream miloconnect.MiloServiceSendServerStream) error {
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
		conv, err := s.q.CreateConversation(ctx, query.CreateConversationParams{ID: newID(), UserID: userID, Title: truncate(text, titleLength)})
		if err != nil {
			return internal(err)
		}
		convID = conv.ID
	} else {
		id, err := uuid.Parse(req.GetConversationId())
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument, "conversation_id is not a UUID")
		}
		conv, err := s.q.GetConversation(ctx, query.GetConversationParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, "No such conversation.")
		}
		if err != nil {
			return internal(err)
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
		mctx = &milo.MiloContext{}
	}
	t := newTurn(s, userID, convID, mctx, loc, displayUnit, stream.Send)
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
			slog.Error("agent turn failed", "err", err, "conversation", convID)
			te = turnError{msg: "Something went wrong on Milo's side. Try again.", retryable: true}
		} else if te.cause != nil {
			slog.Warn("agent turn ended early", "err", te.cause, "conversation", convID)
		}
		t.saveText(ctx, "assistant", te.msg)
		if sendErr := stream.Send(&milo.SendResponse{Event: &milo.SendResponse_Error{Error: &milo.MiloError{Message: te.msg, Retryable: te.retryable}}}); sendErr != nil {
			return sendErr
		}
	}
	return stream.Send(&milo.SendResponse{Event: &milo.SendResponse_Done{Done: &milo.Done{ConversationId: convID.String()}}})
}

func (s *Service) ListConversations(ctx context.Context, req *milo.ListConversationsRequest) (*milo.ListConversationsResponse, error) {
	limit := req.GetLimit()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	convs, err := s.q.ListConversations(ctx, query.ListConversationsParams{UserID: auth.UserID(ctx), Limit: limit})
	if err != nil {
		return nil, internal(err)
	}
	out := make([]*milo.Conversation, len(convs))
	for i, c := range convs {
		out[i] = conversationProto(c)
	}
	return &milo.ListConversationsResponse{Conversations: out}, nil
}

func conversationProto(c query.MiloConversation) *milo.Conversation {
	return &milo.Conversation{Id: c.ID.String(), Title: c.Title, Summary: c.Summary, UpdatedAt: timestamppb.New(c.UpdatedAt)}
}

func (s *Service) GetConversation(ctx context.Context, req *milo.GetConversationRequest) (*milo.GetConversationResponse, error) {
	userID := auth.UserID(ctx)
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is not a UUID")
	}
	conv, err := s.q.GetConversation(ctx, query.GetConversationParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "No such conversation.")
	}
	if err != nil {
		return nil, internal(err)
	}
	stored, err := s.q.ListMiloItems(ctx, query.ListMiloItemsParams{ConversationID: id, UserID: userID})
	if err != nil {
		return nil, internal(err)
	}
	items := make([]*milo.MiloItem, 0, len(stored))
	for _, r := range stored {
		item := &milo.MiloItem{Id: r.ID, CreatedAt: timestamppb.New(r.CreatedAt)}
		switch r.Kind {
		case "user", "assistant":
			var p textPayload
			_ = json.Unmarshal(r.Payload, &p)
			if r.Kind == "user" {
				item.Item = &milo.MiloItem_UserText{UserText: p.Text}
			} else {
				item.Item = &milo.MiloItem_AssistantText{AssistantText: p.Text}
			}
		case "reading":
			var p readingPayload
			_ = json.Unmarshal(r.Payload, &p)
			item.Item = &milo.MiloItem_Reading{Reading: &milo.Reading{Summary: p.Summary}}
		case "card":
			var card milo.Card
			if err := protojson.Unmarshal(r.Payload, &card); err != nil {
				continue
			}
			if r.CardStatus != nil {
				card.Status = rows.Parse[milo.CardStatus](*r.CardStatus)
			}
			item.Item = &milo.MiloItem_Card{Card: &card}
		}
		items = append(items, item)
	}
	return &milo.GetConversationResponse{Conversation: conversationProto(conv), Items: items}, nil
}

func (s *Service) SetCardStatus(ctx context.Context, req *milo.SetCardStatusRequest) (*milo.SetCardStatusResponse, error) {
	cardID, err := uuid.Parse(req.GetCardId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "card_id is not a UUID")
	}
	status := rows.Name(req.GetStatus())
	if status == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "status must be set")
	}
	row, err := s.q.SetCardStatus(ctx, query.SetCardStatusParams{Status: &status, CardID: &cardID, UserID: auth.UserID(ctx)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, "No such card.")
	}
	if err != nil {
		return nil, internal(err)
	}
	// What came of the conversation, for the list of chats: everything applied so far.
	if status == "applied" || status == "undone" {
		if titles, err := s.q.AppliedCardTitles(ctx, row.ConversationID); err == nil && len(titles) > 0 {
			_ = s.q.SetConversationSummary(ctx, query.SetConversationSummaryParams{ID: row.ConversationID, Summary: truncate(strings.Join(titles, " · "), summaryLength)})
		}
	}
	return &milo.SetCardStatusResponse{}, nil
}

func (s *Service) DeleteConversation(ctx context.Context, req *milo.DeleteConversationRequest) (*milo.DeleteConversationResponse, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is not a UUID")
	}
	n, err := s.q.DeleteConversation(ctx, query.DeleteConversationParams{ID: id, UserID: auth.UserID(ctx)})
	if err != nil {
		return nil, internal(err)
	}
	if n == 0 {
		return nil, connect.NewError(connect.CodeNotFound, "No such conversation.")
	}
	return &milo.DeleteConversationResponse{}, nil
}

// A chat's title is how its first message began; its summary is one line.
const (
	titleLength   = 60
	summaryLength = 120
)

// internal logs an unexpected error and tells the client only that it happened.
func internal(err error) error {
	slog.Error("agent failed", "err", err)
	return connect.NewError(connect.CodeInternal, "Something went wrong on the server.")
}
