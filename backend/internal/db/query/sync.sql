-- Sync: last write wins per row. An upsert only lands when the incoming updated_at is not older
-- than the stored one, and only on the caller's own row; every landed write takes a new server_seq.
-- A 0-row result means "older, ignored" or "someone else's row" (check the owner to tell which).

-- name: UpsertExercise :execrows
INSERT INTO exercises (
    id, user_id, name, aliases, primary_muscles, secondary_muscles, movement_pattern, equipment,
    measurement, unilateral, met, description, approximate, updated_at, deleted_at
) VALUES (
    sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(aliases)::text[], sqlc.arg(primary_muscles)::text[],
    sqlc.arg(secondary_muscles)::text[], sqlc.arg(movement_pattern), sqlc.arg(equipment), sqlc.arg(measurement),
    sqlc.arg(unilateral), sqlc.narg(met)::double precision, sqlc.arg(description), sqlc.arg(approximate),
    sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz
)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name, aliases = EXCLUDED.aliases, primary_muscles = EXCLUDED.primary_muscles,
    secondary_muscles = EXCLUDED.secondary_muscles, movement_pattern = EXCLUDED.movement_pattern,
    equipment = EXCLUDED.equipment, measurement = EXCLUDED.measurement, unilateral = EXCLUDED.unilateral,
    met = EXCLUDED.met, description = EXCLUDED.description, approximate = EXCLUDED.approximate,
    updated_at = EXCLUDED.updated_at, deleted_at = EXCLUDED.deleted_at, server_seq = nextval('sync_seq')
WHERE exercises.user_id = EXCLUDED.user_id AND EXCLUDED.updated_at >= exercises.updated_at;

-- name: ExerciseOwner :one
-- NULL owner = built-in.
SELECT user_id FROM exercises WHERE id = $1;

-- name: PullExercises :many
SELECT * FROM exercises
WHERE (user_id = sqlc.arg(user_id) OR user_id IS NULL) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertExercisePref :execrows
INSERT INTO exercise_prefs (user_id, exercise_id, unit, rest_s, updated_at, deleted_at)
VALUES (sqlc.arg(user_id), sqlc.arg(exercise_id), sqlc.arg(unit), sqlc.narg(rest_s)::integer,
    sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz)
ON CONFLICT (user_id, exercise_id) DO UPDATE SET
    unit = EXCLUDED.unit, rest_s = EXCLUDED.rest_s, updated_at = EXCLUDED.updated_at,
    deleted_at = EXCLUDED.deleted_at, server_seq = nextval('sync_seq')
WHERE EXCLUDED.updated_at >= exercise_prefs.updated_at;

-- name: PullExercisePrefs :many
SELECT * FROM exercise_prefs
WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertSettings :execrows
INSERT INTO settings (user_id, display_unit, rest_default_s, height_cm, birth_date, sex, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(display_unit), sqlc.arg(rest_default_s), sqlc.narg(height_cm)::double precision,
    -- A birth date must be a real day; none is stored as '', as the column says.
    COALESCE(CAST(NULLIF(sqlc.arg(birth_date)::text, '') AS date)::text, ''), sqlc.arg(sex), sqlc.arg(updated_at))
ON CONFLICT (user_id) DO UPDATE SET
    display_unit = EXCLUDED.display_unit, rest_default_s = EXCLUDED.rest_default_s,
    height_cm = EXCLUDED.height_cm, birth_date = EXCLUDED.birth_date, sex = EXCLUDED.sex,
    updated_at = EXCLUDED.updated_at, server_seq = nextval('sync_seq')
WHERE EXCLUDED.updated_at >= settings.updated_at;

-- name: PullSettings :many
SELECT * FROM settings WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertBodyMeasurement :execrows
INSERT INTO body_measurements (id, user_id, measured_at, weight_kg, body_fat_pct, source, updated_at, deleted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(measured_at), sqlc.narg(weight_kg)::double precision,
    sqlc.narg(body_fat_pct)::double precision, sqlc.arg(source), sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    measured_at = EXCLUDED.measured_at, weight_kg = EXCLUDED.weight_kg, body_fat_pct = EXCLUDED.body_fat_pct,
    source = EXCLUDED.source, updated_at = EXCLUDED.updated_at, deleted_at = EXCLUDED.deleted_at,
    server_seq = nextval('sync_seq')
WHERE body_measurements.user_id = EXCLUDED.user_id AND EXCLUDED.updated_at >= body_measurements.updated_at;

-- name: BodyMeasurementOwner :one
SELECT user_id FROM body_measurements WHERE id = $1;

-- name: PullBodyMeasurements :many
SELECT * FROM body_measurements
WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertWorkout :execrows
INSERT INTO workouts (id, user_id, name, notes, started_at, ended_at, template_id, health_uuid, updated_at, deleted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(notes), sqlc.arg(started_at),
    sqlc.narg(ended_at)::timestamptz, sqlc.narg(template_id)::uuid, sqlc.narg(health_uuid)::uuid,
    sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name, notes = EXCLUDED.notes, started_at = EXCLUDED.started_at, ended_at = EXCLUDED.ended_at,
    template_id = EXCLUDED.template_id, health_uuid = EXCLUDED.health_uuid, updated_at = EXCLUDED.updated_at,
    deleted_at = EXCLUDED.deleted_at, server_seq = nextval('sync_seq')
WHERE workouts.user_id = EXCLUDED.user_id AND EXCLUDED.updated_at >= workouts.updated_at;

-- name: WorkoutOwner :one
SELECT user_id FROM workouts WHERE id = $1;

-- name: PullWorkouts :many
SELECT * FROM workouts
WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertWorkoutExercise :execrows
INSERT INTO workout_exercises (id, user_id, workout_id, exercise_id, position, group_label, notes, updated_at, deleted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(workout_id), sqlc.arg(exercise_id), sqlc.arg(position),
    sqlc.arg(group_label), sqlc.arg(notes), sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    workout_id = EXCLUDED.workout_id, exercise_id = EXCLUDED.exercise_id, position = EXCLUDED.position,
    group_label = EXCLUDED.group_label, notes = EXCLUDED.notes, updated_at = EXCLUDED.updated_at,
    deleted_at = EXCLUDED.deleted_at, server_seq = nextval('sync_seq')
WHERE workout_exercises.user_id = EXCLUDED.user_id AND EXCLUDED.updated_at >= workout_exercises.updated_at;

-- name: WorkoutExerciseOwner :one
SELECT user_id FROM workout_exercises WHERE id = $1;

-- name: PullWorkoutExercises :many
SELECT * FROM workout_exercises
WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertSet :execrows
INSERT INTO sets (id, user_id, workout_exercise_id, position, kind, reps, weight, weight_unit, per_side,
    duration_s, distance_m, rpe, done, updated_at, deleted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(workout_exercise_id), sqlc.arg(position), sqlc.arg(kind),
    sqlc.narg(reps)::integer, sqlc.arg(weight), sqlc.arg(weight_unit),
    sqlc.arg(per_side), sqlc.narg(duration_s)::integer, sqlc.narg(distance_m)::double precision,
    sqlc.narg(rpe)::double precision, sqlc.arg(done), sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    workout_exercise_id = EXCLUDED.workout_exercise_id, position = EXCLUDED.position, kind = EXCLUDED.kind,
    reps = EXCLUDED.reps, weight = EXCLUDED.weight, weight_unit = EXCLUDED.weight_unit,
    per_side = EXCLUDED.per_side, duration_s = EXCLUDED.duration_s, distance_m = EXCLUDED.distance_m,
    rpe = EXCLUDED.rpe, done = EXCLUDED.done, updated_at = EXCLUDED.updated_at,
    deleted_at = EXCLUDED.deleted_at, server_seq = nextval('sync_seq')
WHERE sets.user_id = EXCLUDED.user_id AND EXCLUDED.updated_at >= sets.updated_at;

-- name: SetOwner :one
SELECT user_id FROM sets WHERE id = $1;

-- name: PullSets :many
SELECT * FROM sets WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);

-- name: UpsertTemplate :execrows
INSERT INTO templates (id, user_id, name, notes, items, updated_at, deleted_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(notes), sqlc.arg(items)::jsonb,
    sqlc.arg(updated_at), sqlc.narg(deleted_at)::timestamptz)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name, notes = EXCLUDED.notes, items = EXCLUDED.items, updated_at = EXCLUDED.updated_at,
    deleted_at = EXCLUDED.deleted_at, server_seq = nextval('sync_seq')
WHERE templates.user_id = EXCLUDED.user_id AND EXCLUDED.updated_at >= templates.updated_at;

-- name: TemplateOwner :one
SELECT user_id FROM templates WHERE id = $1;

-- name: PullTemplates :many
SELECT * FROM templates
WHERE user_id = sqlc.arg(user_id) AND server_seq > sqlc.arg(cursor)
ORDER BY server_seq LIMIT sqlc.arg(lim);
