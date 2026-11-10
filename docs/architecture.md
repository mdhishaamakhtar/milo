# How Milo works

Four ideas carry the whole system. Each section says what the idea is, why it
is that way, and where the code lives.

```
   iPhone                                   Server
 ┌──────────────────────────┐            ┌───────────────────────────┐
 │ Screens                  │            │ SyncService               │
 │   read and write ↓       │   Sync     │   push, then pull         │
 │ SQLite (GRDB)  ◄─────────┼───────────►│ Postgres                  │
 │   rows + dirty flag      │            │   rows + server_seq       │
 │        ▲                 │            │        ▲ reads only       │
 │        │ applies cards   │   Send     │ MiloService               │
 │ Milo sheet  ◄────────────┼────────────┤   tool loop ⇄ Claude API  │
 └──────────────────────────┘  (stream)  └───────────────────────────┘
```

## 1. One contract

Everything that crosses the wire is defined in `proto/milo/`:

- `common.proto` has the rows: exercise, workout, workout exercise, set,
  template, body measurement, settings, and `Change`, which wraps any one of them.
- `sync.proto`, `auth.proto` and `agent.proto` have the three services.

`make gen` runs `buf generate`, which writes Go into `backend/gen` and Swift
into `ios/MiloAPI/Generated`. Nobody writes a request or response type by hand,
and an Android app would get its client the same way.

The services speak [Connect](https://connectrpc.com): plain HTTP, with streaming
for Milo's replies.

## 2. The phone owns the log

Logging has to work in a basement gym with no signal, so the app never waits on
the network to read or write.

- Every screen reads from a local SQLite database through `Live`, a small wrapper
  that re-runs a query when the rows behind it change. Screens don't fetch and
  don't hold copies; they watch. (`ios/Milo/Data/Live.swift`, `Store.swift`)
- Every write stamps the row's `updatedAt` and sets a `dirty` flag, in one
  transaction. (`StoreWrites.swift`)
- A couple of seconds after an edit, `SyncEngine` sends the dirty rows and asks
  for everything after its cursor. One call does both.

On the server, each synced row carries a `server_seq` drawn from one sequence.
Pull is "rows with a `server_seq` above your cursor". Pushes for one user are
serialised with an advisory lock, so sequence numbers commit in order and a pull
can never skip a row. (`backend/internal/sync`)

Conflicts are settled per row: **the newest `updated_at` wins**, on both sides.
Deletes are tombstones (`deleted_at`), so they sync like any other write. There
are no foreign keys between synced rows, because an offline phone may send a set
before the workout it belongs to.

Weights are stored exactly as typed, as text with their unit (`"45"`, `lb`).
Nothing is converted on the way in, so a 45 lb dumbbell never becomes 20.41 kg
in the log. Conversion happens only for display and for totals.

## 3. Milo proposes, the phone applies

Milo is a tool-using agent on the server (`backend/internal/agent`). It can read
the user's log from Postgres. It cannot write to it.

When Milo wants to change something it emits a **card**: a title, some display
lines, and `changes`, a list of rows exactly as sync would carry them. The phone
applies the card to its local database (`ChangeApplier.swift`) and the rows sync
up like any other edit. This buys three things:

- **One way to write.** The server has a single write path, sync. Milo can't
  corrupt the log in a way the app couldn't.
- **Undo for free.** Applying a card records what each row was before. Undo
  writes those back.
- **It works with offline edits.** A card names the versions of the rows it was
  based on. If one changed since Milo looked, the phone refuses the card as out
  of date rather than overwrite newer work.

Milo's tools come in four kinds, and the name says which:

| Kind | Examples | What happens |
| --- | --- | --- |
| Read | `get_workouts`, `exercise_history`, `training_summary` | Nothing changes |
| Do | `start_workout`, `log_sets`, `finish_workout` | The card applies on arrival and shows a line with Undo |
| Propose | `propose_change`, `propose_template`, `propose_delete` | The card waits for a tap |
| Show | `show_progress`, `show_chart`, `show_training`, `show_muscles` | A card to look at; its result hands Milo the numbers it drew |

The rule for which is which: adding what the person just told you is done
straight away; anything that rewrites or removes what's already logged waits for
a tap.

A few details worth knowing before changing the agent:

- **A turn sees its own work.** Because the phone applies cards, the database
  doesn't have a turn's rows yet. `turn` keeps what earlier tool calls in the
  same reply proposed, so "start push day, I did bench 60 for 8" can start the
  workout and then tick its first set. (`turn.go`)
- **Tool schemas come from Go structs.** `newTool` reflects a JSON schema from
  the input type, so a tool's schema and its code can't disagree. (`tools.go`)
- **The prompt is cached.** The system prompt and tool list never change between
  requests. Anything about the user or the moment (the time, the screen, the
  running workout) goes in the user turn. (`prompt.go`)
- **History is append-only.** A turn's messages are saved only when it finishes
  cleanly, so a dropped connection never leaves half an exchange behind.
  (`loop.go`, `turn.go`)
- **Reads run side by side.** Tool calls in one reply that only read run at
  once; anything that changes the turn runs in order. (`loop.go`)
- **Suggested replies ride on the answer.** An answer may end with a line
  starting `>>` that lists what the user might say next. The server holds that
  line back from the text and sends it as a card of buttons. It is text, not a
  tool, because the newer models treat words written before a tool call as
  progress notes, so an answer followed by a tool call would be lost. (`loop.go`)
- **The model is behind an interface.** Tests script a fake model's tool calls
  and check the cards; `make live` runs the same checks against the real one, and
  `make battery AS=username` asks about 60 real questions over a history in the
  local database and writes what Milo did with each.

## 4. The maths is written twice and tested once

Volume, effective load for bodyweight and assisted movements, estimated one-rep
max, new bests and calories are needed on the phone (offline) and on the server
(for Milo). So they are implemented in both `backend/internal/maths` and
`ios/Milo/Domain/Maths.swift`.

What keeps them equal is `testdata/maths/*.json`: named inputs with expected
results. The Go tests and the Swift tests both run every case. To change a rule,
change the vectors first; both suites fail until both implementations agree.

The rules in one breath: volume is weight × reps for done sets of loaded
exercises (× 2 when the reps were per side); bodyweight sets count your body
weight for top sets but add nothing to volume; a warm-up is logged but never
counted; e1RM is Epley, for 1 to 12 reps.

## Smaller things

- **Sign-in** is a username and password (argon2id), giving an opaque session
  token stored hashed. Sign-up needs an invite code. (`backend/internal/auth`)
- **Apple Health** is the phone's business alone: finished workouts and their
  active energy are written, body weight and a few details are read.
  (`ios/Milo/Health`)
- **Built-in exercises** are rows with no owner, seeded by a migration. Users'
  own exercises sit beside them in the same table.
