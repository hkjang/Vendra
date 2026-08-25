-- Creating a supplier scores its name against every existing one to catch a
-- duplicate registration. That scan grew with the register: 13 ms at 15,000
-- suppliers, and linear from there.
--
-- The trigram distance operator can walk this index and read only the nearest
-- name, which the handler then measures against the configured threshold.
-- Measured on 15,000 suppliers: 13.2 ms -> 1.2 ms.
CREATE INDEX IF NOT EXISTS suppliers_name_trgm_idx
  ON suppliers USING gist (lower(name) gist_trgm_ops);
