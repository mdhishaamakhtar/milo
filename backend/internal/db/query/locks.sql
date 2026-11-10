-- Lock keys use the two-int form so they never collide with single-key locks.
-- Space 1: one user's writes. Space 2: writes to shared rows (built-in exercises, added by migrations).

-- name: LockUserWrites :exec
-- Serialises one user's writes so their server_seq values commit in order.
SELECT pg_advisory_xact_lock(1, hashtext(sqlc.arg(user_id)::text));

-- name: LockAgainstBuiltinInserts :exec
-- Taken (shared) by every user write, so a migration adding built-ins waits for them and they for it.
SELECT pg_advisory_xact_lock_shared(2, 0);

-- A migration that adds built-ins takes the same lock exclusively in its own SQL:
-- SELECT pg_advisory_xact_lock(2, 0);
