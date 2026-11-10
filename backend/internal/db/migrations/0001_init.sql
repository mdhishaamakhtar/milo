-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Every synced row takes a fresh value from this sequence when it is written.
-- Writes for one user are serialised (advisory lock), so a pull by cursor never skips a row.
CREATE SEQUENCE sync_seq;

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    username      text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions(user_id);

-- user_id NULL = built-in exercise.
CREATE TABLE exercises (
    id                uuid PRIMARY KEY,
    user_id           uuid REFERENCES users(id) ON DELETE CASCADE,
    name              text NOT NULL,
    aliases           text[] NOT NULL DEFAULT '{}',
    primary_muscles   text[] NOT NULL DEFAULT '{}',
    secondary_muscles text[] NOT NULL DEFAULT '{}',
    movement_pattern  text NOT NULL DEFAULT '',
    equipment         text NOT NULL DEFAULT '',
    measurement       text NOT NULL DEFAULT 'reps_load'
        CHECK (measurement IN ('reps_load','bodyweight','assisted','timed','distance')),
    unilateral        boolean NOT NULL DEFAULT false,
    met               double precision,
    description       text NOT NULL DEFAULT '',
    approximate       boolean NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    server_seq        bigint NOT NULL DEFAULT nextval('sync_seq')
);
CREATE INDEX exercises_user_seq_idx ON exercises(user_id, server_seq);
CREATE INDEX exercises_builtin_seq_idx ON exercises(server_seq) WHERE user_id IS NULL;
CREATE INDEX exercises_name_trgm_idx ON exercises USING gin (lower(name) gin_trgm_ops);

CREATE TABLE exercise_prefs (
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    exercise_id uuid NOT NULL,
    unit        text NOT NULL DEFAULT 'kg' CHECK (unit IN ('kg','lb')),
    rest_s      integer,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    server_seq  bigint NOT NULL DEFAULT nextval('sync_seq'),
    PRIMARY KEY (user_id, exercise_id)
);
CREATE INDEX exercise_prefs_user_seq_idx ON exercise_prefs(user_id, server_seq);

CREATE TABLE settings (
    user_id        uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    display_unit   text NOT NULL DEFAULT 'kg' CHECK (display_unit IN ('kg','lb')),
    rest_default_s integer NOT NULL DEFAULT 60,
    height_cm      double precision,
    birth_date     text NOT NULL DEFAULT '' CHECK (birth_date ~ '^([0-9]{4}-[0-9]{2}-[0-9]{2})?$'),
    sex            text NOT NULL DEFAULT '',
    updated_at     timestamptz NOT NULL DEFAULT now(),
    server_seq     bigint NOT NULL DEFAULT nextval('sync_seq')
);

CREATE TABLE body_measurements (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    measured_at  timestamptz NOT NULL,
    weight_kg    double precision,
    body_fat_pct double precision,
    source       text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','health','hevy')),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    server_seq   bigint NOT NULL DEFAULT nextval('sync_seq')
);
CREATE INDEX body_measurements_user_seq_idx ON body_measurements(user_id, server_seq);
CREATE INDEX body_measurements_user_at_idx ON body_measurements(user_id, measured_at DESC);

CREATE TABLE workouts (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        text NOT NULL DEFAULT '',
    notes       text NOT NULL DEFAULT '',
    started_at  timestamptz NOT NULL,
    ended_at    timestamptz,
    template_id uuid,
    health_uuid uuid,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    server_seq  bigint NOT NULL DEFAULT nextval('sync_seq')
);
CREATE INDEX workouts_user_seq_idx ON workouts(user_id, server_seq);
CREATE INDEX workouts_user_started_idx ON workouts(user_id, started_at DESC);

-- No foreign keys between synced rows: they can arrive in any order from an offline phone.
CREATE TABLE workout_exercises (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workout_id  uuid NOT NULL,
    exercise_id uuid NOT NULL,
    position    integer NOT NULL DEFAULT 0,
    group_label text NOT NULL DEFAULT '',
    notes       text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    server_seq  bigint NOT NULL DEFAULT nextval('sync_seq')
);
CREATE INDEX workout_exercises_user_seq_idx ON workout_exercises(user_id, server_seq);
CREATE INDEX workout_exercises_workout_idx ON workout_exercises(workout_id);
CREATE INDEX workout_exercises_exercise_idx ON workout_exercises(user_id, exercise_id);

CREATE TABLE sets (
    id                  uuid PRIMARY KEY,
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workout_exercise_id uuid NOT NULL,
    position            integer NOT NULL DEFAULT 0,
    kind                text NOT NULL DEFAULT 'normal' CHECK (kind IN ('normal','warmup','drop','failure')),
    reps                integer,
    -- Exact weight as typed ('' for none), in weight_unit. Maths happens in the app and server code.
    weight              text NOT NULL DEFAULT '' CHECK (weight ~ '^([0-9]{1,6}(\.[0-9]{1,3})?)?$'),
    weight_unit         text NOT NULL DEFAULT 'kg' CHECK (weight_unit IN ('kg','lb')),
    per_side            boolean NOT NULL DEFAULT false,
    duration_s          integer,
    distance_m          double precision,
    rpe                 double precision,
    done                boolean NOT NULL DEFAULT false,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz,
    server_seq          bigint NOT NULL DEFAULT nextval('sync_seq')
);
CREATE INDEX sets_user_seq_idx ON sets(user_id, server_seq);
CREATE INDEX sets_we_idx ON sets(workout_exercise_id);

CREATE TABLE templates (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       text NOT NULL,
    notes      text NOT NULL DEFAULT '',
    -- [{"exercise_id": "...", "group_label": "4"}]
    items      jsonb NOT NULL DEFAULT '[]',
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    server_seq bigint NOT NULL DEFAULT nextval('sync_seq')
);
CREATE INDEX templates_user_seq_idx ON templates(user_id, server_seq);

CREATE TABLE milo_conversations (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title      text NOT NULL DEFAULT '',
    summary    text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX milo_conversations_user_idx ON milo_conversations(user_id, updated_at DESC);

-- What the app shows when a conversation is reopened.
CREATE TABLE milo_items (
    id              bigserial PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES milo_conversations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind            text NOT NULL CHECK (kind IN ('user','assistant','reading','card')),
    payload         jsonb NOT NULL,
    card_id         uuid,
    card_status     text CHECK (card_status IN ('pending','applied','dismissed','undone')),
    -- Set when the status changes; cleared once the change is reported to Milo.
    status_unreported boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX milo_items_conv_idx ON milo_items(conversation_id, id);
CREATE UNIQUE INDEX milo_items_card_idx ON milo_items(card_id) WHERE card_id IS NOT NULL;

-- The model's view of a conversation. Append-only: rows are never edited.
CREATE TABLE agent_messages (
    id              bigserial PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES milo_conversations(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('user','assistant')),
    content         jsonb NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_messages_conv_idx ON agent_messages(conversation_id, id);

CREATE TABLE agent_usage (
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day                date NOT NULL,
    input_tokens       bigint NOT NULL DEFAULT 0,
    output_tokens      bigint NOT NULL DEFAULT 0,
    cache_read_tokens  bigint NOT NULL DEFAULT 0,
    cache_write_tokens bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

-- +goose Down
DROP TABLE agent_usage, agent_messages, milo_items, milo_conversations, templates, sets,
    workout_exercises, workouts, body_measurements, settings, exercise_prefs, exercises, sessions, users;
DROP SEQUENCE sync_seq;
