// Package sync is the sync service: phones push the rows they changed and pull
// everything newer than their cursor. The newest write to a row wins.
package sync

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

const (
	pageSize = 500
	maxPush  = 2000
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Sync applies the pushed changes in one transaction, then returns the next page
// of rows after the cursor (which includes what was just pushed).
func (s *Service) Sync(ctx context.Context, req *milo.SyncRequest) (*milo.SyncResponse, error) {
	if req.GetCursor() < 0 {
		return nil, invalid("cursor can't be negative")
	}
	if len(req.GetChanges()) > maxPush {
		return nil, invalid("push at most %d changes at a time", maxPush)
	}
	userID := auth.UserID(ctx)
	var rejected []*milo.Rejection
	if len(req.GetChanges()) > 0 {
		// Nearly every push is clean, so it is first written straight through. Only if
		// the database itself refuses a row is it done again with a savepoint around
		// each one, which costs two more round trips a row.
		err := errBadRow
		for _, careful := range []bool{false, true} {
			if !errors.Is(err, errBadRow) {
				break
			}
			err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
				rejected, err = pushChanges(ctx, tx, userID, req.GetChanges(), careful)
				return err
			})
		}
		if err != nil {
			return nil, rpcError(err)
		}
		for _, r := range rejected {
			slog.Warn("sync: change refused", "user", userID, "index", r.GetIndex(), "reason", r.GetReason())
		}
	}
	changes, cursor, more, err := pull(ctx, query.New(s.pool), userID, req.GetCursor(), pageSize)
	if err != nil {
		return nil, rpcError(err)
	}
	return &milo.SyncResponse{Cursor: cursor, Changes: changes, HasMore: more, Rejected: rejected}, nil
}

// rpcError passes on errors meant for the client and hides everything else.
func rpcError(err error) error {
	if connect.CodeOf(err) != connect.CodeUnknown {
		return err
	}
	slog.Error("sync failed", "err", err)
	return connect.NewError(connect.CodeInternal, "Something went wrong on the server.")
}
