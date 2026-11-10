package agent_test

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/gen/milo/miloconnect"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
	"github.com/mdhishaamakhtar/milo/backend/internal/db"
	"github.com/mdhishaamakhtar/milo/backend/internal/server"
	"github.com/mdhishaamakhtar/milo/backend/internal/testkit"
)

// scenario is one conversation someone might have with Milo: what they say,
// turn by turn, and where they are when they say it.
type scenario struct {
	name   string
	screen string // "today" unless set; "workout" puts the running workout on screen
	turns  []string
}

// battery is the things people ask Milo, grouped by what they want from it.
var battery = []scenario{
	// Progress: the answer should be seen, not only read.
	{name: "progress-open", turns: []string{"What's my progress been?"}},
	{name: "progress-show", turns: []string{"Show me my progress"}},
	{name: "progress-most-improved", turns: []string{"Which lifts have improved most since September?"}},
	{name: "progress-one-lift", turns: []string{"How has my chest press gone this year?"}},
	{name: "progress-stronger", turns: []string{"Am I getting stronger?"}},
	{name: "progress-chart-ask", turns: []string{"chart my lat pulldown"}},
	{name: "progress-compare", turns: []string{"Compare my chest press and shoulder press"}},
	{name: "progress-prs", turns: []string{"What are my PRs?"}},
	{name: "progress-heaviest", turns: []string{"What's the heaviest I've ever leg pressed?"}},
	{name: "progress-e1rm", turns: []string{"What's my one rep max on chest press?"}},
	{name: "progress-plateau", turns: []string{"I feel stuck on lat pulldown. Am I?"}},

	// Habits: how often, how much, how balanced.
	{name: "habit-consistency", turns: []string{"How consistent have I been this year?"}},
	{name: "habit-october", turns: []string{"How many times did I train in October?"}},
	{name: "habit-neglect", turns: []string{"Am I neglecting any muscle groups?"}},
	{name: "habit-volume-months", turns: []string{"How does my volume last month compare with the month before?"}},
	{name: "habit-week-compare", turns: []string{"How does this week compare to last week?"}},
	{name: "habit-weekday", turns: []string{"Which day of the week do I train most?"}},
	{name: "habit-legs", turns: []string{"When did I last train legs? Am I skipping leg day?"}},
	{name: "habit-duration", turns: []string{"How long are my workouts usually?"}},
	{name: "habit-streak", turns: []string{"What's my longest streak of weeks in a row?"}},
	{name: "habit-body", turns: []string{"How's my weight trending?"}},

	// Looking things up.
	{name: "lookup-last", turns: []string{"Summarise my last workout"}},
	{name: "lookup-date", turns: []string{"What did I do on 13 October?"}},
	{name: "lookup-week", turns: []string{"Show me last week's workouts"}},
	{name: "lookup-exercise-last", turns: []string{"what did I do on shoulder press last time"}},
	{name: "lookup-calories", turns: []string{"How many calories did I burn in my last workout?"}},

	// Coaching: what next.
	{name: "coach-today", turns: []string{"What should I train today?"}},
	{name: "coach-weight", screen: "workout", turns: []string{"What weight should I go for on chest press today?"}},
	{name: "coach-go-up", turns: []string{"Am I ready to go up on leg extension?"}},
	{name: "coach-short", turns: []string{"I only have 30 minutes. What should I do?"}},
	{name: "coach-plan-push", turns: []string{"Plan me a push day from what I usually do"}},
	{name: "coach-split", turns: []string{"Build me a 3 day split from the exercises I already do"}},
	{name: "coach-next", turns: []string{"Start my next workout"}},
	{name: "coach-rep-range", turns: []string{"What rep range is best for building muscle?"}},
	{name: "coach-form", turns: []string{"How do I do a face pull properly?"}},
	{name: "coach-pain", turns: []string{"My shoulder hurts when I press overhead. What should I do?"}},

	// Logging, mid-workout and after the fact.
	{name: "log-sets", screen: "workout", turns: []string{"did 3 sets of lat pulldown, 50 for 12"}},
	{name: "log-same", screen: "workout", turns: []string{"chest press done, same as last time"}},
	{name: "log-fix", screen: "workout", turns: []string{"chest press 30 for 15, 15, 12", "sorry the last one was 10 not 12"}},
	{name: "log-drop", screen: "workout", turns: []string{"chest press 40 for 10 then a drop set at 30 for 8"}},
	{name: "log-yesterday", turns: []string{"Log yesterday: bench press 60 kg 3x8, incline dumbbell press 20s for 10, 10, 8, tricep pushdown 25 for 3 sets of 12"}},
	{name: "log-swap", screen: "workout", turns: []string{"swap chest press for bench press"}},
	{name: "log-rename", screen: "workout", turns: []string{"call this one Push A"}},
	{name: "log-superset", screen: "workout", turns: []string{"add lateral raises and face pulls as a superset"}},
	{name: "log-finish", screen: "workout", turns: []string{"I'm done"}},
	{name: "log-unknown", screen: "workout", turns: []string{"did the thing where you hang from the bar and bring your knees up, 3 sets of 12"}},

	// Follow-ups: the second message leans on the first.
	{name: "follow-chart", turns: []string{"How's my leg press going?", "and the leg extension?"}},
	{name: "follow-template", turns: []string{"What did I do in my last push day?", "make that a template"}},
	{name: "follow-repeat", turns: []string{"What was my best leg day?", "do that one again today"}},

	// The edges.
	{name: "edge-hi", turns: []string{"hi"}},
	{name: "edge-what-can", turns: []string{"what can you do?"}},
	{name: "edge-delete-all", turns: []string{"delete all my workouts from 2024"}},
	{name: "edge-remind", turns: []string{"remind me to train tomorrow at 7"}},
	{name: "edge-units", turns: []string{"what's 60 kg in pounds, and show everything in pounds from now on"}},
	{name: "edge-offtopic", turns: []string{"write me a python script to sort a list"}},
	{name: "edge-diet", turns: []string{"how much protein should I eat?"}},
	{name: "edge-vague", turns: []string{"add 5"}},
	{name: "edge-typo", turns: []string{"hw mny times did i do chst prss in octber"}},
}

type batteryTurn struct {
	Said      string   `json:"said"`
	Seconds   float64  `json:"seconds"`
	FirstText float64  `json:"first_text_seconds"`
	Calls     int      `json:"model_calls"`
	Tools     []string `json:"tools"`
	Readings  []string `json:"readings"`
	Cards     []string `json:"cards"`
	Errors    []string `json:"errors"`
	Text      string   `json:"text"`
}

type batteryResult struct {
	Name  string        `json:"name"`
	Turns []batteryTurn `json:"turns"`
}

// TestBattery asks Milo everything in the battery against a database that
// already holds a real training history, and writes what happened to a report.
// It costs money and needs that database, so it only runs with MILO_BATTERY=1:
//
//	MILO_BATTERY=1 MILO_BATTERY_USER=uicheck go test ./internal/agent -run TestBattery -v
//
// MILO_BATTERY_RUN narrows it to scenarios matching a pattern; MILO_MODEL picks the model.
func TestBattery(t *testing.T) {
	if os.Getenv("MILO_BATTERY") != "1" {
		t.Skip("set MILO_BATTERY=1 to run the battery against a real history")
	}
	_ = godotenv.Load("../../../.env")
	cfg, err := config.Load()
	if err != nil || cfg.AnthropicKey == "" {
		t.Fatalf("config: %v (key set: %v)", err, cfg.AnthropicKey != "")
	}
	cfg.DailyTokenCap = 0
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	username := os.Getenv("MILO_BATTERY_USER")
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM users WHERE username = $1", username).Scan(&userID); err != nil {
		t.Fatalf("user %q: %v", username, err)
	}

	// A session of its own, removed afterwards.
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	if _, err := pool.Exec(ctx, "INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')", hash[:], userID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash = $1", hash[:]) }()

	srv := httptest.NewServer(server.New(server.Deps{Config: cfg, Pool: pool, Logger: slog.New(slog.DiscardHandler)}))
	defer srv.Close()
	conn := connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))
	client := miloconnect.NewMiloServiceClient(conn)
	authed := testkit.WithToken(ctx, token)

	// A workout of its own to be in the middle of: chest press, three sets waiting. Removed afterwards.
	var chestPress uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM exercises WHERE user_id IS NULL AND name = 'Chest Press (Machine)'").Scan(&chestPress); err != nil {
		t.Fatal(err)
	}
	runningID, weID := uuid.New(), uuid.New()
	now := timestamppb.Now()
	running := []*milo.Change{
		{Row: &milo.Change_Workout{Workout: &milo.Workout{Id: runningID.String(), Name: "Battery workout", StartedAt: timestamppb.New(time.Now().Add(-10 * time.Minute)), UpdatedAt: now}}},
		{Row: &milo.Change_WorkoutExercise{WorkoutExercise: &milo.WorkoutExercise{Id: weID.String(), WorkoutId: runningID.String(), ExerciseId: chestPress.String(), Position: 1, UpdatedAt: now}}},
	}
	for i := range 3 {
		running = append(running, &milo.Change{Row: &milo.Change_Set{Set: &milo.Set{
			Id: uuid.NewString(), WorkoutExerciseId: weID.String(), Position: int32(i + 1), Kind: milo.SetKind_SET_KIND_NORMAL,
			Reps: proto.Int32(15), Weight: "30", WeightUnit: milo.WeightUnit_WEIGHT_UNIT_KG, UpdatedAt: now,
		}}})
	}
	sync := miloconnect.NewSyncServiceClient(conn)
	if _, err := sync.Sync(authed, &milo.SyncRequest{Changes: running}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Deleted the way the app deletes, so a phone signed in as this user hears of it.
		gone := timestamppb.New(time.Now().Add(time.Second))
		for _, c := range running {
			switch r := c.GetRow().(type) {
			case *milo.Change_Workout:
				r.Workout.Deleted, r.Workout.UpdatedAt = true, gone
			case *milo.Change_WorkoutExercise:
				r.WorkoutExercise.Deleted, r.WorkoutExercise.UpdatedAt = true, gone
			case *milo.Change_Set:
				r.Set.Deleted, r.Set.UpdatedAt = true, gone
			}
		}
		_, _ = sync.Sync(authed, &milo.SyncRequest{Changes: running})
	}()

	pick := regexp.MustCompile(os.Getenv("MILO_BATTERY_RUN"))
	var chosen []scenario
	for _, s := range battery {
		if pick.MatchString(s.name) {
			chosen = append(chosen, s)
		}
	}
	results := make([]batteryResult, len(chosen))
	var wg gosync.WaitGroup
	slots := make(chan struct{}, 6)
	for i, s := range chosen {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			// Its own call context: one shared between goroutines has them all writing the same headers.
			results[i] = runScenario(testkit.WithToken(ctx, token), client, pool, s, runningID)
		}()
	}
	wg.Wait()

	out := os.Getenv("MILO_BATTERY_OUT")
	if out == "" {
		out = t.TempDir()
	}
	label := strings.NewReplacer("/", "-", " ", "-").Replace(cfg.Model)
	if tag := os.Getenv("MILO_BATTERY_TAG"); tag != "" {
		label += "-" + tag
	}
	js, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(out, label+".json"), js, 0o644)
	var md strings.Builder
	for _, r := range results {
		fmt.Fprintf(&md, "## %s\n", r.Name)
		for _, tn := range r.Turns {
			fmt.Fprintf(&md, "\n> %s\n\n%.1fs (first words %.1fs), %d model calls, tools: %s\n", tn.Said, tn.Seconds, tn.FirstText, tn.Calls, strings.Join(tn.Tools, ", "))
			for _, c := range tn.Cards {
				fmt.Fprintf(&md, "  [card] %s\n", c)
			}
			for _, e := range tn.Errors {
				fmt.Fprintf(&md, "  [error] %s\n", e)
			}
			fmt.Fprintf(&md, "\n%s\n", tn.Text)
		}
		md.WriteString("\n")
	}
	_ = os.WriteFile(filepath.Join(out, label+".md"), []byte(md.String()), 0o644)
	t.Logf("wrote %s/%s.{md,json}", out, label)
}

// runScenario has one conversation and records, for each turn, how long it
// took, which tools Milo called and what the app was shown.
func runScenario(ctx context.Context, client miloconnect.MiloServiceClient, pool *pgxpool.Pool, s scenario, runningID uuid.UUID) batteryResult {
	res := batteryResult{Name: s.name}
	mctx := &milo.MiloContext{Screen: cmp.Or(s.screen, "today"), Timezone: "Asia/Kolkata"}
	if s.screen == "workout" && runningID != uuid.Nil {
		mctx.WorkoutId = runningID.String()
	}
	convID := ""
	seen := 0 // model messages already counted in this conversation
	for _, said := range s.turns {
		turn := batteryTurn{Said: said}
		start := time.Now()
		stream, err := client.Send(ctx, &milo.SendRequest{ConversationId: convID, Text: said, Context: mctx})
		if err != nil {
			turn.Errors = append(turn.Errors, err.Error())
			res.Turns = append(res.Turns, turn)
			break
		}
		for {
			ev, err := stream.Receive()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					turn.Errors = append(turn.Errors, err.Error())
				}
				break
			}
			switch e := ev.GetEvent().(type) {
			case *milo.SendResponse_TextDelta:
				if turn.Text == "" {
					turn.FirstText = time.Since(start).Seconds()
				}
				turn.Text += e.TextDelta.GetText()
			case *milo.SendResponse_Reading:
				turn.Readings = append(turn.Readings, e.Reading.GetSummary())
			case *milo.SendResponse_Card:
				c := e.Card
				line := fmt.Sprintf("%s | %s | %s", strings.TrimPrefix(c.GetKind().String(), "CARD_KIND_"), c.GetTitle(), c.GetSubtitle())
				if ch := c.GetChart(); ch != nil {
					line += fmt.Sprintf(" | %d points", len(ch.GetPoints()))
				}
				for _, l := range c.GetLines() {
					line += fmt.Sprintf("\n      %s: %s → %s", l.GetLabel(), l.GetBefore(), l.GetAfter())
				}
				turn.Cards = append(turn.Cards, line)
			case *milo.SendResponse_Error:
				turn.Errors = append(turn.Errors, e.Error.GetMessage())
			case *milo.SendResponse_Done:
				convID = e.Done.GetConversationId()
			}
		}
		_ = stream.Close()
		turn.Seconds = time.Since(start).Seconds()

		// What Milo did, from the model's own messages.
		rows, err := pool.Query(ctx, "SELECT role, content FROM agent_messages WHERE conversation_id = $1 ORDER BY id OFFSET $2", convID, seen)
		if err == nil {
			for rows.Next() {
				var role string
				var content []byte
				if rows.Scan(&role, &content) != nil {
					continue
				}
				seen++
				if role != "assistant" {
					continue
				}
				turn.Calls++
				var msg struct {
					Content []struct {
						Type  string          `json:"type"`
						Name  string          `json:"name"`
						Input json.RawMessage `json:"input"`
					} `json:"content"`
				}
				_ = json.Unmarshal(content, &msg)
				for _, b := range msg.Content {
					if b.Type == "tool_use" {
						turn.Tools = append(turn.Tools, b.Name+string(b.Input))
					}
				}
			}
			rows.Close()
		}
		res.Turns = append(res.Turns, turn)
	}
	if convID != "" && os.Getenv("MILO_BATTERY_KEEP") == "" {
		_, _ = client.DeleteConversation(ctx, &milo.DeleteConversationRequest{Id: convID})
	}
	return res
}
