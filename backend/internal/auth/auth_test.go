package auth_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

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
	h := server.New(server.Deps{Config: config.Config{InviteCode: inviteCode}, Pool: pool})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))
	return miloconnect.NewAuthServiceClient(c), pool
}

func register(t *testing.T, c miloconnect.AuthServiceClient, username, password string) *milo.RegisterResponse {
	t.Helper()
	res, err := c.Register(context.Background(), &milo.RegisterRequest{Username: username, Password: password, InviteCode: invite})
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	return res
}

func TestHashVerify(t *testing.T) {
	t.Parallel()
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
	// Anything that isn't a whole Argon2id hash never matches.
	for _, bad := range []string{
		"not a hash",
		strings.Replace(h1, "argon2id", "argon2i", 1),
		strings.Replace(h1, "v=19", "v=16", 1),
		strings.Replace(h1, "m=65536", "m=lots", 1),
		h1[:strings.LastIndex(h1, "$")] + "$!!!",
		h1[:strings.LastIndex(h1, "$")] + "$",
	} {
		if auth.Verify(bad, "correct horse") {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A hash keeps working after the cost settings change: Verify reads them from the hash.
func TestVerifyUsesTheHashsOwnSettings(t *testing.T) {
	t.Parallel()
	salt := []byte("sixteen byte slt")
	key := argon2.IDKey([]byte("correct horse"), salt, 1, 8*1024, 1, 32)
	b64 := base64.RawStdEncoding
	older := fmt.Sprintf("$argon2id$v=%d$m=8192,t=1,p=1$%s$%s", argon2.Version, b64.EncodeToString(salt), b64.EncodeToString(key))
	if !auth.Verify(older, "correct horse") || auth.Verify(older, "wrong horse") {
		t.Fatal("a hash made with other settings doesn't verify as it should")
	}
}

func TestRegisterLoginMe(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	ctx := context.Background()

	reg := register(t, c, "  Hishaam ", "password1")
	if reg.GetUser().GetUsername() != "hishaam" {
		t.Fatalf("username = %q, want lower-cased and trimmed", reg.GetUser().GetUsername())
	}
	me, err := c.Me(testkit.WithToken(ctx, reg.GetToken()), &milo.MeRequest{})
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
	if _, err := c.Me(testkit.WithToken(ctx, login.GetToken()), &milo.MeRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterNeedsInviteCode(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	_, err := c.Register(context.Background(), &milo.RegisterRequest{Username: "someone", Password: "password1", InviteCode: "guess"})
	testkit.WantCode(t, err, connect.CodePermissionDenied)

	closed, _ := newClient(t, "")
	_, err = closed.Register(context.Background(), &milo.RegisterRequest{Username: "someone", Password: "password1", InviteCode: ""})
	testkit.WantCode(t, err, connect.CodePermissionDenied)
}

func TestRegisterValidates(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	ctx := context.Background()
	for _, tc := range []struct{ username, password string }{
		{"ab", "password1"},
		{"has space", "password1"},
		{"thirty-three-characters-long-name", "password1"},
		{"hishaam", "short"},
	} {
		_, err := c.Register(ctx, &milo.RegisterRequest{Username: tc.username, Password: tc.password, InviteCode: invite})
		testkit.WantCode(t, err, connect.CodeInvalidArgument)
	}
}

func TestDuplicateUsernameAlreadyExists(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	register(t, c, "hishaam", "password1")
	_, err := c.Register(context.Background(), &milo.RegisterRequest{Username: "HISHAAM", Password: "password2", InviteCode: invite})
	testkit.WantCode(t, err, connect.CodeAlreadyExists)
}

func TestBadPasswordUnauthenticated(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	register(t, c, "hishaam", "password1")
	ctx := context.Background()
	_, err := c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password2"})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
	_, err = c.Login(ctx, &milo.LoginRequest{Username: "nobody", Password: "password1"})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
}

func TestExpiredTokenUnauthenticated(t *testing.T) {
	t.Parallel()
	c, pool := newClient(t, invite)
	ctx := context.Background()
	reg := register(t, c, "hishaam", "password1")

	_, err := c.Me(ctx, &milo.MeRequest{})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
	_, err = c.Me(testkit.WithToken(ctx, "made-up-token"), &milo.MeRequest{})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)

	if _, err := pool.Exec(ctx, "UPDATE sessions SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	_, err = c.Me(testkit.WithToken(ctx, reg.GetToken()), &milo.MeRequest{})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
}

// A session used now and then stays alive: the first use after a day pushes its end back.
func TestUsingASessionKeepsItAlive(t *testing.T) {
	t.Parallel()
	c, pool := newClient(t, invite)
	ctx := context.Background()
	reg := register(t, c, "hishaam", "password1")
	if _, err := pool.Exec(ctx, "UPDATE sessions SET last_used_at = now() - interval '2 days', expires_at = now() + interval '1 day'"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Me(testkit.WithToken(ctx, reg.GetToken()), &milo.MeRequest{}); err != nil {
		t.Fatal(err)
	}

	var left time.Duration
	if err := pool.QueryRow(ctx, "SELECT EXTRACT(EPOCH FROM expires_at - now())::bigint * 1000000000 FROM sessions").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left < 89*24*time.Hour {
		t.Fatalf("session ends in %v, want about 90 days after it was used", left)
	}
}

func TestLogoutRevokesToken(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	ctx := context.Background()
	reg := register(t, c, "hishaam", "password1")
	if _, err := c.Logout(testkit.WithToken(ctx, reg.GetToken()), &milo.LogoutRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Me(testkit.WithToken(ctx, reg.GetToken()), &milo.MeRequest{})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, invite)
	ctx := context.Background()
	a := register(t, c, "hishaam", "password1")
	b, err := c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password1"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.ChangePassword(testkit.WithToken(ctx, a.GetToken()), &milo.ChangePasswordRequest{CurrentPassword: "wrong-one", NewPassword: "password2"})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
	if _, err := c.ChangePassword(testkit.WithToken(ctx, a.GetToken()), &milo.ChangePasswordRequest{CurrentPassword: "password1", NewPassword: "password2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Me(testkit.WithToken(ctx, a.GetToken()), &milo.MeRequest{}); err != nil {
		t.Fatalf("the session that changed the password was signed out: %v", err)
	}
	_, err = c.Me(testkit.WithToken(ctx, b.GetToken()), &milo.MeRequest{})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)

	_, err = c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password1"})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
	if _, err := c.Login(ctx, &milo.LoginRequest{Username: "hishaam", Password: "password2"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAccountRemovesEverything(t *testing.T) {
	t.Parallel()
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

	_, err := c.DeleteAccount(testkit.WithToken(ctx, reg.GetToken()), &milo.DeleteAccountRequest{Password: "password2"})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
	if _, err := c.DeleteAccount(testkit.WithToken(ctx, reg.GetToken()), &milo.DeleteAccountRequest{Password: "password1"}); err != nil {
		t.Fatal(err)
	}

	_, err = c.Me(testkit.WithToken(ctx, reg.GetToken()), &milo.MeRequest{})
	testkit.WantCode(t, err, connect.CodeUnauthenticated)
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
