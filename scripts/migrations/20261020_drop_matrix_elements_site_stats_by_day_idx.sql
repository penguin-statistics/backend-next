-- Site stats keep the aggregation of past days in memory and only rescan the most recent days of a server every
-- worker batch. This index serves that range scan (server, source_category = 'all', day_num >= ...) as an
-- index-only scan, as is the rebuild of the aggregation of past days (day_num < ...).
-- Run outside a transaction; CONCURRENTLY keeps the table writable while building.
CREATE INDEX CONCURRENTLY IF NOT EXISTS drop_matrix_elements_site_stats_by_day
    ON drop_matrix_elements (server, source_category, day_num)
    INCLUDE (stage_id, item_id, times, quantity);
