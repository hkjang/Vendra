CREATE TABLE screening_templates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, active boolean NOT NULL DEFAULT true,
  items jsonb NOT NULL, result_rules jsonb NOT NULL DEFAULT '{}', required_document_types jsonb NOT NULL DEFAULT '[]',
  created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE TABLE supplier_screenings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), supplier_id uuid NOT NULL REFERENCES suppliers(id), template_id uuid NOT NULL REFERENCES screening_templates(id),
  status text NOT NULL DEFAULT 'draft', responses jsonb NOT NULL DEFAULT '{}', domain_results jsonb NOT NULL DEFAULT '{}', result text CHECK(result IN ('PASS','CONDITIONAL_PASS','REVIEW_REQUIRED','REJECT')),
  reviewer_id uuid REFERENCES users(id), comments text, submitted_at timestamptz, completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sourcing_participants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), sourcing_id uuid NOT NULL REFERENCES business_objects(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES suppliers(id), status text NOT NULL DEFAULT 'invited', invited_at timestamptz NOT NULL DEFAULT now(),
  viewed_at timestamptz, declined_at timestamptz, decline_reason text, UNIQUE(sourcing_id,supplier_id)
);
CREATE TABLE sourcing_responses (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), sourcing_id uuid NOT NULL REFERENCES business_objects(id) ON DELETE CASCADE,
  supplier_id uuid NOT NULL REFERENCES suppliers(id), status text NOT NULL DEFAULT 'draft', currency text NOT NULL DEFAULT 'KRW',
  total_amount numeric(20,2), delivery_days integer, warranty text, validity_date date, commercial_terms jsonb NOT NULL DEFAULT '{}',
  technical_response jsonb NOT NULL DEFAULT '{}', line_items jsonb NOT NULL DEFAULT '[]', attachments jsonb NOT NULL DEFAULT '[]',
  price_score numeric(7,2), quality_score numeric(7,2), delivery_score numeric(7,2), risk_score numeric(7,2), technical_score numeric(7,2), final_score numeric(7,2),
  submitted_by uuid REFERENCES users(id), submitted_at timestamptz, updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(sourcing_id,supplier_id)
);
CREATE TABLE sourcing_evaluations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), response_id uuid NOT NULL REFERENCES sourcing_responses(id) ON DELETE CASCADE,
  evaluator_id uuid NOT NULL REFERENCES users(id), scores jsonb NOT NULL DEFAULT '{}', total_score numeric(7,2), comment text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(response_id,evaluator_id)
);

CREATE TABLE spend_transactions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), transaction_number text NOT NULL UNIQUE, supplier_id uuid NOT NULL REFERENCES suppliers(id),
  organization_id uuid REFERENCES organizations(id), purchase_order_id uuid REFERENCES business_objects(id), contract_id uuid REFERENCES business_objects(id),
  invoice_id uuid REFERENCES business_objects(id), item_code text, item_name text NOT NULL, category text, quantity numeric(20,4), unit text,
  unit_price numeric(20,4), amount numeric(20,2) NOT NULL, currency text NOT NULL DEFAULT 'KRW', transaction_date date NOT NULL,
  contracted boolean NOT NULL DEFAULT false, payment_status text NOT NULL DEFAULT 'pending', metadata jsonb NOT NULL DEFAULT '{}',
  created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX spend_supplier_date_idx ON spend_transactions(supplier_id,transaction_date DESC);
CREATE INDEX spend_org_category_idx ON spend_transactions(organization_id,category,transaction_date DESC);

CREATE TABLE email_verifications (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid REFERENCES users(id) ON DELETE CASCADE, email text NOT NULL,
  token_hash bytea NOT NULL UNIQUE, expires_at timestamptz NOT NULL, verified_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE extracted_contract_clauses (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), contract_id uuid NOT NULL REFERENCES business_objects(id) ON DELETE CASCADE,
  document_id uuid REFERENCES documents(id), extraction jsonb NOT NULL, risk_clauses jsonb NOT NULL DEFAULT '[]',
  model text, status text NOT NULL DEFAULT 'completed', reviewed_by uuid REFERENCES users(id), reviewed_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO screening_templates(name,items,result_rules,required_document_types) VALUES (
 '기본 공급업체 종합심사',
 '[{"code":"eligibility","name":"기본 적격성","weight":10,"required":true},{"code":"finance","name":"재무","weight":15,"required":true},{"code":"credit","name":"신용","weight":10,"required":true},{"code":"quality","name":"품질","weight":15,"required":true},{"code":"technology","name":"기술","weight":10,"required":false},{"code":"security","name":"보안","weight":10,"required":true},{"code":"privacy","name":"개인정보","weight":10,"required":false},{"code":"compliance","name":"준법","weight":10,"required":true},{"code":"esg","name":"ESG","weight":5,"required":false},{"code":"supply_chain","name":"공급망","weight":5,"required":true}]',
 '{"passMin":80,"conditionalMin":70,"reviewMin":60,"requiredFailureResult":"REVIEW_REQUIRED"}',
 '["business_registration","financial_statement","security_pledge"]'
);

INSERT INTO settings(key,value,category) VALUES
 ('sourcing.score_weights','{"price":30,"quality":20,"delivery":15,"risk":15,"technical":20}','procurement'),
 ('supplier.registration','{"requireEmailVerification":true,"similarNameThreshold":0.35,"bankChangeApproval":true}','supplier'),
 ('spend.currency','{"base":"KRW"}','analytics')
ON CONFLICT DO NOTHING;

INSERT INTO workflow_definitions(name,object_type,enabled,conditions,steps) VALUES
 ('공급업체 계좌정보 변경 승인','supplier_bank_change',true,'{}','[{"name":"구매 관리자 승인","role":"procurement_manager"}]');
