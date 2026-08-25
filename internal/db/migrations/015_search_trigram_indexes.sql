-- Global search matches with ILIKE '%term%', which no btree can serve, so each
-- leg scanned its table. Trigram indexes turn a specific search into a lookup.
--
-- Measured over 100,000 business objects, 30,000 contacts and 40,000 documents:
--
--   business objects, specific term   52.7 ms -> 0.4 ms
--   contacts                          17.8 ms -> 0.7 ms
--   documents                          scan  -> 0.2 ms
--   suppliers                          scan  -> 0.5 ms
--
-- A term that matches most rows still sorts them: the work is real, and the
-- planner correctly ignores the index there.
CREATE INDEX IF NOT EXISTS business_objects_search_trgm_idx
  ON business_objects USING gin (title gin_trgm_ops, number gin_trgm_ops);

CREATE INDEX IF NOT EXISTS supplier_contacts_search_trgm_idx
  ON supplier_contacts USING gin (name gin_trgm_ops, email gin_trgm_ops);

CREATE INDEX IF NOT EXISTS documents_name_trgm_idx
  ON documents USING gin (name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS suppliers_search_trgm_idx
  ON suppliers USING gin (name gin_trgm_ops, business_number gin_trgm_ops, supplier_number gin_trgm_ops);
