package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

const (
	// A session lasts this long after it was last used.
	sessionTTL = 90 * 24 * time.Hour
	// Extend a session at most this often, to avoid a write on every request.
	touchEvery = 24 * time.Hour
)

var errNoSession = errors.New("no valid session")

// newToken returns a bearer token for the client and the hash stored for it.
func newToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func createSession(ctx context.Context, q *query.Queries, userID uuid.UUID, now time.Time) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	if err := q.CreateSession(ctx, query.CreateSessionParams{TokenHash: hash, UserID: userID, ExpiresAt: now.Add(sessionTTL)}); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// lookupSession returns the session for a token, extending it when it is used.
func lookupSession(ctx context.Context, q *query.Queries, token string, now time.Time) (query.Session, error) {
	hash := hashToken(token)
	s, err := q.GetSession(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return query.Session{}, errNoSession
	}
	if err != nil {
		return query.Session{}, fmt.Errorf("get session: %w", err)
	}
	if !now.Before(s.ExpiresAt) {
		_ = q.DeleteSession(ctx, hash)
		return query.Session{}, errNoSession
	}
	if now.Sub(s.LastUsedAt) > touchEvery {
		if err := q.TouchSession(ctx, query.TouchSessionParams{TokenHash: hash, ExpiresAt: now.Add(sessionTTL)}); err != nil {
			return query.Session{}, fmt.Errorf("touch session: %w", err)
		}
	}
	return s, nil
}
