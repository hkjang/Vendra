-- Two list screens sort on an expression, so there was nothing to read in order:
-- every matching row was fetched and sorted to return the first few hundred.
--
-- Measured over 100,000 business objects and 30,000 risks:
--
--   risk list         57.8 ms -> 1.9 ms
--   work inbox due    41.0 ms -> 1.0 ms
--
-- The index also gives the planner statistics for the expression. Without them
-- it estimated 402 rows where 70,646 matched, judged a sort trivial, and chose
-- a plan that could not stop early.
CREATE INDEX IF NOT EXISTS risks_severity_score_idx
  ON risks((CASE severity WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END), score DESC);

CREATE INDEX IF NOT EXISTS business_objects_due_idx
  ON business_objects((CASE WHEN object_type='contract' THEN end_date ELSE due_date END));
