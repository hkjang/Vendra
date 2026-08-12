CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE organizations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  parent_id uuid REFERENCES organizations(id),
  path text NOT NULL DEFAULT '/',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email text NOT NULL UNIQUE,
  display_name text NOT NULL,
  password_hash text,
  user_type text NOT NULL DEFAULT 'internal' CHECK (user_type IN ('internal','supplier','api')),
  organization_id uuid REFERENCES organizations(id),
  supplier_id uuid,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','invited')),
  is_bootstrap_admin boolean NOT NULL DEFAULT false,
  oidc_subject text UNIQUE,
  locale text NOT NULL DEFAULT 'ko',
  timezone text NOT NULL DEFAULT 'Asia/Seoul',
  last_login_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE roles (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE,
  name text NOT NULL,
  permissions jsonb NOT NULL DEFAULT '[]',
  data_scope text NOT NULL DEFAULT 'own',
  system boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE user_roles (user_id uuid REFERENCES users(id) ON DELETE CASCADE, role_id uuid REFERENCES roles(id) ON DELETE CASCADE, PRIMARY KEY(user_id, role_id));
CREATE TABLE access_grants (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  permission text NOT NULL, resource_type text, resource_id uuid, conditions jsonb NOT NULL DEFAULT '{}',
  valid_from timestamptz NOT NULL DEFAULT now(), valid_until timestamptz, delegated_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE, ip inet, user_agent text, expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(), last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE settings (
  key text PRIMARY KEY, value jsonb NOT NULL, secret_value text, secret boolean NOT NULL DEFAULT false,
  category text NOT NULL DEFAULT 'general', updated_by uuid REFERENCES users(id), updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE suppliers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), supplier_number text NOT NULL UNIQUE,
  name text NOT NULL, legal_name text, business_number text NOT NULL UNIQUE, corporate_number text,
  representative text, status text NOT NULL DEFAULT 'candidate', grade text, risk_level text NOT NULL DEFAULT 'LOW',
  supplier_type text, industry text, categories jsonb NOT NULL DEFAULT '[]', addresses jsonb NOT NULL DEFAULT '[]',
  phone text, email text, website text, financials jsonb NOT NULL DEFAULT '{}', bank_account_encrypted text,
  tax_info jsonb NOT NULL DEFAULT '{}', erp_vendor_id text, owner_id uuid REFERENCES users(id), organization_id uuid REFERENCES organizations(id),
  trading_since date, annual_spend numeric(20,2) NOT NULL DEFAULT 0, score numeric(7,2), metadata jsonb NOT NULL DEFAULT '{}',
  created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
ALTER TABLE users ADD CONSTRAINT users_supplier_fk FOREIGN KEY (supplier_id) REFERENCES suppliers(id);
CREATE INDEX suppliers_search_idx ON suppliers USING gin (to_tsvector('simple', coalesce(name,'') || ' ' || coalesce(legal_name,'') || ' ' || coalesce(business_number,'')));

CREATE TABLE supplier_contacts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
  name text NOT NULL, title text, department text, email text, phone text, primary_contact boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE lifecycle_states (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), entity_type text NOT NULL, code text NOT NULL, name text NOT NULL,
  color text NOT NULL DEFAULT '#64748b', sort_order integer NOT NULL DEFAULT 0, terminal boolean NOT NULL DEFAULT false, enabled boolean NOT NULL DEFAULT true,
  UNIQUE(entity_type, code)
);

CREATE TABLE business_objects (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), object_type text NOT NULL, number text NOT NULL,
  supplier_id uuid REFERENCES suppliers(id), parent_id uuid, title text NOT NULL, status text NOT NULL DEFAULT 'draft',
  amount numeric(20,2), currency text NOT NULL DEFAULT 'KRW', owner_id uuid REFERENCES users(id), organization_id uuid REFERENCES organizations(id),
  start_date date, due_date date, end_date date, risk_level text, score numeric(7,2), data jsonb NOT NULL DEFAULT '{}',
  created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
  UNIQUE(object_type, number)
);
CREATE INDEX business_objects_type_idx ON business_objects(object_type, status, updated_at DESC);
CREATE INDEX business_objects_supplier_idx ON business_objects(supplier_id, object_type);
CREATE INDEX business_objects_search_idx ON business_objects USING gin (to_tsvector('simple', coalesce(number,'') || ' ' || coalesce(title,'')));

CREATE TABLE scorecard_templates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, evaluation_type text NOT NULL, active boolean NOT NULL DEFAULT true,
  criteria jsonb NOT NULL, grade_rules jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE evaluations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), supplier_id uuid NOT NULL REFERENCES suppliers(id), template_id uuid REFERENCES scorecard_templates(id),
  evaluation_type text NOT NULL, status text NOT NULL DEFAULT 'draft', period_start date, period_end date, scores jsonb NOT NULL DEFAULT '{}',
  total_score numeric(7,2), grade text, evaluator_id uuid REFERENCES users(id), comments text,
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE risks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), supplier_id uuid NOT NULL REFERENCES suppliers(id), risk_type text NOT NULL,
  probability numeric(5,2) NOT NULL DEFAULT 0, impact numeric(5,2) NOT NULL DEFAULT 0, score numeric(7,2) GENERATED ALWAYS AS (probability * impact) STORED,
  severity text NOT NULL, status text NOT NULL DEFAULT 'open', description text, mitigation text, owner_id uuid REFERENCES users(id),
  review_date date, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workflow_definitions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, object_type text NOT NULL, enabled boolean NOT NULL DEFAULT true,
  conditions jsonb NOT NULL DEFAULT '{}', steps jsonb NOT NULL, version integer NOT NULL DEFAULT 1,
  created_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE workflow_instances (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), definition_id uuid REFERENCES workflow_definitions(id), object_type text NOT NULL, object_id uuid NOT NULL,
  status text NOT NULL DEFAULT 'pending', current_step integer NOT NULL DEFAULT 0, context jsonb NOT NULL DEFAULT '{}',
  requested_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz
);
CREATE TABLE workflow_actions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), instance_id uuid NOT NULL REFERENCES workflow_instances(id) ON DELETE CASCADE,
  step integer NOT NULL, action text NOT NULL CHECK (action IN ('request','approve','reject','return','cancel')),
  actor_id uuid REFERENCES users(id), comment text, created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE documents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), supplier_id uuid REFERENCES suppliers(id), object_type text, object_id uuid,
  document_type text NOT NULL, name text NOT NULL, version integer NOT NULL DEFAULT 1, storage_path text NOT NULL,
  content_type text, size bigint NOT NULL, checksum text NOT NULL, expires_at date, status text NOT NULL DEFAULT 'active',
  uploaded_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name text NOT NULL, prefix text NOT NULL, key_hash bytea NOT NULL UNIQUE, scopes jsonb NOT NULL DEFAULT '[]',
  expires_at timestamptz, last_used_at timestamptz, revoked_at timestamptz, rotated_from uuid REFERENCES api_keys(id), created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invitations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(), email text NOT NULL, supplier_id uuid REFERENCES suppliers(id), token_hash bytea NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL, accepted_at timestamptz, invited_by uuid REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_logs (
  id bigserial PRIMARY KEY, occurred_at timestamptz NOT NULL DEFAULT now(), actor_id uuid REFERENCES users(id), actor_email text,
  action text NOT NULL, object_type text NOT NULL, object_id text, previous_value jsonb, new_value jsonb,
  ip inet, session_id uuid, request_id text NOT NULL, metadata jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_object_idx ON audit_logs(object_type, object_id, occurred_at DESC);

CREATE TABLE jobs (
  id bigserial PRIMARY KEY, kind text NOT NULL, payload jsonb NOT NULL, run_at timestamptz NOT NULL DEFAULT now(),
  status text NOT NULL DEFAULT 'pending', attempts integer NOT NULL DEFAULT 0, locked_at timestamptz, last_error text, created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO roles(code,name,permissions,data_scope,system) VALUES
 ('system_admin','시스템 관리자','["*"]','company',true),
 ('procurement_manager','구매 관리자','["supplier.*","procurement.*","evaluation.*","workflow.read"]','company',true),
 ('contract_manager','계약 담당자','["supplier.read","contract.*"]','company',true),
 ('business_user','현업 담당자','["supplier.read","purchase_request.create","purchase_request.read","inspection.*","evaluation.create"]','department',true),
 ('finance','재무 담당자','["supplier.read","spend.read","payment.*"]','company',true),
 ('security','보안 담당자','["supplier.read","risk.security.*","evaluation.security.*"]','company',true),
 ('legal','준법·법무 담당자','["supplier.read","contract.review","risk.compliance.*"]','company',true),
 ('auditor','감사 담당자','["audit.read","*.read"]','company',true),
 ('executive','경영진','["dashboard.read","analytics.read","supplier.read"]','company',true),
 ('supplier_user','공급업체 사용자','["portal.*"]','own',true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO lifecycle_states(entity_type,code,name,color,sort_order,terminal) VALUES
 ('supplier','candidate','후보','#818cf8',10,false), ('supplier','registration','등록','#3b82f6',20,false),
 ('supplier','screening','심사','#f59e0b',30,false), ('supplier','approved','승인','#10b981',40,false),
 ('supplier','active','거래 가능','#22c55e',50,false), ('supplier','improvement','개선 대상','#f97316',60,false),
 ('supplier','suspended','거래 중단','#ef4444',70,true)
ON CONFLICT DO NOTHING;

INSERT INTO scorecard_templates(name,evaluation_type,criteria,grade_rules) VALUES
 ('기본 공급업체 평가','periodic','[{"code":"price","name":"가격 경쟁력","weight":20},{"code":"quality","name":"품질","weight":25},{"code":"delivery","name":"납기","weight":20},{"code":"service","name":"서비스","weight":10},{"code":"technology","name":"기술력","weight":10},{"code":"security","name":"보안","weight":5},{"code":"finance","name":"재무 안정성","weight":5},{"code":"esg","name":"ESG","weight":5}]','[{"min":90,"grade":"S"},{"min":80,"grade":"A"},{"min":70,"grade":"B"},{"min":60,"grade":"C"},{"min":0,"grade":"D"}]')
ON CONFLICT DO NOTHING;

INSERT INTO settings(key,value,category) VALUES
 ('branding','{"serviceName":"Vendra","loginMessage":"Enterprise Supplier Intelligence Platform"}','general'),
 ('workflow.approval_enabled','false','workflow'),
 ('security.session','{"ttlHours":12,"secureCookie":false}','security'),
 ('notification.adapters','[]','notification'),
 ('oidc','{"enabled":false,"issuer":"","clientId":"","scopes":["openid","profile","email"],"autoCreate":true,"defaultRole":"business_user"}','identity'),
 ('ai','{"enabled":false,"baseUrl":"","model":"","timeoutSeconds":60}','ai'),
 ('storage','{"driver":"filesystem","path":"/var/lib/vendra/documents"}','document')
ON CONFLICT DO NOTHING;
