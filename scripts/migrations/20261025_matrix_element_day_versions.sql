-- Day versions of the drop and pattern matrix elements (#860). Every statement that writes drop_matrix_elements or
-- pattern_matrix_elements (INSERT, UPDATE, DELETE, MERGE, COPY, TRUNCATE, by any role or tool) gives each
-- (server, day_num) it touched a new version from matrix_element_day_version_seq, in its own transaction. A reader
-- that aggregated the elements of some days can then tell, in one snapshot, whether any of them changed since.
-- The same objects are in scripts/newdb/schema.sql (table, sequence) and scripts/newdb/indexes.sql (function,
-- triggers).
--
-- Apply as the role that owns the element tables (ENABLE ALWAYS TRIGGER needs the owner), which must not be a
-- superuser: it owns the version table and the function, which writes that table for every writer of the elements
-- (SECURITY DEFINER), so that a writer needs no privilege on it. Then let the backend role read the versions (the
-- grants are managed with the host, not here):
--   GRANT SELECT ON matrix_element_day_versions TO <backend role>;
-- CREATE TRIGGER locks each element table against writes (SHARE ROW EXCLUSIVE, readers go on) until the commit:
-- apply outside a calcwkr batch. Reverted by 20261025_matrix_element_day_versions_down.sql.
BEGIN;
SET LOCAL lock_timeout = '10s';

-- Rows are never deleted, and versions never reused: a (server, day) without a row has not been written since the
-- migration. server is text, as the element tables have an enum each. A TRUNCATE bumps ('*', -1), below every day
-- of every server.
CREATE SEQUENCE matrix_element_day_version_seq;
CREATE TABLE matrix_element_day_versions (
    tbl text NOT NULL CHECK (tbl IN ('drop', 'pattern')),
    server text NOT NULL,
    day_num integer NOT NULL,
    version bigint NOT NULL,
    CONSTRAINT matrix_element_day_versions_pkey PRIMARY KEY (tbl, server, day_num)
);

-- TG_ARGV[0] is the table, 'drop' or 'pattern'. The (server, day_num) of a statement are upserted in order, so that
-- statements touching several days lock their versions in the same order.
CREATE FUNCTION bump_matrix_element_day_versions() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER AS $$
BEGIN
    IF TG_OP = 'TRUNCATE' THEN
        INSERT INTO matrix_element_day_versions (tbl, server, day_num, version)
        VALUES (TG_ARGV[0], '*', -1, nextval('matrix_element_day_version_seq'))
        ON CONFLICT (tbl, server, day_num) DO UPDATE SET version = EXCLUDED.version;
    ELSIF TG_OP = 'INSERT' THEN
        INSERT INTO matrix_element_day_versions (tbl, server, day_num, version)
        SELECT TG_ARGV[0], c.server, c.day_num, nextval('matrix_element_day_version_seq')
        FROM (SELECT DISTINCT server::text AS server, day_num FROM new_rows ORDER BY 1, 2) AS c
        ON CONFLICT (tbl, server, day_num) DO UPDATE SET version = EXCLUDED.version;
    ELSIF TG_OP = 'DELETE' THEN
        INSERT INTO matrix_element_day_versions (tbl, server, day_num, version)
        SELECT TG_ARGV[0], c.server, c.day_num, nextval('matrix_element_day_version_seq')
        FROM (SELECT DISTINCT server::text AS server, day_num FROM old_rows ORDER BY 1, 2) AS c
        ON CONFLICT (tbl, server, day_num) DO UPDATE SET version = EXCLUDED.version;
    ELSE -- UPDATE: the days before and after, as day_num can change
        INSERT INTO matrix_element_day_versions (tbl, server, day_num, version)
        SELECT TG_ARGV[0], c.server, c.day_num, nextval('matrix_element_day_version_seq')
        FROM (SELECT server::text AS server, day_num FROM old_rows
              UNION SELECT server::text, day_num FROM new_rows ORDER BY 1, 2) AS c
        ON CONFLICT (tbl, server, day_num) DO UPDATE SET version = EXCLUDED.version;
    END IF;
    RETURN NULL;
END $$;
-- A SECURITY DEFINER function must not resolve its table and sequence through the search_path of the writer, nor
-- through a temporary table of the writer (pg_temp comes first unless it is listed): the schema the function is
-- created in, then pg_temp.
DO $$ BEGIN
    EXECUTE format('ALTER FUNCTION bump_matrix_element_day_versions() SET search_path = %I, pg_temp', current_schema());
END $$;
-- only the triggers call it
REVOKE ALL ON FUNCTION bump_matrix_element_day_versions() FROM PUBLIC;

-- One trigger per event, as a trigger with transition tables can have only one. ENABLE ALWAYS: they fire with
-- session_replication_role = replica too. Disabling one, or pg_restore --disable-triggers, changes their pg_trigger
-- rows (and leaves them at 'O' once enabled again), which readers check.
CREATE TRIGGER matrix_element_day_versions_ins AFTER INSERT ON drop_matrix_elements
    REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('drop');
CREATE TRIGGER matrix_element_day_versions_del AFTER DELETE ON drop_matrix_elements
    REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('drop');
CREATE TRIGGER matrix_element_day_versions_upd AFTER UPDATE ON drop_matrix_elements
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('drop');
CREATE TRIGGER matrix_element_day_versions_trunc AFTER TRUNCATE ON drop_matrix_elements
    FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('drop');
ALTER TABLE drop_matrix_elements
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_ins,
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_del,
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_upd,
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_trunc;

CREATE TRIGGER matrix_element_day_versions_ins AFTER INSERT ON pattern_matrix_elements
    REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('pattern');
CREATE TRIGGER matrix_element_day_versions_del AFTER DELETE ON pattern_matrix_elements
    REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('pattern');
CREATE TRIGGER matrix_element_day_versions_upd AFTER UPDATE ON pattern_matrix_elements
    REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('pattern');
CREATE TRIGGER matrix_element_day_versions_trunc AFTER TRUNCATE ON pattern_matrix_elements
    FOR EACH STATEMENT EXECUTE FUNCTION bump_matrix_element_day_versions('pattern');
ALTER TABLE pattern_matrix_elements
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_ins,
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_del,
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_upd,
    ENABLE ALWAYS TRIGGER matrix_element_day_versions_trunc;

COMMIT;
