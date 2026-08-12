CREATE TABLE sourcing_questions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sourcing_id uuid NOT NULL REFERENCES business_objects(id) ON DELETE CASCADE,
  supplier_id uuid REFERENCES suppliers(id),
  asked_by uuid NOT NULL REFERENCES users(id),
  question text NOT NULL,
  answer text,
  answered_by uuid REFERENCES users(id),
  visibility text NOT NULL DEFAULT 'participants' CHECK(visibility IN ('participants','private','internal')),
  asked_at timestamptz NOT NULL DEFAULT now(),
  answered_at timestamptz
);

CREATE TABLE sourcing_committee (
  sourcing_id uuid NOT NULL REFERENCES business_objects(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id),
  role text NOT NULL DEFAULT 'evaluator',
  appointed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(sourcing_id,user_id)
);

CREATE TABLE sourcing_selections (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  sourcing_id uuid NOT NULL REFERENCES business_objects(id) ON DELETE CASCADE,
  response_id uuid NOT NULL REFERENCES sourcing_responses(id),
  selection_type text NOT NULL CHECK(selection_type IN ('preferred','final')),
  reason text,
  selected_by uuid NOT NULL REFERENCES users(id),
  selected_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(sourcing_id,selection_type)
);

INSERT INTO settings(key,value,category) VALUES
 ('supplier.grades','[{"code":"S","min":90},{"code":"A","min":80},{"code":"B","min":70},{"code":"C","min":60},{"code":"D","min":0}]','supplier'),
 ('supplier.risk_levels','[{"code":"LOW","min":0},{"code":"MEDIUM","min":25},{"code":"HIGH","min":50},{"code":"CRITICAL","min":75}]','supplier'),
 ('supplier.types','["제조","유통","서비스","용역","IT"]','supplier'),
 ('procurement.categories','[]','procurement'),
 ('contract.types','["물품","서비스","용역"]','contract'),
 ('document.types','["business_registration","corporate_registry","financial_statement","security_pledge","nda","contract","quality_certificate","insurance","iso","proposal","quotation"]','document'),
 ('certification.types','["ISO 9001","ISO 14001","ISO 27001","ISMS"]','supplier'),
 ('risk.rules','{"continuousMonitoring":true,"criticalStopReview":true}','risk'),
 ('sla.rules','{"breachNotification":"immediate"}','contract'),
 ('rfp.templates','[]','procurement')
ON CONFLICT DO NOTHING;

UPDATE roles SET permissions = permissions || '["spend.create"]'::jsonb WHERE code='finance' AND NOT permissions ? 'spend.create';
