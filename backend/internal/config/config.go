// Package config loads runtime configuration from the environment.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/joho/godotenv"
)

// DefaultModel is the Claude model Milo runs on unless MILO_MODEL says otherwise.
const DefaultModel = "claude-haiku-5-5"

type Config struct {
	DatabaseURL   string
	AnthropicKey  string
	Port          string
	DailyTokenCap int64
	Model         string
	// InviteCode gates sign-up. Empty means sign-up is closed.
	InviteCode string
}

// Load reads configuration from the environment. In development it also
// loads the repo-root .env (and backend/.env) without overriding real env vars.
func Load() (Config, error) {
	for _, p := range []string{".env", filepath.Join("..", ".env")} {
		_ = godotenv.Load(p)
	}
	c := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AnthropicKey:  firstNonEmpty(os.Getenv("ANTHROPIC_API_KEY_MILO"), os.Getenv("ANTHROPIC_API_KEY")),
		Port:          firstNonEmpty(os.Getenv("PORT"), "8080"),
		DailyTokenCap: 3_000_000,
		Model:         firstNonEmpty(os.Getenv("MILO_MODEL"), DefaultModel),
		InviteCode:    os.Getenv("MILO_INVITE_CODE"),
	}
	if c.DatabaseURL == "" {
		c.DatabaseURL = "postgres://milo:milo@localhost:5433/milo?sslmode=disable"
	}
	if v := os.Getenv("MILO_DAILY_TOKEN_CAP"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, errors.New("MILO_DAILY_TOKEN_CAP must be an integer")
		}
		c.DailyTokenCap = n
	}
	return c, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
