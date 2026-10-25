-- Schema of the new PostgreSQL 17 database (#752): the hot reports of the last months, partitioned by month, and
-- every other table as it is today. Apply this file, load the data, then apply indexes.sql (secondary indexes and
-- foreign keys), as migrate.sh does; for an empty database apply both files in a row.
--
-- Object names are unqualified, so the file can be applied in any schema of the search_path (the integration tests
-- use a schema of their own). Extensions (pg_stat_statements), the datadog schema and the grants are set up with the
-- host, not here.
--
-- Partitioning drop_reports and drop_report_extras by created_at:
--
-- - The archive job detaches and drops the partition of a month once it is in the cold store: no DELETE, no bloat.
--   create_report_partitions creates the partitions of a month for both tables; it must run ahead of time (the
--   migration creates the months after the hot ones, the report worker the current and the next month every hour):
--   there is no default partition, so a report of a month without partition fails to insert instead of landing where
--   no archive finds it.
--   created_at is the time the API accepted the report (types.ReportTask.CreatedAt), never set by clients.
-- - A unique constraint of a partitioned table must contain the partition key, so the primary keys are
--   (report_id, created_at). report_id stays unique because every report id comes from drop_reports_report_id_seq,
--   as the recall key of a task and the admin tools expect. Lookups by report_id alone use the primary key of every
--   partition (one probe each).
-- - drop_report_extras gains created_at, copied from its report when written, so it is partitioned (and archived)
--   with the same months as drop_reports.
-- - No foreign key references these tables, and nothing upserts into them (no ON CONFLICT target to adapt).
--   Foreign keys from a partitioned table to the dimension tables work as before (indexes.sql).

CREATE TYPE enum_drop_infos_drop_type AS ENUM ('REGULAR', 'SPECIAL', 'EXTRA', 'FURNITURE', 'RECOGNITION_ONLY');
CREATE TYPE enum_drop_infos_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE enum_drop_matrix_elements_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE enum_drop_reports_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE enum_pattern_matrix_elements_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE enum_stages_extra_process_type AS ENUM ('GACHABOX');
CREATE TYPE enum_stages_stage_type AS ENUM ('MAIN', 'SUB', 'ACTIVITY', 'DAILY');
CREATE TYPE enum_time_ranges_server AS ENUM ('CN', 'US', 'JP', 'KR');
CREATE TYPE enum_trend_elements_server AS ENUM ('CN', 'US', 'JP', 'KR');

-- reports

-- the minimum is that of today's sequence, which started above the ids of the reports of v2
CREATE SEQUENCE drop_reports_report_id_seq START WITH 4988523 MINVALUE 4988523;

CREATE TABLE drop_reports (
    report_id bigint DEFAULT nextval('drop_reports_report_id_seq') NOT NULL,
    stage_id integer NOT NULL,
    pattern_id bigint NOT NULL,
    times integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    reliability integer NOT NULL,
    server enum_drop_reports_server NOT NULL,
    account_id bigint NOT NULL,
    source_name text,
    version text,
    CONSTRAINT drop_reports_pkey PRIMARY KEY (report_id, created_at)
) PARTITION BY RANGE (created_at);
ALTER SEQUENCE drop_reports_report_id_seq OWNED BY drop_reports.report_id;

CREATE TABLE drop_report_extras (
    report_id bigint NOT NULL,
    ip cidr NOT NULL,
    metadata jsonb,
    md5 character varying(32),
    -- created_at of the report, the partition key
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT drop_report_extras_pkey PRIMARY KEY (report_id, created_at)
) PARTITION BY RANGE (created_at);

-- create_report_partitions creates the partitions of the month of p_month (UTC) for drop_reports and
-- drop_report_extras, named <table>_YYYY_MM, unless they exist. The indexes of the parent tables are created on the
-- new partitions as well.
CREATE FUNCTION create_report_partitions(p_month date) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE
    month_start timestamp := date_trunc('month', p_month::timestamp);
    -- UTC bounds; month arithmetic on timestamp (without time zone) does not depend on the session time zone
    lo text := to_char(month_start, 'YYYY-MM-DD HH24:MI:SS') || '+00';
    hi text := to_char(month_start + interval '1 month', 'YYYY-MM-DD HH24:MI:SS') || '+00';
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['drop_reports', 'drop_report_extras'] LOOP
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
            t || '_' || to_char(month_start, 'YYYY_MM'), t, lo, hi);
    END LOOP;
END;
$$;

-- every other table, as today

CREATE TABLE accounts (
    account_id bigserial NOT NULL,
    penguin_id character varying(32) NOT NULL,
    weight double precision NOT NULL,
    tags character varying(32)[],
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT accounts_pkey PRIMARY KEY (account_id),
    CONSTRAINT accounts_penguin_id_key UNIQUE (penguin_id)
);

CREATE TABLE activities (
    activity_id bigserial NOT NULL,
    start_time timestamp with time zone NOT NULL,
    end_time timestamp with time zone NOT NULL,
    name jsonb NOT NULL,
    existence jsonb NOT NULL,
    CONSTRAINT activities_pkey PRIMARY KEY (activity_id)
);

CREATE TABLE drop_infos (
    drop_id bigserial NOT NULL,
    server enum_drop_infos_server NOT NULL,
    stage_id integer NOT NULL,
    item_id integer,
    drop_type enum_drop_infos_drop_type NOT NULL,
    range_id bigint NOT NULL,
    bounds jsonb,
    accumulable boolean NOT NULL,
    extras jsonb,
    CONSTRAINT drop_infos_pkey PRIMARY KEY (drop_id)
);

CREATE TABLE drop_matrix_elements (
    element_id bigserial NOT NULL,
    stage_id integer NOT NULL,
    item_id integer NOT NULL,
    quantity integer NOT NULL,
    times integer NOT NULL,
    server enum_drop_matrix_elements_server NOT NULL,
    quantity_buckets jsonb,
    source_category character varying(32) DEFAULT 'all'::character varying NOT NULL,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    day_num integer NOT NULL,
    CONSTRAINT drop_matrix_elements_pkey PRIMARY KEY (element_id)
);

CREATE TABLE drop_pattern_elements (
    element_id bigserial NOT NULL,
    drop_pattern_id bigint NOT NULL,
    item_id integer NOT NULL,
    quantity integer NOT NULL,
    CONSTRAINT drop_pattern_elements_pkey PRIMARY KEY (element_id)
);

CREATE TABLE drop_patterns (
    pattern_id bigserial NOT NULL,
    hash character varying(64) NOT NULL,
    original_fingerprint text,
    CONSTRAINT drop_patterns_pkey PRIMARY KEY (pattern_id)
);
-- unique: GetOrCreateDropPatternFromDrops looks patterns up by hash and must never store one twice
CREATE UNIQUE INDEX drop_patterns_hash ON drop_patterns USING btree (hash);

CREATE TABLE items (
    item_id serial NOT NULL,
    ark_item_id character varying(32) NOT NULL,
    existence jsonb NOT NULL,
    name jsonb NOT NULL,
    sort_id integer NOT NULL,
    rarity integer NOT NULL,
    type character varying(16),
    "group" character varying(16),
    sprite character varying(16),
    keywords jsonb NOT NULL,
    CONSTRAINT items_pkey PRIMARY KEY (item_id)
);

-- scripts/migrations/20261025_matrix_element_day_versions.sql: the day versions of drop_matrix_elements and
-- pattern_matrix_elements, written by the triggers of indexes.sql. Rows are never deleted, and versions never reused:
-- a (server, day) without a row has not been written since the triggers exist. server is text, as the element tables
-- have an enum each. A TRUNCATE bumps ('*', -1), below every day of every server. migrate.sh copies no row of it.
CREATE SEQUENCE matrix_element_day_version_seq;
CREATE TABLE matrix_element_day_versions (
    tbl text NOT NULL CHECK (tbl IN ('drop', 'pattern')),
    server text NOT NULL,
    day_num integer NOT NULL,
    version bigint NOT NULL,
    CONSTRAINT matrix_element_day_versions_pkey PRIMARY KEY (tbl, server, day_num)
);

CREATE TABLE notices (
    notice_id serial NOT NULL,
    existence jsonb,
    severity integer,
    content jsonb NOT NULL,
    CONSTRAINT notices_pkey PRIMARY KEY (notice_id)
);

CREATE TABLE pattern_matrix_elements (
    element_id bigserial NOT NULL,
    stage_id integer NOT NULL,
    pattern_id bigint NOT NULL,
    quantity integer NOT NULL,
    times integer NOT NULL,
    server enum_pattern_matrix_elements_server NOT NULL,
    source_category character varying(32) DEFAULT 'all'::character varying NOT NULL,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    day_num integer NOT NULL,
    CONSTRAINT pattern_matrix_elements_pkey PRIMARY KEY (element_id)
);

CREATE TABLE properties (
    property_id serial NOT NULL,
    key character varying(32) NOT NULL,
    value text NOT NULL,
    CONSTRAINT properties_pkey PRIMARY KEY (property_id)
);

CREATE TABLE recognition_defects (
    defect_id character varying(32) NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    updated_at timestamp without time zone,
    session_id character varying(8) NOT NULL,
    account_id bigint,
    image_uri text,
    recognition_result json,
    environment jsonb NOT NULL,
    CONSTRAINT recognition_defects_pkey PRIMARY KEY (defect_id)
);

CREATE TABLE reject_rules (
    rule_id serial NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone,
    status integer DEFAULT 1,
    expr text NOT NULL,
    with_reliability integer NOT NULL,
    CONSTRAINT reject_rules_pk PRIMARY KEY (rule_id)
);
COMMENT ON COLUMN reject_rules.status IS '1: activated; others: non-activated';
COMMENT ON COLUMN reject_rules.with_reliability IS 'only in range [256, 4096)';

-- not owned by snapshots.snapshot_id today either
CREATE SEQUENCE snapshots_id_seq;
CREATE TABLE snapshots (
    snapshot_id integer DEFAULT nextval('snapshots_id_seq') NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    key text NOT NULL,
    version text NOT NULL,
    content text,
    CONSTRAINT snapshots_pkey PRIMARY KEY (snapshot_id)
);

CREATE TABLE stages (
    stage_id serial NOT NULL,
    ark_stage_id character varying(32) NOT NULL,
    stage_type enum_stages_stage_type,
    zone_id integer NOT NULL,
    extra_process_type enum_stages_extra_process_type,
    code jsonb NOT NULL,
    sanity integer,
    existence jsonb NOT NULL,
    min_clear_time integer,
    CONSTRAINT stages_pkey PRIMARY KEY (stage_id),
    CONSTRAINT stages_ark_stage_id_key UNIQUE (ark_stage_id)
);

CREATE TABLE time_ranges (
    range_id bigserial NOT NULL,
    name character varying(64),
    start_time timestamp with time zone NOT NULL,
    end_time timestamp with time zone NOT NULL,
    comment text,
    server enum_time_ranges_server NOT NULL,
    CONSTRAINT time_ranges_pkey PRIMARY KEY (range_id)
);

CREATE TABLE trend_elements (
    element_id bigserial NOT NULL,
    stage_id integer NOT NULL,
    item_id integer NOT NULL,
    group_id integer NOT NULL,
    start_time timestamp with time zone NOT NULL,
    end_time timestamp with time zone NOT NULL,
    quantity integer NOT NULL,
    times integer NOT NULL,
    server enum_trend_elements_server NOT NULL,
    source_category character varying(32) DEFAULT 'all'::character varying NOT NULL,
    CONSTRAINT trend_elements_pkey PRIMARY KEY (element_id)
);

CREATE TABLE zones (
    zone_id serial NOT NULL,
    ark_zone_id character varying(32) NOT NULL,
    index integer NOT NULL,
    category character varying(32) NOT NULL,
    type character varying(64),
    name jsonb NOT NULL,
    existence jsonb NOT NULL,
    background character varying(128),
    CONSTRAINT zones_pkey PRIMARY KEY (zone_id),
    CONSTRAINT zones_ark_zone_id_key UNIQUE (ark_zone_id)
);
