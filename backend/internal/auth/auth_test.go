package auth_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/gen/milo/miloconnect"
	"github.com/mdhishaamakhtar/milo/backend/internal/auth"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

const invite = "lift-heavy"

func newClient(t *testing.T, inviteCode string) (miloconnect.AuthServiceClient, *pgxpool.Pool) {
	t.Helper()
	pool, _ := testkit.DB(t)
	h := server.New(server.Deps{Config: config.Config{InviteCode: inviteCode}, Pool: pool, Logger: slog.New(slog.DiscardHandler)})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))
	return miloconnect.NewAuthServiceClient(c), pool
}

func withToken(ctx context.Context, token string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

func register(t *testing.T, c miloconnect.AuthServiceClient, username, password string) *milo.RegisterResponse {
	t.Helper()
	res, err := c.Register(context.Background(), &milo.RegisterRequest{Username: username, Password: password, InviteCode: invite})
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	return res
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want %v", code)
	}
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("got %v (%v), want %v", got, err, code)
	}
}

func TestHashVerify(t *testing.T) {
	h1, err := auth.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := auth.Hash("correct horse")
	if h1 == h2 {
		t.Fatal("two hashes of the same password are equal: salt missing")
	}
	if !auth.Verify(h1, "correct horse") {
		t.Fatal("right password rejected")
	}
	if auth.Verify(h1, "correct horsf") {
		t.Fatal("wrong password accepted")
	}
	if auth.Verify("not a hash", "correct horse") {
		t.Fatal("malformed hash accepted")
	}
}

func TestRegisterLoginMe(t *testing.T) {
	c, _ := newClient(t, invite)
	ctx := context.Background()

	reg := register(t, c, "  Hishaam ", "password1")
	if reg.GetUser().GetUsername() != "hishaam" {
		t.Fatalf("username = %q, want lower-cased and trimmed", reg.GetUser().GetUsername())
	}
	me, err := c.Me(withToken(ctx, reg.GetToken()), &milo.MeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if me.GetUser().GetId() != reg.GetUser().GetId() {
		t.Fatal("Me returned a different user")
	}

	login, err := c.Login(ctx, &milo.LoginRequest{Username: "HISHAAM", Password: "password1"})
	if err != nil {
		t.Fatal(err)
	}
	if login.GetToken() == reg.GetToken() {
		t.Fatal("login reused the registration token")
	}
	if _, err := c.Me(withToken(ctx, login.GetToken()), &milo.MeRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterNeedsInviteCode(t *testing.T) {
	c, _ := newClient(t, invite)
	_, err := c.Register(context.Background(), &milo.RegisterRequest{Username: "someone", Password: "password1", InviteCode: "guess"})
	wantCode(t, err, connect.CodePermissionDenied)

	closed, _ := newClient(t, "")
	_, err = closed.Register(context.Background(), &milo.RegisterRequest{Username: "someone", Password: "password1", InviteCode: ""})
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestRegisterValidates(t *testing.T) {
	c, _ := newClient(t, invite)
	ctx := context.Background()
	for _, tc := range []struct{ username, password string }{
		{"ab", "password1"},
		{"has space", "password1"},
		{"thirty-three-characters-long-name", "password1"},
		{"hishaam", "short"},
	} {
		_, err := c.Register(ctx, &milo.RegisterRequest{Username: tc.username, Password: tc.password, InviteCode: invite})
		wantCode(t, err, connect.CodeInvalidArgument)
	}
}

func TestDuplicateUsernameAlreadyExists(t *testing.T) {
	c, _ := newClient(t, invite)
	register(t, c, "hishaam", "password1")
	_, err := c.Register(context.Background(), &milo.RegisterRequest{Username: "HISHAAM", Password: "password2", InviteCode: invite})
	wantCode(t, err, connect.CodeAlreadyExists)
}

func TestBadPasswordUnauthenticated(t *testing.T) {
	c, _ := newClient(t, invite)
	register(t, c, "hishaam", "password1")
	ctx := context.Background()
	_, err := c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password2"})
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = c.Login(ctx, &milo.LoginRequest{Username: "nobody", Password: "password1"})
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestExpiredTokenUnauthenticated(t *testing.T) {
	c, pool := newClient(t, invite)
	ctx := context.Background()
	reg := register(t, c, "hishaam", "password1")

	_, err := c.Me(ctx, &milo.MeRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)
	_, err = c.Me(withToken(ctx, "made-up-token"), &milo.MeRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)

	if _, err := pool.Exec(ctx, "UPDATE sessions SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	_, err = c.Me(withToken(ctx, reg.GetToken()), &milo.MeRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestLogoutRevokesToken(t *testing.T) {
	c, _ := newClient(t, invite)
	ctx := context.Background()
	reg := register(t, c, "hishaam", "password1")
	if _, err := c.Logout(withToken(ctx, reg.GetToken()), &milo.LogoutRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Me(withToken(ctx, reg.GetToken()), &milo.MeRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	c, _ := newClient(t, invite)
	ctx := context.Background()
	a := register(t, c, "hishaam", "password1")
	b, err := c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password1"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.ChangePassword(withToken(ctx, a.GetToken()), &milo.ChangePasswordRequest{CurrentPassword: "wrong-one", NewPassword: "password2"})
	wantCode(t, err, connect.CodeUnauthenticated)
	if _, err := c.ChangePassword(withToken(ctx, a.GetToken()), &milo.ChangePasswordRequest{CurrentPassword: "password1", NewPassword: "password2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Me(withToken(ctx, a.GetToken()), &milo.MeRequest{}); err != nil {
		t.Fatalf("the session that changed the password was signed out: %v", err)
	}
	_, err = c.Me(withToken(ctx, b.GetToken()), &milo.MeRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)

	_, err = c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password1"})
	wantCode(t, err, connect.CodeUnauthenticated)
	if _, err := c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password2"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAccountRemovesEverything(t *testing.T) {
	c, pool := newClient(t, invite)
	ctx := context.Background()
	reg := register(t, c, "hishaam", "password1")
	other := register(t, c, "someone", "password1")
	userID := uuid.MustParse(reg.GetUser().GetId())

	for _, id := range []string{reg.GetUser().GetId(), other.GetUser().GetId()} {
		if _, err := pool.Exec(ctx, "INSERT INTO workouts (id, user_id, started_at) VALUES ($1, $2, now())", uuid.New(), id); err != nil {
			t.Fatal(err)
		}
	}

	_, err := c.DeleteAccount(withToken(ctx, reg.GetToken()), &milo.DeleteAccountRequest{Password: "password2"})
	wantCode(t, err, connect.CodeUnauthenticated)
	if _, err := c.DeleteAccount(withToken(ctx, reg.GetToken()), &milo.DeleteAccountRequest{Password: "password1"}); err != nil {
		t.Fatal(err)
	}

	_, err = c.Me(withToken(ctx, reg.GetToken()), &milo.MeRequest{})
	wantCode(t, err, connect.CodeUnauthenticated)
	for _, table := range []string{"users", "sessions", "settings", "workouts"} {
		col := "user_id"
		if table == "users" {
			col = "id"
		}
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE "+col+" = $1", userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s still has %d rows for the deleted user", table, n)
		}
	}
	var others int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM workouts").Scan(&others); err != nil {
		t.Fatal(err)
	}
	if others != 1 {
		t.Fatalf("other user's workouts = %d, want 1", others)
	}
}
