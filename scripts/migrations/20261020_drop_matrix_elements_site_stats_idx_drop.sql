-- Supersedes 20261020_drop_matrix_elements_site_stats_idx.sql. The planner also picks that (stage_id, item_id) ordered
-- index for the global drop matrix queries (GetAll*ForGlobalDropMatrix), which then fetch heap rows in stage/item
-- order: CN quantities 0.47 s -> 1.77 s and quantity buckets 1.31 s -> 2.12 s per time range, more than the times
-- query gains. Its only other user, the site stats base rebuild (once per 6 hrs per server), runs as an index-only
-- scan of drop_matrix_elements_site_stats_by_day instead (2.1 s -> 4.2 s for CN).
-- Run outside a transaction; CONCURRENTLY keeps the table writable while dropping.
DROP INDEX CONCURRENTLY IF EXISTS drop_matrix_elements_site_stats;
