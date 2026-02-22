-- Reset historical data for persona model v2 rollout.
-- Usage:
--   psql "$DB_DSN" -f scripts/reset_soul_history.sql

BEGIN;

TRUNCATE TABLE
  mem0_async_jobs,
  memory_episode,
  messages,
  sessions,
  terminal_soul_bindings,
  souls
RESTART IDENTITY CASCADE;

COMMIT;
