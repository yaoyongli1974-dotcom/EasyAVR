-- Read-only role for agent access (AGENTS.md "查询数据库（只读）").
-- Runs once, on an empty data directory, via docker-entrypoint-initdb.d.
-- Local dev credentials match the rest of this compose file; do not reuse in production.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'easyavr_ro') THEN
    CREATE ROLE easyavr_ro LOGIN PASSWORD 'easyavr_ro';
  END IF;
END
$$;

GRANT CONNECT ON DATABASE easyavr TO easyavr_ro;
GRANT USAGE ON SCHEMA public TO easyavr_ro;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO easyavr_ro;

-- Tables created later by AutoMigrate (running as easyavr) must also be readable.
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO easyavr_ro;
