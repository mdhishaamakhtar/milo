# Milo

A workout log you can talk to: a SwiftUI iPhone app (iOS 26), a Go API on Postgres, and Milo, an agent that reads the log and proposes changes as cards. One person's app today, built to App Store standard.

Read before changing anything substantial: `PRODUCT.md` (who it is for and what it must never become), `DESIGN.md` (how it looks and why), `docs/architecture.md` (how it works, in four ideas), `CONTRIBUTING.md` (conventions and recipes).

## Layout

- `proto/milo/` is the contract. `make gen` writes Go into `backend/gen` and Swift into `ios/MiloAPI/Generated`; both are checked in.
- `backend/` is the server: `internal/agent` (Milo), `internal/sync`, `internal/auth`, `internal/maths`, `internal/db` (migrations and sqlc queries), `internal/testkit`.
- `ios/Milo/` is the app: `Features/` by screen, `DesignSystem/`, `Data/` (GRDB, sync), `Domain/` (maths, formatting).
- `testdata/maths/` holds test vectors both codebases run.

## Find your way

There is no hand-written router. `proto/milo/*.proto` declares three services, and the code generator writes the routes, the request types and both clients from them.

A request, end to end:

1. The app calls a generated client: `ios/Milo/Data/APIClient.swift` holds `auth`, `sync` and `milo`. `SyncEngine.swift` calls `sync`; `Features/Milo/MiloSession.swift` calls `milo`.
2. It arrives at `POST /milo.<Service>/<Method>`. `backend/cmd/api/main.go` starts the server and `backend/internal/server/server.go` is the one place the three services are registered. This is the router.
3. `backend/internal/auth/interceptor.go` checks the bearer token on every call.
4. The call lands in the service that implements it:

| Service (in `proto/milo`) | Implemented in | What it does |
|---|---|---|
| `AuthService` (`auth.proto`) | `backend/internal/auth/service.go` | Sign up, sign in, password, delete account |
| `SyncService` (`sync.proto`) | `backend/internal/sync/` (`push.go`, `pull.go`) | The only way rows are written: the phone pushes its changes and pulls everyone else's |
| `MiloService` (`agent.proto`) | `backend/internal/agent/agent.go` | One user turn, streamed; conversations; card status |

Where to make a change:

| You want to | Go to |
|---|---|
| Add or change a call, a row or a card field | `proto/milo`, then `make gen` |
| Change the database | `backend/internal/db/migrations`, queries in `backend/internal/db/query/*.sql`, then `make gen` |
| Change what Milo says or when it acts | `backend/internal/agent/prompt.go` |
| Change what Milo can do | `backend/internal/agent/tools.go` lists every tool; each points at its function |
| Change a number (volume, 1RM, calories) | `testdata/maths`, then `backend/internal/maths` and `ios/Milo/Domain/Maths.swift` |
| Change a screen | `ios/Milo/Features/<Screen>` |
| Change how a card looks | `ios/Milo/Features/Milo/CardView.swift` |

The agent is most of the server (about three quarters of its code) because the phone owns the log: sync and accounts are thin, and Milo has 25 tools. Inside `backend/internal/agent`:

- `agent.go` is the service: `Send` and the conversation calls.
- `loop.go` is one turn: call the model, run its tools, repeat.
- `turn.go` holds a turn's state and what is saved for the app.
- `prompt.go` is the system prompt and the context sent with each message.
- `tools.go` is the list of tools. The tools themselves are four files, one per kind: `read.go` (look things up), `do.go` (done on the phone straight away, with Undo), `propose.go` (a card that waits for a tap), `show.go` (a card to look at).
- `format.go` is how numbers and dates are written.

## Commands

```bash
make up          # Postgres on :5433 (Docker)
make run         # API on :8080, migrates on start
make ios         # generate the Xcode project and open it
make test        # server tests against a real Postgres
make ios-test    # app tests on a simulator (SIM="iPhone 18 Pro Max" to pick one)
make lint        # buf lint and go vet
make cover       # branch coverage of the server (gobco), region coverage of the app's logic
make gen         # after editing a .proto or .sql file
make live        # Milo against the real model, about a cent
make battery AS=username   # about 60 real questions over that user's history in the local database, about 15 cents
make phone       # Release build on the plugged-in iPhone, pointed at production
```

Server tests start one throwaway Postgres with Testcontainers for the whole run, copy each test's database from a migrated template and run in parallel. `MILO_TEST_DATABASE_URL=postgres://milo:milo@localhost:5433/milo?sslmode=disable` uses the compose one instead. CONTRIBUTING.md says which test a change needs.

## Rules that are easy to break

- **Pushing to master deploys the API to Railway.** Commit freely; push only when asked.
- **A new card kind needs the app installed too.** An older app draws a card it doesn't know as a plain list, so ship the server change and `make phone` together.
- **Milo never writes to the log.** A tool returns a card of rows; the phone applies it and syncs. Reads and show tools change nothing.
- **Show tools return what they drew**, so Milo calls them instead of reading first. Keep that when adding one.
- **Suggested replies are the answer's last line** (starting `>>`), held back in `loop.go`. They are not a tool: Sonnet 5.5 and Opus 5.5 turn text written before a tool call into progress notes, and the answer is lost.
- **The system prompt and tool list are cached and shared by every user.** Never put a time, a name or anything per-user in them; that goes in the user turn (`contextText`).
- **Lean Go.** No layers that only forward, no versioned package paths, no hand-written mapping where `rows` already maps. Comments say why.
- **Generated code and `go.mod` stay tidy:** run `make gen` and `go mod tidy` and commit what they write.
- **Local files stay out of git:** `.env`, `*.csv`, `.claude/`, `docs/superpowers/`.

## iOS traps already hit

- A `TextField` reports its top edge as its text baseline. To stand Milo's mark on its first line use `BarMLogo.onFirstLine(of:)` and align the row at the top.
- The system `navigationSubtitle` can't be spaced and a "y" in the title touches it. Tabs use `TitleLine` as the first row of content instead.
- Gaps between stacked text come from `Space` in `Theme.swift`; don't write literal 1 to 4 point gaps.
- App code runs on the main actor. A closure a framework calls on its own thread must be built in a `nonisolated` function (`Speech/Dictation.swift`).
- Look at every screen you change in the simulator before calling it done. The simulator tool's own screenshot is one frame stale; `xcrun simctl io <udid> screenshot <file>` is current.

## Milo's model

`MILO_MODEL` picks it (default `claude-haiku-5-5` in `internal/config`). Measured on the battery: Haiku costs about 0.2 cents a message, Sonnet 5.5 about 3.5 cents, at similar speed. When changing the prompt or a tool description, run `make battery` before and after and read the answers, not only the timings.

## Words

Plain and short, in the lifter's terms ("60 kg × 8", "each side"). No em dashes in the app's copy. Commits are `type(scope): what changed`, with a body that says why.
