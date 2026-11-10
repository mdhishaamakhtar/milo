// Package apitest runs the whole API against a fresh database for tests and
// hands back typed clients, the way the apps talk to it.
package apitest

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/gen/milo/v1/milov1connect"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
	"github.com/mdhishaamakhtar/milo/backend/internal/testdb"
)

// InviteCode is the sign-up code the test server accepts.
const InviteCode = "test-invite"

type Env struct {
	Pool *pgxpool.Pool
	Auth milov1connect.AuthServiceClient
	Sync milov1connect.SyncServiceClient
	Milo milov1connect.MiloServiceClient
}

// New starts the API on a fresh database. cfg may be zero; its invite code is always InviteCode.
func New(t testing.TB, cfg config.Config) *Env {
	t.Helper()
	pool := testdb.New(t)
	cfg.InviteCode = InviteCode
	srv := httptest.NewServer(server.New(server.Deps{Config: cfg, Pool: pool, Logger: slog.New(slog.DiscardHandler)}))
	t.Cleanup(srv.Close)
	c := connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))
	return &Env{
		Pool: pool,
		Auth: milov1connect.NewAuthServiceClient(c),
		Sync: milov1connect.NewSyncServiceClient(c),
		Milo: milov1connect.NewMiloServiceClient(c),
	}
}

// SignUp registers a user and returns a context that calls as them.
func (e *Env) SignUp(t testing.TB, username string) (context.Context, uuid.UUID) {
	t.Helper()
	res, err := e.Auth.Register(context.Background(), &milov1.RegisterRequest{Username: username, Password: "password1", InviteCode: InviteCode})
	if err != nil {
		t.Fatalf("sign up %s: %v", username, err)
	}
	return WithToken(context.Background(), res.GetToken()), uuid.MustParse(res.GetUser().GetId())
}

// WithToken returns a context whose calls carry the bearer token.
func WithToken(ctx context.Context, token string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

// WantCode fails the test unless err is a Connect error with the given code.
func WantCode(t testing.TB, err error, code connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want %v", code)
	}
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("got %v (%v), want %v", got, err, code)
	}
}

// At is a fixed instant plus an offset, for readable timestamps in tests.
func At(offset time.Duration) time.Time {
	return time.Date(2026, 11, 10, 7, 52, 0, 0, time.UTC).Add(offset)
}
