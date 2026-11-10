// Command api runs the Milo API server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/db"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("api stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// HTTP/1.1 for Railway's proxy and curl; cleartext HTTP/2 for Connect clients that use it.
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           server.New(server.Deps{Config: cfg, Pool: pool, Logger: logger}),
		Protocols:         &protocols,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: Milo's replies stream for as long as a turn takes.
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "port", cfg.Port, "model", cfg.Model, "signup_open", cfg.InviteCode != "")
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
