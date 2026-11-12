# Working on Milo

Read the README's Quick Start first; this picks up once the app is running.

## The loop

```bash
make test       # server tests, against a real Postgres in Docker
make ios-test   # app tests on a simulator
make lint       # buf lint and go vet
make cover      # branch coverage of the server, region coverage of the app's logic
```

Run the tests for the side you touched before you commit. A change to
`proto/` or `testdata/maths` touches both sides. Both suites take seconds and
run their tests in parallel.

Generated code is checked in (`backend/gen`, `ios/MiloAPI/Generated`, and the
`*.sql.go` files). After editing a `.proto` or a `.sql` file, run `make gen` and
commit what it writes alongside your change.

## Conventions

These are the things a reviewer will ask for.

- **Plain code.** No layers that only forward calls, no interface with one
  implementation unless a test needs the seam. If a function is used once and
  is short, it probably belongs where it's used.
- **Comments say why.** A comment earns its place by telling the reader
  something the code can't: the reason, the rule, the trap. Don't narrate.
- **Tests use the real thing.** Server tests run against Postgres through the
  real HTTP handlers (`internal/testkit`). The one fake is the language model.
  App tests use a real in-memory SQLite database. See "Testing" below.
- **The phone never waits on the network to log.** Reads and writes go to the
  local database; sync catches up.
- **Milo never writes to the log.** A tool that changes something returns a card
  of rows; the phone applies it.
- **Words on screen are plain.** Short sentences, the lifter's own terms
  ("60 kg × 8", "each side"), no jargon.
- **Swift.** The app's code runs on the main actor by default. A closure that a
  framework calls on its own thread must be built in a `nonisolated` function,
  or the app traps at run time; `Speech/Dictation.swift` shows the pattern.
- **Motion.** Use `.quick` for a control answering a touch and `.settle` for
  things arriving or moving (`DesignSystem/Theme.swift`). Don't invent timings.
- **Room.** Stacked text takes its gap from `Space` in the same file, and a
  tab's line under its title is a `TitleLine`. Don't write 1 to 4 point gaps.

Commits follow `type(scope): what changed`, with a body that says why when the
why isn't obvious.

## Testing

A test earns its place by catching something a person would notice breaking.
Don't test a library (Foundation's date formats, JSON, godotenv), a constant, or
a line that only calls something else. Don't bend code to reach it from a test:
if a test needs a seam, plain code usually has one already (a parameter, a
function moved out of a view body).

### Which test an app change needs

| You changed | Test it with |
|---|---|
| A number (volume, 1RM, calories, bests) | A case in `testdata/maths`; both apps run it |
| How something reads ("60 kg × 8", "Tue 6 Oct") | `FormatTests` |
| What the store reads or writes | `StoreReadTests` / `StoreWriteTests`, on an in-memory database |
| A synced row or field | `ProtoTests` round trip, and a sync test on the server |
| Sync | `SyncEngineTests`, against `FakeServer` |
| Talking to Milo, or how a card applies | `MiloSessionTests`, against `FakeServer` |
| How Milo's words or cards are laid out | `ChatLogicTests` (`ReplyText.blocks`, `Conversation.rows`, `CardView.layout`) |
| The workout screen's logic (ticking, rest, the pad) | `WorkoutModelTests` |
| A view's look | No unit test: look at it on a simulator (empty, a long name, the largest text size) |

Keep logic out of view bodies so it can be tested as a plain function: that is
why `Conversation.rows` and `CardView.layout` are `static func`s.

`FakeServer` is Connect's own transport interface answering from queues, so a
test runs the generated clients, codec and envelopes and sees exactly what the
app sends. A `Gate` holds a reply part-way, to look at the app mid-stream
without sleeping. Apps built in tests pass `onDevice: false`: no network
monitor and no notifications, which a test host can't answer.

### Server tests

Every test gets its own database, copied from a migrated template, so tests run
with `t.Parallel()`. One Postgres container serves a whole `go test ./...`.
Drive Milo's tools with `call` and the fake model: never the real model outside
`make live` and `make battery`. A tool's refusals belong in
`TestToolsTellMiloWhatWasWrong`, one line per mistake a model makes.

### Coverage

`make cover` prints branch coverage per server package and region coverage of
the app's logic. A branch that only a failing database can take (an
`if err != nil` straight after a query) is left untested rather than faked.

## Recipes

### Give Milo a new tool

1. Decide its kind: does it read, do something straight away, propose, or show?
   Something that rewrites or removes logged data must propose.
2. In `backend/internal/agent`, in the file for its kind (`read.go`, `do.go`,
   `propose.go` or `show.go`), write an input struct with `json` and
   `jsonschema_description` tags, and a method `func (t *turn) myTool(ctx, in) (any, error)`.
   Return `badInput(...)` for anything the model got wrong; the message goes
   back to it, so say how to fix it.
3. If it changes data, build rows with the `rows` package and send them with
   `t.emitCard`. Set `AutoApply` for a "do" tool; add `Base` versions for a
   "propose" tool that edits existing rows. A "show" tool returns the numbers
   it drew, so Milo can talk about them without reading again. A tool that only
   reads is registered with `readTool`, which lets it run beside other reads.
4. Register it in `tools.go`, in the group it belongs to, with a description
   that says when to use it and when not to.
5. If it changes when Milo should act, say so in the prompt (`prompt.go`).
6. Test it in `tools_test.go` with the `call` helper, which drives the tool the
   way the model would and hands back the cards.

The phone needs no change unless you add a card kind: it applies any rows. A
new kind is drawn in `ios/Milo/Features/Milo/CardView.swift`, and the app must
ship with the server, since an older app shows a kind it doesn't know as a
plain list.

### Add a field to a synced row

1. Add the column in a new migration under `backend/internal/db/migrations`.
2. Add the field to the message in `proto/milo/common.proto`.
3. Update the upsert in `backend/internal/db/query/sync.sql`, then `make gen`.
4. Map it both ways in `backend/internal/rows/rows.go` and
   `backend/internal/sync/push.go`.
5. On the phone: add the column in a new migration in `AppDatabase.swift`, the
   property in `Records.swift`, and the mapping in `Proto.swift`.
6. `make test` and `make ios-test`. The sync round-trip tests catch a missed mapping.

### Change a number

1. Edit or add cases in `testdata/maths/`. Give each a name that states the rule.
2. Run both test suites and watch them fail.
3. Change `backend/internal/maths/maths.go` and `ios/Milo/Domain/Maths.swift`
   until both pass.

### Add a screen

1. Make a folder under `ios/Milo/Features`. Read through `Live` and a query on
   `AppDatabase`; write through a function in `StoreWrites.swift`, then call
   `app.edited()` so it syncs.
2. Use the pieces in `DesignSystem` (`card()`, `SectionHeader`, the button
   styles) before making new ones.
3. Set `app.miloContext` in `onAppear` so Milo knows where the user is.
4. Run `xcodegen` (or `make ios`) so the project picks up the new files.
5. Look at it on a simulator. Check the empty state, a long name, and the
   largest text size.

## Talking to the real model

`make live` runs Milo against the Claude API with the key in your `.env`. It
costs about a cent, so run it when you change the prompt or a tool's
description, not on every commit. It prints what Milo said and the cards it
made; read them, since a passing check can still be a clumsy answer.

`make battery AS=username` asks Milo about 60 questions people really ask
(progress, habits, logging, coaching, the edges) over that user's history in
your local database, and writes each answer, the tools it took and how long it
took to a report. It costs about 15 cents on Haiku. Run it before and after a
change to the prompt or the tools and compare the two reports.
