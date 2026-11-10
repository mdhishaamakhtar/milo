package server_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
	"github.com/mdhishaamakhtar/milo/backend/internal/testdb"
)

func TestHealthz(t *testing.T) {
	pool := testdb.New(t)
	srv := httptest.NewServer(server.New(server.Deps{Config: config.Config{}, Pool: pool, Logger: slog.New(slog.DiscardHandler)}))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("got %d %q, want 200 \"ok\"", res.StatusCode, body)
	}
}
