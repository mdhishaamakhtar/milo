package config_test

import (
	"os"
	"testing"

	"github.com/mdhishaamakhtar/milo/backend/internal/config"
)

// The settings Load reads; each test starts with all of them empty.
var settings = []string{"DATABASE_URL", "ANTHROPIC_API_KEY_MILO", "ANTHROPIC_API_KEY", "PORT", "MILO_MODEL", "MILO_INVITE_CODE", "MILO_DAILY_TOKEN_CAP"}

// unset removes the settings for this test (they come back after it) and runs it in an
// empty folder, so no .env is read.
func unset(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, k := range settings {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadDefaultsToLocalDevelopment(t *testing.T) {
	unset(t)
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		DatabaseURL: "postgres://milo:milo@localhost:5433/milo?sslmode=disable", Port: "8080",
		DailyTokenCap: 3_000_000, Model: config.DefaultModel,
	}
	if c != want {
		t.Fatalf("got %+v, want %+v", c, want)
	}
}

func TestLoadReadsTheEnvironment(t *testing.T) {
	unset(t)
	for k, v := range map[string]string{
		"DATABASE_URL": "postgres://prod", "ANTHROPIC_API_KEY_MILO": "milo-key", "ANTHROPIC_API_KEY": "other-key",
		"PORT": "9000", "MILO_MODEL": "claude-sonnet-5-5", "MILO_INVITE_CODE": "friends", "MILO_DAILY_TOKEN_CAP": "500",
	} {
		t.Setenv(k, v)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Milo's own key wins over a general one.
	want := config.Config{DatabaseURL: "postgres://prod", AnthropicKey: "milo-key", Port: "9000", DailyTokenCap: 500, Model: "claude-sonnet-5-5", InviteCode: "friends"}
	if c != want {
		t.Fatalf("got %+v, want %+v", c, want)
	}

	t.Setenv("ANTHROPIC_API_KEY_MILO", "")
	if c, _ := config.Load(); c.AnthropicKey != "other-key" {
		t.Fatalf("key %q, want the general one when Milo has none", c.AnthropicKey)
	}
}

func TestLoadRefusesATokenCapThatIsntANumber(t *testing.T) {
	unset(t)
	t.Setenv("MILO_DAILY_TOKEN_CAP", "lots")
	if _, err := config.Load(); err == nil {
		t.Fatal("got no error for a token cap of \"lots\"")
	}
}
