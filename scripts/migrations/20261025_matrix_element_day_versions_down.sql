-- Reverts 20261025_matrix_element_day_versions.sql: the element tables are written without day versions again.
-- A refresh that finds the triggers missing calculates the global matrices in full, as before the migration.
BEGIN;
SET LOCAL lock_timeout = '10s';

DROP TRIGGER IF EXISTS matrix_element_day_versions_ins ON drop_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_del ON drop_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_upd ON drop_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_trunc ON drop_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_ins ON pattern_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_del ON pattern_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_upd ON pattern_matrix_elements;
DROP TRIGGER IF EXISTS matrix_element_day_versions_trunc ON pattern_matrix_elements;
DROP FUNCTION IF EXISTS bump_matrix_element_day_versions();
DROP TABLE IF EXISTS matrix_element_day_versions;
DROP SEQUENCE IF EXISTS matrix_element_day_version_seq;

COMMIT;
