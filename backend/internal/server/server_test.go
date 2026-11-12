package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

func TestHealthz(t *testing.T) {
	t.Parallel()
	pool, _ := testkit.DB(t)
	srv := httptest.NewServer(server.New(server.Deps{Config: config.Config{}, Pool: pool}))
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

// Railway restarts the API when its health check fails: it fails when the database is gone.
func TestHealthzWithoutTheDatabase(t *testing.T) {
	t.Parallel()
	pool, _ := testkit.DB(t)
	srv := httptest.NewServer(server.New(server.Deps{Config: config.Config{}, Pool: pool}))
	defer srv.Close()
	pool.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusServiceUnavailable || string(body) != "database unavailable" {
		t.Fatalf("got %d %q, want 503 \"database unavailable\"", res.StatusCode, body)
	}
}
