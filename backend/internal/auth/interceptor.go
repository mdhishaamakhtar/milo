package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo/miloconnect"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

type ctxKey struct{}

type caller struct {
	userID    uuid.UUID
	tokenHash []byte
}

// public procedures need no session.
var public = map[string]bool{
	miloconnect.AuthServiceRegisterProcedure: true,
	miloconnect.AuthServiceLoginProcedure:    true,
}

// NewInterceptor checks the bearer token on every non-public call and puts the
// signed-in user in the context.
func NewInterceptor(q *query.Queries, now func() time.Time) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if public[spec.Procedure] {
				return next(ctx, spec, stream)
			}
			info, ok := connect.CallInfoForServerContext(ctx)
			if !ok {
				return errSignIn()
			}
			token, ok := strings.CutPrefix(info.RequestHeader().Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				return errSignIn()
			}
			s, err := lookupSession(ctx, q, token, now())
			if errors.Is(err, errNoSession) {
				return errSignIn()
			}
			if err != nil {
				slog.Error("session lookup failed", "err", err)
				return connect.NewError(connect.CodeInternal, "Something went wrong on the server.")
			}
			return next(context.WithValue(ctx, ctxKey{}, caller{userID: s.UserID, tokenHash: s.TokenHash}), spec, stream)
		}
	}
}

// Errors are built per call: a *connect.Error can carry per-call metadata.
func errSignIn() error {
	return connect.NewError(connect.CodeUnauthenticated, "Sign in to continue.")
}

// UserID returns the signed-in user. It panics when called outside an
// authenticated call, which is a wiring bug.
func UserID(ctx context.Context) uuid.UUID {
	c, ok := ctx.Value(ctxKey{}).(caller)
	if !ok {
		panic("auth.UserID called without an authenticated context")
	}
	return c.userID
}

// WithUserID returns a context authenticated as userID, for calling a service directly in tests.
func WithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKey{}, caller{userID: userID})
}

func tokenHashFrom(ctx context.Context) []byte {
	c, _ := ctx.Value(ctxKey{}).(caller)
	return c.tokenHash
}
