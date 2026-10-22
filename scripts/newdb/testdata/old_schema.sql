-- Schema of the old (production, PostgreSQL 14) database, for test.sh only: pg_dump --schema-only --no-owner
-- --no-privileges -n public of a restore of production on 2026-10-21 (without the extensions and the datadog schema).

-- Dumped from database version 14.24 (Ubuntu 14.24-1.pgdg24.04+2)
-- Dumped by pg_dump version 14.24 (Ubuntu 14.24-1.pgdg24.04+2)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: enum_drop_infos_drop_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_drop_infos_drop_type AS ENUM (
    'REGULAR',
    'SPECIAL',
    'EXTRA',
    'FURNITURE',
    'RECOGNITION_ONLY'
);

--
-- Name: enum_drop_infos_server; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_drop_infos_server AS ENUM (
    'CN',
    'US',
    'JP',
    'KR'
);

--
-- Name: enum_drop_matrix_elements_server; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_drop_matrix_elements_server AS ENUM (
    'CN',
    'US',
    'JP',
    'KR'
);

--
-- Name: enum_drop_reports_server; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_drop_reports_server AS ENUM (
    'CN',
    'US',
    'JP',
    'KR'
);

--
-- Name: enum_pattern_matrix_elements_server; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_pattern_matrix_elements_server AS ENUM (
    'CN',
    'US',
    'JP',
    'KR'
);

--
-- Name: enum_stages_extra_process_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_stages_extra_process_type AS ENUM (
    'GACHABOX'
);

--
-- Name: enum_stages_stage_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_stages_stage_type AS ENUM (
    'MAIN',
    'SUB',
    'ACTIVITY',
    'DAILY'
);

--
-- Name: enum_time_ranges_server; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_time_ranges_server AS ENUM (
    'CN',
    'US',
    'JP',
    'KR'
);

--
-- Name: enum_trend_elements_server; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.enum_trend_elements_server AS ENUM (
    'CN',
    'US',
    'JP',
    'KR'
);

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.accounts (
    account_id bigint NOT NULL,
    penguin_id character varying(32) NOT NULL,
    weight double precision NOT NULL,
    tags character varying(32)[],
    created_at timestamp with time zone NOT NULL
);

--
-- Name: accounts_account_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.accounts_account_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: accounts_account_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.accounts_account_id_seq OWNED BY public.accounts.account_id;

--
-- Name: activities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.activities (
    activity_id bigint NOT NULL,
    start_time timestamp with time zone NOT NULL,
    end_time timestamp with time zone NOT NULL,
    name jsonb NOT NULL,
    existence jsonb NOT NULL
);

--
-- Name: activities_activity_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.activities_activity_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: activities_activity_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.activities_activity_id_seq OWNED BY public.activities.activity_id;

--
-- Name: drop_infos; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drop_infos (
    drop_id bigint NOT NULL,
    server public.enum_drop_infos_server NOT NULL,
    stage_id integer NOT NULL,
    item_id integer,
    drop_type public.enum_drop_infos_drop_type NOT NULL,
    range_id bigint NOT NULL,
    bounds jsonb,
    accumulable boolean NOT NULL,
    extras jsonb
);

--
-- Name: drop_infos_drop_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.drop_infos_drop_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: drop_infos_drop_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.drop_infos_drop_id_seq OWNED BY public.drop_infos.drop_id;

--
-- Name: drop_matrix_elements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drop_matrix_elements (
    element_id bigint NOT NULL,
    stage_id integer NOT NULL,
    item_id integer NOT NULL,
    quantity integer NOT NULL,
    times integer NOT NULL,
    server public.enum_drop_matrix_elements_server NOT NULL,
    quantity_buckets jsonb,
    source_category character varying(32) DEFAULT 'all'::character varying NOT NULL,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    day_num integer NOT NULL
);

--
-- Name: drop_matrix_elements_element_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.drop_matrix_elements_element_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: drop_matrix_elements_element_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.drop_matrix_elements_element_id_seq OWNED BY public.drop_matrix_elements.element_id;

--
-- Name: drop_pattern_elements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drop_pattern_elements (
    element_id bigint NOT NULL,
    drop_pattern_id bigint NOT NULL,
    item_id integer NOT NULL,
    quantity integer NOT NULL
);

--
-- Name: drop_pattern_elements_element_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.drop_pattern_elements_element_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: drop_pattern_elements_element_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.drop_pattern_elements_element_id_seq OWNED BY public.drop_pattern_elements.element_id;

--
-- Name: drop_patterns; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drop_patterns (
    pattern_id bigint NOT NULL,
    hash character varying(64) NOT NULL,
    original_fingerprint text
);

--
-- Name: drop_patterns_pattern_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.drop_patterns_pattern_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: drop_patterns_pattern_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.drop_patterns_pattern_id_seq OWNED BY public.drop_patterns.pattern_id;

--
-- Name: drop_report_extras; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drop_report_extras (
    report_id bigint NOT NULL,
    ip cidr NOT NULL,
    metadata jsonb,
    md5 character varying(32)
);

--
-- Name: drop_reports; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.drop_reports (
    report_id bigint NOT NULL,
    stage_id integer NOT NULL,
    pattern_id bigint NOT NULL,
    times integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    reliability integer NOT NULL,
    server public.enum_drop_reports_server NOT NULL,
    account_id bigint NOT NULL,
    source_name text,
    version text
);

--
-- Name: drop_reports_report_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.drop_reports_report_id_seq
    START WITH 4988523
    INCREMENT BY 1
    MINVALUE 4988523
    NO MAXVALUE
    CACHE 1;

--
-- Name: drop_reports_report_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.drop_reports_report_id_seq OWNED BY public.drop_reports.report_id;

--
-- Name: items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.items (
    item_id integer NOT NULL,
    ark_item_id character varying(32) NOT NULL,
    existence jsonb NOT NULL,
    name jsonb NOT NULL,
    sort_id integer NOT NULL,
    rarity integer NOT NULL,
    type character varying(16),
    "group" character varying(16),
    sprite character varying(16),
    keywords jsonb NOT NULL
);

--
-- Name: items_item_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.items_item_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: items_item_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.items_item_id_seq OWNED BY public.items.item_id;

--
-- Name: notices; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.notices (
    notice_id integer NOT NULL,
    existence jsonb,
    severity integer,
    content jsonb NOT NULL
);

--
-- Name: notices_notice_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.notices_notice_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: notices_notice_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.notices_notice_id_seq OWNED BY public.notices.notice_id;

--
-- Name: pattern_matrix_elements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pattern_matrix_elements (
    element_id bigint NOT NULL,
    stage_id integer NOT NULL,
    pattern_id bigint NOT NULL,
    quantity integer NOT NULL,
    times integer NOT NULL,
    server public.enum_pattern_matrix_elements_server NOT NULL,
    source_category character varying(32) DEFAULT 'all'::character varying NOT NULL,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    day_num integer NOT NULL
);

--
-- Name: pattern_matrix_elements_element_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.pattern_matrix_elements_element_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: pattern_matrix_elements_element_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.pattern_matrix_elements_element_id_seq OWNED BY public.pattern_matrix_elements.element_id;

--
-- Name: properties; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.properties (
    property_id integer NOT NULL,
    key character varying(32) NOT NULL,
    value text NOT NULL
);

--
-- Name: properties_property_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.properties_property_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: properties_property_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.properties_property_id_seq OWNED BY public.properties.property_id;

--
-- Name: recognition_defects; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.recognition_defects (
    defect_id character varying(32) NOT NULL,
    created_at timestamp without time zone DEFAULT now() NOT NULL,
    updated_at timestamp without time zone,
    session_id character varying(8) NOT NULL,
    account_id bigint,
    image_uri text,
    recognition_result json,
    environment jsonb NOT NULL
);

--
-- Name: reject_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.reject_rules (
    rule_id integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone,
    status integer DEFAULT 1,
    expr text NOT NULL,
    with_reliability integer NOT NULL
);

--
-- Name: COLUMN reject_rules.status; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.reject_rules.status IS '1: activated; others: non-activated';

--
-- Name: COLUMN reject_rules.with_reliability; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.reject_rules.with_reliability IS 'only in range [256, 4096)';

--
-- Name: reject_rules_rule_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.reject_rules_rule_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: reject_rules_rule_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.reject_rules_rule_id_seq OWNED BY public.reject_rules.rule_id;

--
-- Name: snapshots_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.snapshots_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.snapshots (
    snapshot_id integer DEFAULT nextval('public.snapshots_id_seq'::regclass) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    key text NOT NULL,
    version text NOT NULL,
    content text
);

--
-- Name: stages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.stages (
    stage_id integer NOT NULL,
    ark_stage_id character varying(32) NOT NULL,
    stage_type public.enum_stages_stage_type,
    zone_id integer NOT NULL,
    extra_process_type public.enum_stages_extra_process_type,
    code jsonb NOT NULL,
    sanity integer,
    existence jsonb NOT NULL,
    min_clear_time integer
);

--
-- Name: stages_stage_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.stages_stage_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: stages_stage_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.stages_stage_id_seq OWNED BY public.stages.stage_id;

--
-- Name: time_ranges; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.time_ranges (
    range_id bigint NOT NULL,
    name character varying(64),
    start_time timestamp with time zone NOT NULL,
    end_time timestamp with time zone NOT NULL,
    comment text,
    server public.enum_time_ranges_server NOT NULL
);

--
-- Name: time_ranges_range_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.time_ranges_range_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: time_ranges_range_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.time_ranges_range_id_seq OWNED BY public.time_ranges.range_id;

--
-- Name: trend_elements; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.trend_elements (
    element_id bigint NOT NULL,
    stage_id integer NOT NULL,
    item_id integer NOT NULL,
    group_id integer NOT NULL,
    start_time timestamp with time zone NOT NULL,
    end_time timestamp with time zone NOT NULL,
    quantity integer NOT NULL,
    times integer NOT NULL,
    server public.enum_trend_elements_server NOT NULL,
    source_category character varying(32) DEFAULT 'all'::character varying NOT NULL
);

--
-- Name: trend_elements_element_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.trend_elements_element_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: trend_elements_element_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.trend_elements_element_id_seq OWNED BY public.trend_elements.element_id;

--
-- Name: zones; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.zones (
    zone_id integer NOT NULL,
    ark_zone_id character varying(32) NOT NULL,
    index integer NOT NULL,
    category character varying(32) NOT NULL,
    type character varying(64),
    name jsonb NOT NULL,
    existence jsonb NOT NULL,
    background character varying(128)
);

--
-- Name: zones_zone_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.zones_zone_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: zones_zone_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.zones_zone_id_seq OWNED BY public.zones.zone_id;

--
-- Name: accounts account_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts ALTER COLUMN account_id SET DEFAULT nextval('public.accounts_account_id_seq'::regclass);

--
-- Name: activities activity_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activities ALTER COLUMN activity_id SET DEFAULT nextval('public.activities_activity_id_seq'::regclass);

--
-- Name: drop_infos drop_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_infos ALTER COLUMN drop_id SET DEFAULT nextval('public.drop_infos_drop_id_seq'::regclass);

--
-- Name: drop_matrix_elements element_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_matrix_elements ALTER COLUMN element_id SET DEFAULT nextval('public.drop_matrix_elements_element_id_seq'::regclass);

--
-- Name: drop_pattern_elements element_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_pattern_elements ALTER COLUMN element_id SET DEFAULT nextval('public.drop_pattern_elements_element_id_seq'::regclass);

--
-- Name: drop_patterns pattern_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_patterns ALTER COLUMN pattern_id SET DEFAULT nextval('public.drop_patterns_pattern_id_seq'::regclass);

--
-- Name: drop_reports report_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_reports ALTER COLUMN report_id SET DEFAULT nextval('public.drop_reports_report_id_seq'::regclass);

--
-- Name: items item_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.items ALTER COLUMN item_id SET DEFAULT nextval('public.items_item_id_seq'::regclass);

--
-- Name: notices notice_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notices ALTER COLUMN notice_id SET DEFAULT nextval('public.notices_notice_id_seq'::regclass);

--
-- Name: pattern_matrix_elements element_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pattern_matrix_elements ALTER COLUMN element_id SET DEFAULT nextval('public.pattern_matrix_elements_element_id_seq'::regclass);

--
-- Name: properties property_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.properties ALTER COLUMN property_id SET DEFAULT nextval('public.properties_property_id_seq'::regclass);

--
-- Name: reject_rules rule_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reject_rules ALTER COLUMN rule_id SET DEFAULT nextval('public.reject_rules_rule_id_seq'::regclass);

--
-- Name: stages stage_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stages ALTER COLUMN stage_id SET DEFAULT nextval('public.stages_stage_id_seq'::regclass);

--
-- Name: time_ranges range_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.time_ranges ALTER COLUMN range_id SET DEFAULT nextval('public.time_ranges_range_id_seq'::regclass);

--
-- Name: trend_elements element_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trend_elements ALTER COLUMN element_id SET DEFAULT nextval('public.trend_elements_element_id_seq'::regclass);

--
-- Name: zones zone_id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.zones ALTER COLUMN zone_id SET DEFAULT nextval('public.zones_zone_id_seq'::regclass);

--
-- Name: accounts accounts_penguin_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_penguin_id_key UNIQUE (penguin_id);

--
-- Name: accounts accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.accounts
    ADD CONSTRAINT accounts_pkey PRIMARY KEY (account_id);

--
-- Name: activities activities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.activities
    ADD CONSTRAINT activities_pkey PRIMARY KEY (activity_id);

--
-- Name: drop_infos drop_infos_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_infos
    ADD CONSTRAINT drop_infos_pkey PRIMARY KEY (drop_id);

--
-- Name: drop_matrix_elements drop_matrix_elements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_matrix_elements
    ADD CONSTRAINT drop_matrix_elements_pkey PRIMARY KEY (element_id);

--
-- Name: drop_pattern_elements drop_pattern_elements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_pattern_elements
    ADD CONSTRAINT drop_pattern_elements_pkey PRIMARY KEY (element_id);

--
-- Name: drop_patterns drop_patterns_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_patterns
    ADD CONSTRAINT drop_patterns_pkey PRIMARY KEY (pattern_id);

--
-- Name: drop_report_extras drop_report_extras_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_report_extras
    ADD CONSTRAINT drop_report_extras_pkey PRIMARY KEY (report_id);

--
-- Name: drop_reports drop_reports_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_reports
    ADD CONSTRAINT drop_reports_pkey PRIMARY KEY (report_id);

--
-- Name: items items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.items
    ADD CONSTRAINT items_pkey PRIMARY KEY (item_id);

--
-- Name: notices notices_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.notices
    ADD CONSTRAINT notices_pkey PRIMARY KEY (notice_id);

--
-- Name: pattern_matrix_elements pattern_matrix_elements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pattern_matrix_elements
    ADD CONSTRAINT pattern_matrix_elements_pkey PRIMARY KEY (element_id);

--
-- Name: properties properties_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.properties
    ADD CONSTRAINT properties_pkey PRIMARY KEY (property_id);

--
-- Name: recognition_defects recognition_defects_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.recognition_defects
    ADD CONSTRAINT recognition_defects_pkey PRIMARY KEY (defect_id);

--
-- Name: reject_rules reject_rules_pk; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.reject_rules
    ADD CONSTRAINT reject_rules_pk PRIMARY KEY (rule_id);

--
-- Name: snapshots snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.snapshots
    ADD CONSTRAINT snapshots_pkey PRIMARY KEY (snapshot_id);

--
-- Name: stages stages_ark_stage_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stages
    ADD CONSTRAINT stages_ark_stage_id_key UNIQUE (ark_stage_id);

--
-- Name: stages stages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stages
    ADD CONSTRAINT stages_pkey PRIMARY KEY (stage_id);

--
-- Name: time_ranges time_ranges_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.time_ranges
    ADD CONSTRAINT time_ranges_pkey PRIMARY KEY (range_id);

--
-- Name: trend_elements trend_elements_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trend_elements
    ADD CONSTRAINT trend_elements_pkey PRIMARY KEY (element_id);

--
-- Name: zones zones_ark_zone_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.zones
    ADD CONSTRAINT zones_ark_zone_id_key UNIQUE (ark_zone_id);

--
-- Name: zones zones_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.zones
    ADD CONSTRAINT zones_pkey PRIMARY KEY (zone_id);

--
-- Name: accounts_tags; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX accounts_tags ON public.accounts USING gin (tags);

--
-- Name: drop_infos_drop_type_item_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_infos_drop_type_item_id ON public.drop_infos USING btree (drop_type, item_id);

--
-- Name: drop_infos_item_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_infos_item_id ON public.drop_infos USING btree (item_id);

--
-- Name: drop_infos_range_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_infos_range_id ON public.drop_infos USING btree (range_id);

--
-- Name: drop_infos_server; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_infos_server ON public.drop_infos USING btree (server);

--
-- Name: drop_infos_stage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_infos_stage_id ON public.drop_infos USING btree (stage_id);

--
-- Name: drop_matrix_elements_day_num; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_elements_day_num ON public.drop_matrix_elements USING btree (day_num);

--
-- Name: drop_matrix_elements_end_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_elements_end_time ON public.drop_matrix_elements USING btree (end_time);

--
-- Name: drop_matrix_elements_item_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_elements_item_id ON public.drop_matrix_elements USING btree (item_id);

--
-- Name: drop_matrix_elements_server; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_elements_server ON public.drop_matrix_elements USING btree (server);

--
-- Name: drop_matrix_elements_stage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_elements_stage_id ON public.drop_matrix_elements USING btree (stage_id);

--
-- Name: drop_matrix_elements_start_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_elements_start_time ON public.drop_matrix_elements USING btree (start_time);

--
-- Name: drop_matrix_source_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_matrix_source_category ON public.drop_matrix_elements USING btree (source_category);

--
-- Name: drop_pattern_elements_drop_pattern_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_pattern_elements_drop_pattern_id ON public.drop_pattern_elements USING btree (drop_pattern_id);

--
-- Name: drop_patterns_hash; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX drop_patterns_hash ON public.drop_patterns USING btree (hash);

--
-- Name: drop_patterns_original_fingerprint; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_patterns_original_fingerprint ON public.drop_patterns USING btree (original_fingerprint);

--
-- Name: drop_reports_account_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_account_id ON public.drop_reports USING btree (account_id);

--
-- Name: drop_reports_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_created_at ON public.drop_reports USING btree (created_at);

--
-- Name: drop_reports_created_at_brin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_created_at_brin ON public.drop_reports USING brin (created_at);

--
-- Name: drop_reports_created_at_brin32; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_created_at_brin32 ON public.drop_reports USING brin (created_at) WITH (pages_per_range='32', autosummarize='on');

--
-- Name: drop_reports_pattern_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_pattern_id ON public.drop_reports USING btree (pattern_id);

--
-- Name: drop_reports_reliability; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_reliability ON public.drop_reports USING btree (reliability);

--
-- Name: drop_reports_stage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX drop_reports_stage_id ON public.drop_reports USING btree (stage_id);

--
-- Name: items_ark_item_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX items_ark_item_id ON public.items USING btree (ark_item_id);

--
-- Name: items_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX items_group ON public.items USING btree ("group");

--
-- Name: pattern_matrix_elements_day_num; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pattern_matrix_elements_day_num ON public.pattern_matrix_elements USING btree (day_num);

--
-- Name: pattern_matrix_elements_end_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pattern_matrix_elements_end_time ON public.pattern_matrix_elements USING btree (end_time);

--
-- Name: pattern_matrix_elements_server; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pattern_matrix_elements_server ON public.pattern_matrix_elements USING btree (server);

--
-- Name: pattern_matrix_elements_stage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pattern_matrix_elements_stage_id ON public.pattern_matrix_elements USING btree (stage_id);

--
-- Name: pattern_matrix_elements_start_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pattern_matrix_elements_start_time ON public.pattern_matrix_elements USING btree (start_time);

--
-- Name: pattern_matrix_source_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX pattern_matrix_source_category ON public.pattern_matrix_elements USING btree (source_category);

--
-- Name: properties_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX properties_key ON public.properties USING btree (key);

--
-- Name: recognition_defects_account_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX recognition_defects_account_id_idx ON public.recognition_defects USING btree (account_id);

--
-- Name: recognition_defects_environment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX recognition_defects_environment_idx ON public.recognition_defects USING btree (environment);

--
-- Name: recognition_defects_session_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX recognition_defects_session_id_idx ON public.recognition_defects USING btree (session_id);

--
-- Name: reject_rules_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX reject_rules_status ON public.reject_rules USING btree (status);

--
-- Name: snapshots_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX snapshots_created_at ON public.snapshots USING btree (created_at);

--
-- Name: snapshots_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX snapshots_key ON public.snapshots USING btree (key);

--
-- Name: snapshots_version; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX snapshots_version ON public.snapshots USING btree (version);

--
-- Name: stages_zone_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX stages_zone_id ON public.stages USING btree (zone_id);

--
-- Name: time_ranges_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX time_ranges_name ON public.time_ranges USING btree (name);

--
-- Name: time_ranges_server; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX time_ranges_server ON public.time_ranges USING btree (server);

--
-- Name: trend_elements_item_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trend_elements_item_id ON public.trend_elements USING btree (item_id);

--
-- Name: trend_elements_server; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trend_elements_server ON public.trend_elements USING btree (server);

--
-- Name: trend_elements_source_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trend_elements_source_category ON public.trend_elements USING btree (source_category);

--
-- Name: trend_elements_stage_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trend_elements_stage_id ON public.trend_elements USING btree (stage_id);

--
-- Name: trend_elements_start_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX trend_elements_start_time ON public.trend_elements USING btree (start_time);

--
-- Name: drop_infos drop_infos_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_infos
    ADD CONSTRAINT drop_infos_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: drop_infos drop_infos_range_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_infos
    ADD CONSTRAINT drop_infos_range_id_fkey FOREIGN KEY (range_id) REFERENCES public.time_ranges(range_id);

--
-- Name: drop_infos drop_infos_stage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_infos
    ADD CONSTRAINT drop_infos_stage_id_fkey FOREIGN KEY (stage_id) REFERENCES public.stages(stage_id);

--
-- Name: drop_matrix_elements drop_matrix_elements_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_matrix_elements
    ADD CONSTRAINT drop_matrix_elements_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: drop_matrix_elements drop_matrix_elements_stage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_matrix_elements
    ADD CONSTRAINT drop_matrix_elements_stage_id_fkey FOREIGN KEY (stage_id) REFERENCES public.stages(stage_id);

--
-- Name: drop_pattern_elements drop_pattern_elements_drop_pattern_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_pattern_elements
    ADD CONSTRAINT drop_pattern_elements_drop_pattern_id_fkey FOREIGN KEY (drop_pattern_id) REFERENCES public.drop_patterns(pattern_id);

--
-- Name: drop_pattern_elements drop_pattern_elements_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_pattern_elements
    ADD CONSTRAINT drop_pattern_elements_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: drop_reports drop_reports_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_reports
    ADD CONSTRAINT drop_reports_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.accounts(account_id);

--
-- Name: drop_reports drop_reports_pattern_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_reports
    ADD CONSTRAINT drop_reports_pattern_id_fkey FOREIGN KEY (pattern_id) REFERENCES public.drop_patterns(pattern_id);

--
-- Name: drop_reports drop_reports_stage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.drop_reports
    ADD CONSTRAINT drop_reports_stage_id_fkey FOREIGN KEY (stage_id) REFERENCES public.stages(stage_id);

--
-- Name: pattern_matrix_elements pattern_matrix_elements_pattern_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pattern_matrix_elements
    ADD CONSTRAINT pattern_matrix_elements_pattern_id_fkey FOREIGN KEY (pattern_id) REFERENCES public.drop_patterns(pattern_id);

--
-- Name: pattern_matrix_elements pattern_matrix_elements_stage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pattern_matrix_elements
    ADD CONSTRAINT pattern_matrix_elements_stage_id_fkey FOREIGN KEY (stage_id) REFERENCES public.stages(stage_id);

--
-- Name: stages stages_zone_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.stages
    ADD CONSTRAINT stages_zone_id_fkey FOREIGN KEY (zone_id) REFERENCES public.zones(zone_id);

--
-- Name: trend_elements trend_elements_item_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trend_elements
    ADD CONSTRAINT trend_elements_item_id_fkey FOREIGN KEY (item_id) REFERENCES public.items(item_id);

--
-- Name: trend_elements trend_elements_stage_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trend_elements
    ADD CONSTRAINT trend_elements_stage_id_fkey FOREIGN KEY (stage_id) REFERENCES public.stages(stage_id);

