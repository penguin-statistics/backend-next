-- Superseded: dropped again by 20261020_drop_matrix_elements_site_stats_idx_drop.sql.
-- Site stats (calcwkr siteStats microtask) aggregates every drop_matrix_elements row of a server for
-- source_category = 'all'. This covering index lets those aggregations run as index-only scans in
-- (stage_id, item_id) order instead of fetching ~4M heap rows (CN) and spilling a hash aggregate.
-- Run outside a transaction; CONCURRENTLY keeps the table writable while building.
CREATE INDEX CONCURRENTLY IF NOT EXISTS drop_matrix_elements_site_stats
    ON drop_matrix_elements (server, source_category, stage_id, item_id, times, day_num)
    INCLUDE (quantity);
