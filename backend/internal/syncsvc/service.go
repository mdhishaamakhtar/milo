package syncsvc

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

const (
	// PageSize is how many rows one pull returns.
	PageSize = 500
	// MaxPush is how many changes one push may carry.
	MaxPush = 2000
)

// Service implements milov1connect.SyncServiceHandler.
type Service struct {
	pool *pgxpool.Pool
	q    *dbq.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: dbq.New(pool)}
}

// Sync applies the pushed changes in one transaction, then returns the next
// page of rows after the cursor (which includes what was just pushed).
func (s *Service) Sync(ctx context.Context, req *milov1.SyncRequest) (*milov1.SyncResponse, error) {
	if req.GetCursor() < 0 {
		return nil, invalid("cursor can't be negative")
	}
	if len(req.GetChanges()) > MaxPush {
		return nil, invalid("push at most %d changes at a time", MaxPush)
	}
	userID := auth.UserID(ctx)

	if len(req.GetChanges()) > 0 {
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			return ApplyChanges(ctx, tx, userID, req.GetChanges())
		})
		if err != nil {
			if connect.CodeOf(err) != connect.CodeUnknown {
				return nil, err
			}
			return nil, connect.NewError(connect.CodeInternal, err.Error())
		}
	}

	changes, cursor, more, err := Pull(ctx, s.q, userID, req.GetCursor(), PageSize)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	return &milov1.SyncResponse{Cursor: cursor, Changes: changes, HasMore: more}, nil
}
