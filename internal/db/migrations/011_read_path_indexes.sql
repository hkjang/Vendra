-- Indexes for the read paths measured against enterprise-scale data, where
-- these tables were being scanned end to end on every request.

-- The administration audit list orders by time with no other filter, and this
-- table grows fastest of all. The existing index leads with object_type, so an
-- unfiltered listing could not use it.
CREATE INDEX IF NOT EXISTS audit_logs_occurred_idx ON audit_logs(occurred_at DESC);

-- Document listings filter by supplier and order by upload time.
CREATE INDEX IF NOT EXISTS documents_supplier_created_idx ON documents(supplier_id, created_at DESC);
CREATE INDEX IF NOT EXISTS documents_created_idx ON documents(created_at DESC);

-- Supplier 360 tabs: every one of these reads by supplier.
CREATE INDEX IF NOT EXISTS risks_supplier_idx ON risks(supplier_id);
CREATE INDEX IF NOT EXISTS evaluations_supplier_idx ON evaluations(supplier_id);
CREATE INDEX IF NOT EXISTS supplier_contacts_supplier_idx ON supplier_contacts(supplier_id);

-- Session listing and revocation, and the cascade when a user is removed.
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id);

-- Approval history, and the cascade when an instance is removed.
CREATE INDEX IF NOT EXISTS workflow_actions_instance_idx ON workflow_actions(instance_id);
CREATE INDEX IF NOT EXISTS workflow_instances_object_idx ON workflow_instances(object_id);

-- Notification dispatch joins deliveries back to their notification.
CREATE INDEX IF NOT EXISTS notification_deliveries_notification_idx ON notification_deliveries(notification_id);
CREATE INDEX IF NOT EXISTS notifications_user_idx ON notifications(user_id, created_at DESC);
