UPDATE roles SET permissions='["supplier.*","purchase_request.*","rfq.*","rfp.*","purchase_order.*","delivery.*","inspection.*","quality.*","issue.*","risk.*","evaluation.*","contract.read","document.*","dashboard.read","spend.read","analytics.read","workflow.read","workflow.approve","ai.use"]' WHERE code='procurement_manager';
UPDATE roles SET permissions='["supplier.read","contract.*","document.*","workflow.read","workflow.approve","risk.contract.*","dashboard.read"]' WHERE code='contract_manager';
UPDATE roles SET permissions='["supplier.read","purchase_request.create","purchase_request.read","inspection.*","evaluation.create","document.read","dashboard.read"]' WHERE code='business_user';
UPDATE roles SET permissions='["supplier.read","spend.read","analytics.read","purchase_order.read","purchase_order.amount.read","contract.read","contract.amount.read","invoice.*","payment.*","workflow.read","workflow.approve","dashboard.read"]' WHERE code='finance';
UPDATE roles SET permissions='["supplier.read","risk.read","risk.security.*","evaluation.read","evaluation.security.*","document.read","workflow.read","workflow.approve"]' WHERE code='security';
UPDATE roles SET permissions='["supplier.read","contract.read","contract.review","risk.read","risk.compliance.*","document.read","workflow.read","workflow.approve"]' WHERE code='legal';
UPDATE roles SET permissions='["audit.read","*.read","dashboard.read"]' WHERE code='auditor';
UPDATE roles SET permissions='["dashboard.read","analytics.read","spend.read","supplier.read","contract.read","risk.read","evaluation.read","issue.read"]' WHERE code='executive';

CREATE TABLE IF NOT EXISTS supplier_relationships (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  source_supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
  target_supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
  relationship_type text NOT NULL,
  criticality text NOT NULL DEFAULT 'normal',
  supplied_categories jsonb NOT NULL DEFAULT '[]',
  dependency_percent numeric(7,2),
  notes text,
  valid_from date,
  valid_until date,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(source_supplier_id,target_supplier_id,relationship_type)
);

CREATE TABLE IF NOT EXISTS notifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid REFERENCES users(id) ON DELETE CASCADE,
  supplier_id uuid REFERENCES suppliers(id) ON DELETE CASCADE,
  kind text NOT NULL,
  title text NOT NULL,
  body text NOT NULL,
  severity text NOT NULL DEFAULT 'info',
  object_type text,
  object_id uuid,
  read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(user_id,kind,object_type,object_id,title)
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  adapter text NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  attempts integer NOT NULL DEFAULT 0,
  response text,
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS document_signatures (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  signer_id uuid NOT NULL REFERENCES users(id),
  signature_type text NOT NULL DEFAULT 'approval',
  signature_metadata jsonb NOT NULL DEFAULT '{}',
  signed_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(document_id,signer_id,signature_type)
);

CREATE TABLE IF NOT EXISTS role_field_permissions (
  role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
  object_type text NOT NULL,
  field_name text NOT NULL,
  access text NOT NULL CHECK(access IN ('none','masked','read','write')),
  PRIMARY KEY(role_id,object_type,field_name)
);
