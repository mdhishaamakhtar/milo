package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// These set environment variables, so they don't run in parallel. Neither asks Milo
// anything, and noKey makes sure a key in the shell can't reach the server either.

func noKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY_MILO", "")
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// The API starts, answers its health check, and stops cleanly when Railway sends SIGTERM.
func TestRunServesUntilStopped(t *testing.T) {
	noKey(t)
	_, dbURL := testkit.DB(t)
	port := freePort(t)
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("PORT", port)

	stopped := make(chan error, 1)
	go func() { stopped <- run() }()

	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := http.Get("http://127.0.0.1:" + port + "/healthz")
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no healthy answer within 10s: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stopped with %v, want a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("still running 10s after SIGTERM")
	}
}

// Anything that would leave the API half working stops it at once, with the reason.
func TestRunRefusesToStartBroken(t *testing.T) {
	noKey(t)
	_, dbURL := testkit.DB(t)
	taken, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()

	for _, c := range []struct {
		name, port, database, cap, want string
	}{
		{"a token cap that isn't a number", freePort(t), dbURL, "lots", "MILO_DAILY_TOKEN_CAP"},
		{"no database", freePort(t), "postgres://milo:milo@127.0.0.1:1/milo?sslmode=disable&connect_timeout=2", "", "ping"},
		{"the port is taken", strconv.Itoa(taken.Addr().(*net.TCPAddr).Port), dbURL, "", "address already in use"},
	} {
		t.Setenv("PORT", c.port)
		t.Setenv("DATABASE_URL", c.database)
		t.Setenv("MILO_DAILY_TOKEN_CAP", c.cap)
		if err := run(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error about %q", c.name, err, c.want)
		}
	}
}
