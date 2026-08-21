-- One approval per request, enforced by the database. The application checked
-- before inserting, which concurrent submits could both pass.
UPDATE workflow_instances wi
SET status='superseded', completed_at=now()
WHERE wi.status='pending'
  AND EXISTS (
    SELECT 1 FROM workflow_instances o
    WHERE o.object_id = wi.object_id
      AND o.status = 'pending'
      AND (o.created_at, o.id) < (wi.created_at, wi.id));

CREATE UNIQUE INDEX IF NOT EXISTS workflow_instances_one_pending_idx
  ON workflow_instances(object_id) WHERE status='pending';
