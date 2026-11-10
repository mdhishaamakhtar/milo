package testkit

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

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/gen/milo/miloconnect"
	"github.com/mdhishaamakhtar/milo/backend/internal/agent"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
)

// InviteCode is the sign-up code the test server accepts.
const InviteCode = "test-invite"

// API is the whole server on a fresh database, with typed clients the way the apps call it.
type API struct {
	Pool *pgxpool.Pool
	Auth miloconnect.AuthServiceClient
	Sync miloconnect.SyncServiceClient
	Milo miloconnect.MiloServiceClient
}

// NewAPI starts the API on a fresh database. cfg may be zero; its invite code is
// always InviteCode. A nil model means Milo has no API key.
func NewAPI(t testing.TB, cfg config.Config, model agent.Model) *API {
	t.Helper()
	pool, _ := DB(t)
	cfg.InviteCode = InviteCode
	if cfg.Model == "" {
		cfg.Model = config.DefaultModel
	}
	srv := httptest.NewServer(server.New(server.Deps{Config: cfg, Pool: pool, Logger: slog.New(slog.DiscardHandler), Model: model}))
	t.Cleanup(srv.Close)
	c := connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))
	return &API{
		Pool: pool,
		Auth: miloconnect.NewAuthServiceClient(c),
		Sync: miloconnect.NewSyncServiceClient(c),
		Milo: miloconnect.NewMiloServiceClient(c),
	}
}

// SignUp registers a user and returns a context that calls as them.
func (e *API) SignUp(t testing.TB, username string) (context.Context, uuid.UUID) {
	t.Helper()
	res, err := e.Auth.Register(context.Background(), &milo.RegisterRequest{Username: username, Password: "password1", InviteCode: InviteCode})
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
