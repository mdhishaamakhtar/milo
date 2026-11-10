package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

const (
	minPassword = 8
	maxPassword = 256
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.]{3,32}$`)

// Service implements milov1connect.AuthServiceHandler.
type Service struct {
	pool       *pgxpool.Pool
	q          *dbq.Queries
	inviteCode string
	now        func() time.Time
}

// NewService returns the account service. An empty inviteCode closes sign-up.
func NewService(pool *pgxpool.Pool, inviteCode string, now func() time.Time) *Service {
	return &Service{pool: pool, q: dbq.New(pool), inviteCode: inviteCode, now: now}
}

func (s *Service) Register(ctx context.Context, req *milov1.RegisterRequest) (*milov1.RegisterResponse, error) {
	if s.inviteCode == "" || subtle.ConstantTimeCompare([]byte(req.GetInviteCode()), []byte(s.inviteCode)) != 1 {
		return nil, connect.NewError(connect.CodePermissionDenied, "Sign-up needs an invite code.")
	}
	username := normalise(req.GetUsername())
	if !usernamePattern.MatchString(username) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "Usernames are 3 to 32 letters, numbers, dots or underscores.")
	}
	if err := checkPassword(req.GetPassword()); err != nil {
		return nil, err
	}
	hash, err := Hash(req.GetPassword())
	if err != nil {
		return nil, internal(err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, internal(err)
	}

	var user dbq.User
	var token string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		if user, err = q.CreateUser(ctx, dbq.CreateUserParams{ID: id, Username: username, PasswordHash: hash}); err != nil {
			return err
		}
		if err = q.CreateDefaultSettings(ctx, user.ID); err != nil {
			return err
		}
		token, err = createSession(ctx, q, user.ID, s.now())
		return err
	})
	if isUniqueViolation(err) {
		return nil, connect.NewError(connect.CodeAlreadyExists, "That username is taken.")
	}
	if err != nil {
		return nil, internal(err)
	}
	return &milov1.RegisterResponse{Token: token, User: toProto(user)}, nil
}

func (s *Service) Login(ctx context.Context, req *milov1.LoginRequest) (*milov1.LoginResponse, error) {
	user, err := s.q.GetUserByUsername(ctx, normalise(req.GetUsername()))
	if errors.Is(err, pgx.ErrNoRows) {
		Verify(dummyHash, req.GetPassword())
		return nil, errBadLogin()
	}
	if err != nil {
		return nil, internal(err)
	}
	if !Verify(user.PasswordHash, req.GetPassword()) {
		return nil, errBadLogin()
	}
	token, err := createSession(ctx, s.q, user.ID, s.now())
	if err != nil {
		return nil, internal(err)
	}
	return &milov1.LoginResponse{Token: token, User: toProto(user)}, nil
}

func (s *Service) Logout(ctx context.Context, _ *milov1.LogoutRequest) (*milov1.LogoutResponse, error) {
	if err := s.q.DeleteSession(ctx, tokenHashFrom(ctx)); err != nil {
		return nil, internal(err)
	}
	return &milov1.LogoutResponse{}, nil
}

func (s *Service) Me(ctx context.Context, _ *milov1.MeRequest) (*milov1.MeResponse, error) {
	user, err := s.q.GetUser(ctx, UserID(ctx))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSignIn()
	}
	if err != nil {
		return nil, internal(err)
	}
	return &milov1.MeResponse{User: toProto(user)}, nil
}

func (s *Service) ChangePassword(ctx context.Context, req *milov1.ChangePasswordRequest) (*milov1.ChangePasswordResponse, error) {
	user, err := s.checkCurrentPassword(ctx, req.GetCurrentPassword())
	if err != nil {
		return nil, err
	}
	if err := checkPassword(req.GetNewPassword()); err != nil {
		return nil, err
	}
	hash, err := Hash(req.GetNewPassword())
	if err != nil {
		return nil, internal(err)
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.UpdatePasswordHash(ctx, dbq.UpdatePasswordHashParams{ID: user.ID, PasswordHash: hash}); err != nil {
			return err
		}
		return q.DeleteOtherSessions(ctx, dbq.DeleteOtherSessionsParams{UserID: user.ID, TokenHash: tokenHashFrom(ctx)})
	})
	if err != nil {
		return nil, internal(err)
	}
	return &milov1.ChangePasswordResponse{}, nil
}

func (s *Service) DeleteAccount(ctx context.Context, req *milov1.DeleteAccountRequest) (*milov1.DeleteAccountResponse, error) {
	user, err := s.checkCurrentPassword(ctx, req.GetPassword())
	if err != nil {
		return nil, err
	}
	if err := s.q.DeleteUser(ctx, user.ID); err != nil {
		return nil, internal(err)
	}
	return &milov1.DeleteAccountResponse{}, nil
}

func (s *Service) checkCurrentPassword(ctx context.Context, password string) (dbq.User, error) {
	user, err := s.q.GetUser(ctx, UserID(ctx))
	if errors.Is(err, pgx.ErrNoRows) {
		return dbq.User{}, errSignIn()
	}
	if err != nil {
		return dbq.User{}, internal(err)
	}
	if !Verify(user.PasswordHash, password) {
		return dbq.User{}, connect.NewError(connect.CodeUnauthenticated, "That password isn't right.")
	}
	return user, nil
}

func errBadLogin() error {
	return connect.NewError(connect.CodeUnauthenticated, "Wrong username or password.")
}

func normalise(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func checkPassword(p string) error {
	if len(p) < minPassword || len(p) > maxPassword {
		return connect.NewError(connect.CodeInvalidArgument, "Passwords need at least 8 characters.")
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func internal(err error) error {
	return connect.NewError(connect.CodeInternal, err.Error())
}

func toProto(u dbq.User) *milov1.User {
	return &milov1.User{Id: u.ID.String(), Username: u.Username, CreatedAt: timestamppb.New(u.CreatedAt)}
}
