-- 0003_seed.sql — bootstrap the single admin account from environment.
--
-- The password hash is inserted by the migrator (it needs bcrypt, which SQL
-- cannot do). This file only creates the row shape and is a no-op if the
-- migrator already created the user.

BEGIN;

-- Nothing to insert here by design: see internal/store/postgres/migrate.go,
-- which runs EnsureAdminUser after migrations. Kept as a numbered step so the
-- ordering of future data migrations stays obvious.

COMMIT;
