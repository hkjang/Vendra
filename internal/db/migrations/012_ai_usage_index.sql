-- The hourly AI budget counts an actor's recent calls out of the audit trail.
-- Without this the count falls back to the time-ordered index and filters, which
-- grows with everything else happening in that hour.
CREATE INDEX IF NOT EXISTS audit_logs_actor_occurred_idx
  ON audit_logs(actor_id, action, occurred_at DESC);
