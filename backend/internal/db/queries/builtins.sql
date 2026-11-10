-- Built-in exercises are inserted once by migrations (0002_builtin_exercises.sql and later ones);
-- the server never rewrites them at runtime.

-- name: CountBuiltinExercises :one
SELECT count(*) FROM exercises WHERE user_id IS NULL AND deleted_at IS NULL;

-- name: GetBuiltinExerciseByName :one
SELECT * FROM exercises WHERE user_id IS NULL AND lower(name) = lower(sqlc.arg(name));
