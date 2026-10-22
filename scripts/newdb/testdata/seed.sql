-- Synthetic data of the old database for test.sh: reports from 2026-06 to 2026-10 (test.sh migrates from 2026-08),
-- with recruitment reports (stage 9, copied like every other report), reports on the bounds of a month, reports
-- without drop_report_extras and extras without report, and some rows in every other table.

INSERT INTO zones (zone_id, ark_zone_id, index, category, name, existence)
VALUES (1, 'main_0', 0, 'MAINLINE', '{"en": "Prologue"}', '{}'), (2, 'recruit', 1, 'RECRUIT', '{"en": "Recruit"}', '{}');
SELECT setval('zones_zone_id_seq', 2);

INSERT INTO stages (stage_id, ark_stage_id, stage_type, zone_id, code, sanity, existence)
SELECT i, 'main_00-0' || i, 'MAIN', 1, jsonb_build_object('en', '0-' || i), 6, '{}' FROM generate_series(1, 5) i;
INSERT INTO stages (stage_id, ark_stage_id, zone_id, code, existence) VALUES (9, 'recruit', 2, '{"en": "Recruit"}', '{}');
SELECT setval('stages_stage_id_seq', 9);

-- items 1-10 materials, 11-15 recruitment tags
INSERT INTO items (item_id, ark_item_id, existence, name, sort_id, rarity, type, keywords)
SELECT i, CASE WHEN i <= 10 THEN '300' || i ELSE 'tag_' || i END, '{}', jsonb_build_object('en', 'item ' || i), i, 1,
    CASE WHEN i <= 10 THEN 'MATERIAL' ELSE 'RECRUIT_TAG' END, '{}'
FROM generate_series(1, 15) i;
SELECT setval('items_item_id_seq', 15);

INSERT INTO accounts (penguin_id, weight, tags, created_at)
SELECT lpad(i::text, 8, '0'), 1, CASE WHEN i % 7 = 0 THEN '{internal}'::varchar[] END, '2026-01-01 00:00:00+00'
FROM generate_series(1, 50) i;

-- pattern 1 is empty; 2-40 materials; 41-60 recruitment tags; 61 has a tag twice, 62 a tag and a material
INSERT INTO drop_patterns (hash, original_fingerprint) SELECT md5(i::text), 'p' || i FROM generate_series(1, 62) i;
INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity)
SELECT p, 1 + (p * 7 + k) % 10, 1 + (p + k) % 3 FROM generate_series(2, 40) p, generate_series(0, p % 3) k;
INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity)
SELECT p, 11 + (p + k * 2) % 5, 1 FROM generate_series(41, 60) p, generate_series(0, p % 3) k;
INSERT INTO drop_pattern_elements (drop_pattern_id, item_id, quantity) VALUES (61, 12, 2), (61, 14, 1), (62, 13, 1), (62, 4, 1);

-- 37 reports every 7 hours, 40% of them recruitment reports; report ids from the sequence, in created_at order
INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id, source_name, version)
SELECT CASE WHEN n % 5 < 2 THEN 9 ELSE 1 + n % 5 END,
    CASE WHEN n % 5 < 2 THEN 41 + n % 22 ELSE 1 + n % 40 END,
    CASE WHEN n % 11 = 0 THEN 1 + n % 6 ELSE 1 END,
    t + (n % 37) * interval '11 minutes 13.123456 seconds',
    CASE WHEN n % 13 = 0 THEN 1 WHEN n % 17 = 0 THEN -1 ELSE 0 END,
    (ARRAY['CN', 'CN', 'CN', 'US', 'JP', 'KR'])[1 + n % 6]::enum_drop_reports_server,
    1 + n % 50,
    (ARRAY['frontend-v2', 'MeoAssistant', 'penguin-stats.io', NULL])[1 + n % 4],
    CASE WHEN n % 3 = 0 THEN 'v' || n % 9 END
FROM generate_series(timestamptz '2026-06-01 00:00:00+00', '2026-10-21 00:00:00+00', interval '7 hours')
    WITH ORDINALITY AS g(t, d), generate_series(1, 37) n
ORDER BY 4;

-- on the bounds of the first hot month (2026-08)
INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id)
VALUES (1, 2, 1, '2026-07-31 23:59:59.999999+00', 0, 'CN', 1), (9, 41, 1, '2026-07-31 23:59:59.999999+00', 0, 'CN', 1),
    (1, 2, 1, '2026-08-01 00:00:00+00', 0, 'CN', 2), (9, 1, 1, '2026-08-01 00:00:00+00', 0, 'CN', 2),
    (2, 3, 1, '2026-09-30 23:59:59.999999+00', 0, 'US', 3), (9, 61, 1, '2026-10-01 00:00:00+00', 0, 'KR', 3);

-- before the coldstore epoch (2019-05): in the "pre" archive of the cold store, not in a month of it
INSERT INTO drop_reports (stage_id, pattern_id, times, created_at, reliability, server, account_id)
VALUES (1, 2, 1, '1970-01-01 00:00:00+00', 0, 'CN', 4), (9, 41, 1, '2019-04-30 23:59:59.999999+00', 0, 'CN', 4);

-- extras of every report but 1 in 19; md5 and metadata on some
INSERT INTO drop_report_extras (report_id, ip, metadata, md5)
SELECT report_id, ('10.' || report_id % 256 || '.' || report_id / 256 % 256 || '.' || report_id % 7)::cidr,
    CASE WHEN report_id % 5 = 0 THEN jsonb_build_object('fingerprint', md5(report_id::text), 'md5', md5(report_id::text)) END,
    CASE WHEN report_id % 10 = 0 THEN md5(report_id::text) END
FROM drop_reports WHERE report_id % 19 <> 0;
-- extras without report
INSERT INTO drop_report_extras (report_id, ip) VALUES (1, '127.0.0.1'), (2, '::1');

INSERT INTO time_ranges (name, start_time, end_time, server)
VALUES ('cn_1', '2026-01-01 00:00:00+00', '2026-12-01 00:00:00+00', 'CN');
INSERT INTO drop_infos (server, stage_id, item_id, drop_type, range_id, accumulable)
SELECT 'CN', s, s + 1, 'REGULAR', 1, true FROM generate_series(1, 5) s;
INSERT INTO drop_matrix_elements (stage_id, item_id, quantity, times, server, day_num)
SELECT 1 + i % 5, 1 + i % 10, i, i * 2, 'CN', 20000 + i FROM generate_series(1, 100) i;
INSERT INTO pattern_matrix_elements (stage_id, pattern_id, quantity, times, server, day_num)
SELECT 1 + i % 5, 2 + i % 39, i, i * 2, 'CN', 20000 + i FROM generate_series(1, 100) i;
INSERT INTO trend_elements (stage_id, item_id, group_id, start_time, end_time, quantity, times, server)
SELECT 1 + i % 5, 1 + i % 10, i, '2026-10-01 00:00:00+00', '2026-10-02 00:00:00+00', i, i, 'CN' FROM generate_series(1, 20) i;
INSERT INTO activities (start_time, end_time, name, existence)
VALUES ('2026-10-01 00:00:00+00', '2026-10-15 00:00:00+00', '{"en": "event"}', '{}');
INSERT INTO notices (existence, severity, content) VALUES ('{}', 1, '{"en": "notice"}');
INSERT INTO properties (key, value) VALUES ('frontend_config', '{}');
INSERT INTO snapshots (key, version, content) VALUES ('shim', 'v1', 'x');
INSERT INTO reject_rules (expr, with_reliability) VALUES ('false', 256);
INSERT INTO recognition_defects (defect_id, session_id, environment) VALUES ('d1', 's1', '{}');
