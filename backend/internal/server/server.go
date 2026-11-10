// Package server wires the HTTP routes: the health check and the API services.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo/miloconnect"
	"github.com/mdhishaamakhtar/milo/backend/internal/agent"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/sync"
)

// Deps is everything the handlers need.
type Deps struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// Model replaces the Claude API in tests. When nil, the API key in Config is used.
	Model agent.Model
}

// New returns the API's root handler.
func New(d Deps) http.Handler {
	model := d.Model
	if model == nil && d.Config.AnthropicKey != "" {
		model = agent.NewAnthropicModel(d.Config.AnthropicKey)
	}

	rpc := connect.NewServer(auth.NewInterceptor(query.New(d.Pool), time.Now))
	miloconnect.RegisterAuthServiceHandler(rpc, auth.NewService(d.Pool, d.Config.InviteCode, time.Now))
	miloconnect.RegisterSyncServiceHandler(rpc, sync.NewService(d.Pool))
	miloconnect.RegisterMiloServiceHandler(rpc, agent.NewService(d.Pool, agent.Options{
		Model: model, ModelName: d.Config.Model, DailyTokenCap: d.Config.DailyTokenCap, Logger: d.Logger,
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
