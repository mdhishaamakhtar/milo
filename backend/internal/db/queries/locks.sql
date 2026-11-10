-- Lock keys use the two-int form so they never collide with single-key locks.
-- Space 1: one user's writes. Space 2: writes to shared rows (built-in exercises, added by migrations).

-- name: LockUserWrites :exec
-- Serialises one user's writes so their server_seq values commit in order.
SELECT pg_advisory_xact_lock(1, hashtext(sqlc.arg(user_id)::text));

-- name: LockSharedWritesShared :exec
-- Taken by every user write; waits while built-ins are being seeded.
SELECT pg_advisory_xact_lock_shared(2, 0);

-- name: LockSharedWritesExclusive :exec
-- Taken by a migration that adds built-ins (in SQL: SELECT pg_advisory_xact_lock(2, 0)),
-- so no user write commits a later server_seq meanwhile and no pull skips a built-in.
SELECT pg_advisory_xact_lock(2, 0);
