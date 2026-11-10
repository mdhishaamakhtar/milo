-- Milo's own state.

-- name: CreateConversation :one
INSERT INTO milo_conversations (id, user_id, title) VALUES ($1, $2, $3) RETURNING *;

-- name: GetConversation :one
SELECT * FROM milo_conversations WHERE id = $1 AND user_id = $2;

-- name: TouchConversation :exec
UPDATE milo_conversations SET updated_at = now() WHERE id = $1;

-- name: SetConversationSummary :exec
UPDATE milo_conversations SET summary = $2, updated_at = now() WHERE id = $1;

-- name: ListConversations :many
SELECT * FROM milo_conversations WHERE user_id = $1 ORDER BY updated_at DESC LIMIT $2;

-- name: InsertMiloItem :one
INSERT INTO milo_items (conversation_id, user_id, kind, payload, card_id, card_status)
VALUES ($1, $2, $3, $4, sqlc.narg(card_id)::uuid, sqlc.narg(card_status)::text)
RETURNING id, created_at;

-- name: ListMiloItems :many
SELECT * FROM milo_items WHERE conversation_id = $1 AND user_id = $2 ORDER BY id;

-- name: SetCardStatus :one
UPDATE milo_items SET card_status = sqlc.arg(status), status_unreported = true
WHERE card_id = sqlc.arg(card_id) AND user_id = sqlc.arg(user_id)
RETURNING conversation_id, payload;

-- name: UnreportedCards :many
SELECT card_id, card_status, payload FROM milo_items
WHERE conversation_id = $1 AND status_unreported ORDER BY id;

-- name: MarkCardsReported :exec
UPDATE milo_items SET status_unreported = false WHERE conversation_id = $1 AND status_unreported;

-- name: InsertAgentMessage :exec
INSERT INTO agent_messages (conversation_id, user_id, role, content) VALUES ($1, $2, $3, $4);

-- name: ListAgentMessages :many
SELECT role, content FROM agent_messages WHERE conversation_id = $1 ORDER BY id;

-- name: AddUsage :exec
INSERT INTO agent_usage (user_id, day, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, day) DO UPDATE SET
    input_tokens = agent_usage.input_tokens + EXCLUDED.input_tokens,
    output_tokens = agent_usage.output_tokens + EXCLUDED.output_tokens,
    cache_read_tokens = agent_usage.cache_read_tokens + EXCLUDED.cache_read_tokens,
    cache_write_tokens = agent_usage.cache_write_tokens + EXCLUDED.cache_write_tokens;

-- name: TokensUsedOn :one
SELECT COALESCE(sum(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0)::bigint
FROM agent_usage WHERE user_id = $1 AND day = $2;

-- What Milo reads. Everything is scoped to the user (and built-in exercises).

-- name: VisibleExercise :one
SELECT * FROM exercises WHERE id = $1 AND (user_id = $2 OR user_id IS NULL) AND deleted_at IS NULL;

-- name: SearchExercises :many
SELECT e.*,
    -- 1970 means never done.
    COALESCE((SELECT max(w.started_at) FROM workout_exercises we JOIN workouts w ON w.id = we.workout_id
     WHERE we.exercise_id = e.id AND we.user_id = sqlc.arg(user_id) AND we.deleted_at IS NULL AND w.deleted_at IS NULL
    ), 'epoch'::timestamptz)::timestamptz AS last_done
FROM exercises e
WHERE (e.user_id = sqlc.arg(user_id) OR e.user_id IS NULL) AND e.deleted_at IS NULL
  AND (lower(e.name) LIKE '%' || lower(sqlc.arg(query)::text) || '%'
       OR word_similarity(lower(sqlc.arg(query)::text), lower(e.name)) > 0.45
       OR EXISTS (SELECT 1 FROM unnest(e.aliases) a
                  WHERE lower(a) LIKE '%' || lower(sqlc.arg(query)::text) || '%'
                     OR word_similarity(lower(sqlc.arg(query)::text), lower(a)) > 0.45))
ORDER BY (e.user_id IS NOT NULL) DESC,
    GREATEST(word_similarity(lower(sqlc.arg(query)::text), lower(e.name)),
             similarity(lower(e.name), lower(sqlc.arg(query)::text))) DESC
LIMIT sqlc.arg(lim);

-- name: UsedExercises :many
-- The exercises a user has logged, most-used first, plus their own never-used ones.
SELECT e.id, e.name, e.aliases, e.measurement, e.unilateral, (e.user_id IS NOT NULL)::boolean AS custom,
    count(we.id)::bigint AS times
FROM exercises e
LEFT JOIN workout_exercises we ON we.exercise_id = e.id AND we.user_id = sqlc.arg(user_id) AND we.deleted_at IS NULL
WHERE e.deleted_at IS NULL AND (e.user_id = sqlc.arg(user_id) OR e.user_id IS NULL)
GROUP BY e.id
HAVING count(we.id) > 0 OR e.user_id IS NOT NULL
ORDER BY count(we.id) DESC, e.name;

-- name: RecentWorkouts :many
SELECT * FROM workouts WHERE user_id = $1 AND deleted_at IS NULL ORDER BY started_at DESC LIMIT $2;

-- name: WorkoutsBetween :many
SELECT * FROM workouts
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL
  AND started_at >= sqlc.arg(from_at) AND started_at < sqlc.arg(to_at)
ORDER BY started_at LIMIT sqlc.arg(lim);

-- name: WorkoutsByIDs :many
SELECT * FROM workouts WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL AND id = ANY(sqlc.arg(ids)::uuid[])
ORDER BY started_at;

-- name: WorkoutExercisesFor :many
SELECT we.*, e.name AS exercise_name, e.measurement, e.unilateral, e.met, e.approximate
FROM workout_exercises we JOIN exercises e ON e.id = we.exercise_id
WHERE we.user_id = sqlc.arg(user_id) AND we.deleted_at IS NULL AND we.workout_id = ANY(sqlc.arg(workout_ids)::uuid[])
ORDER BY we.workout_id, we.position;

-- name: SetsFor :many
SELECT id, workout_exercise_id, position, kind, reps, COALESCE(weight::text, '')::text AS weight, weight_unit,
    per_side, duration_s, distance_m, rpe, done, updated_at
FROM sets
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL AND workout_exercise_id = ANY(sqlc.arg(we_ids)::uuid[])
ORDER BY workout_exercise_id, position;

-- name: ExerciseSessions :many
-- The workouts an exercise was done in, newest first.
SELECT w.id AS workout_id, w.name AS workout_name, w.started_at, we.id AS workout_exercise_id
FROM workout_exercises we JOIN workouts w ON w.id = we.workout_id
WHERE we.user_id = sqlc.arg(user_id) AND we.exercise_id = sqlc.arg(exercise_id)
  AND we.deleted_at IS NULL AND w.deleted_at IS NULL
ORDER BY w.started_at DESC LIMIT sqlc.arg(lim);

-- name: ListTemplatesFor :many
SELECT * FROM templates WHERE user_id = $1 AND deleted_at IS NULL ORDER BY updated_at DESC;

-- name: LatestBodyWeight :one
SELECT weight_kg FROM body_measurements
WHERE user_id = sqlc.arg(user_id) AND deleted_at IS NULL AND weight_kg IS NOT NULL AND measured_at <= sqlc.arg(at)
ORDER BY measured_at DESC LIMIT 1;

-- name: GetSettingsFor :one
SELECT display_unit, rest_default_s FROM settings WHERE user_id = $1;

-- name: ExercisePrefFor :one
SELECT unit FROM exercise_prefs WHERE user_id = $1 AND exercise_id = $2 AND deleted_at IS NULL;

-- name: GetUserSet :one
SELECT id, workout_exercise_id, position, kind, reps, COALESCE(weight::text, '')::text AS weight, weight_unit,
    per_side, duration_s, distance_m, rpe, done, updated_at
FROM sets WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: GetUserWorkoutExercise :one
SELECT * FROM workout_exercises WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: GetUserWorkout :one
SELECT * FROM workouts WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: FindWorkoutExercise :one
SELECT * FROM workout_exercises
WHERE workout_id = $1 AND exercise_id = $2 AND user_id = $3 AND deleted_at IS NULL
ORDER BY position DESC LIMIT 1;

-- name: MaxExercisePosition :one
SELECT COALESCE(max(position), 0)::integer FROM workout_exercises WHERE workout_id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: MaxSetPosition :one
SELECT COALESCE(max(position), 0)::integer FROM sets WHERE workout_exercise_id = $1 AND user_id = $2 AND deleted_at IS NULL;
