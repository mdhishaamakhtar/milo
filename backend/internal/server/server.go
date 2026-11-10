// Package server wires the HTTP routes: health check and the Connect services.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo/v1/milov1connect"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
	"github.com/mdhishaamakhtar/milo/backend/internal/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/syncsvc"
)

// Deps is everything the handlers need.
type Deps struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// Now defaults to time.Now.
	Now func() time.Time
	// MiloModel replaces the Claude API (tests). When nil, the API key in Config is used.
	MiloModel milo.Model
}

// New returns the API's root handler.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	q := dbq.New(d.Pool)

	rpc := connect.NewServer(auth.NewInterceptor(q, d.Now))
	milov1connect.RegisterAuthServiceHandler(rpc, auth.NewService(d.Pool, d.Config.InviteCode, d.Now))
	milov1connect.RegisterSyncServiceHandler(rpc, syncsvc.NewService(d.Pool))

	model := d.MiloModel
	if model == nil && d.Config.AnthropicKey != "" {
		model = milo.NewAnthropicModel(d.Config.AnthropicKey)
	}
	milov1connect.RegisterMiloServiceHandler(rpc, milo.NewService(d.Pool, milo.Options{
		Model: model, ModelName: d.Config.Model, DailyTokenCap: d.Config.DailyTokenCap, Now: d.Now, Logger: d.Logger,
	}))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(d.Pool))
	// Binary protobuf for the apps; JSON too, so the API can be poked with curl.
	connecthttp.Mount(mux, rpc, connecthttp.WithProtoJSON())
	return mux
}

// healthz answers "ok" when the database is reachable, for Railway's health check.
func healthz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := pool.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("database unavailable"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}
