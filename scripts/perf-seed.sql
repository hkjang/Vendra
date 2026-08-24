\set ON_ERROR_STOP on
-- Seeds a database with enough data for the latency harness to mean something.
-- Apply the migrations first (booting the application does it), then:
--
--   docker exec -i <postgres> psql -U postgres -d vendra -q < scripts/perf-seed.sql
--   VENDRA_PERF=1 VENDRA_TEST_DSN=<dsn> go test ./internal/httpapi/ \
--     -run TestMeasureEndpointLatency -v
--
-- Roughly 270 MB: 10,000 suppliers, 100,000 business objects, 200,000 spend
-- transactions, 40,000 documents, 30,000 risks and 400,000 audit entries.
-- Organisations: a company with divisions and departments.
INSERT INTO organizations(id,name,path)
SELECT gen_random_uuid(), '부서'||n, '/' FROM generate_series(1,40) n;

-- Suppliers
INSERT INTO suppliers(supplier_number,business_number,name,status,risk_level,grade,score,annual_spend,organization_id,categories)
SELECT 'SUP-'||lpad(n::text,6,'0'), lpad(n::text,3,'0')||'-00-'||lpad(n::text,5,'0'),
       '성능 공급사 '||n,
       (ARRAY['active','approved','screening','suspended'])[1+(n%4)],
       (ARRAY['LOW','MEDIUM','HIGH','CRITICAL'])[1+(n%4)],
       (ARRAY['A','B','C','D'])[1+(n%4)],
       (n%100)::numeric, (n%9000)*1000,
       (SELECT id FROM organizations ORDER BY id LIMIT 1 OFFSET (n%40)),
       '["자재","용역"]'::jsonb
FROM generate_series(1,10000) n;

-- Business objects across every type
INSERT INTO business_objects(object_type,number,supplier_id,organization_id,title,status,amount,due_date,end_date,data)
SELECT (ARRAY['contract','purchase_order','delivery','invoice','issue','rfq','quality','inspection','payment','purchase_request'])[1+(n%10)],
       'OBJ-'||lpad(n::text,7,'0'),
       s.id, s.organization_id,
       '성능 업무 '||n,
       (ARRAY['draft','pending_approval','approved','active','closed'])[1+(n%5)],
       (n%50000)*10,
       current_date + ((n%400)-200),
       current_date + ((n%700)-100),
       '{}'::jsonb
FROM generate_series(1,100000) n
JOIN LATERAL (SELECT id, organization_id FROM suppliers OFFSET (n%10000) LIMIT 1) s ON true;

-- Spend
INSERT INTO spend_transactions(transaction_number,supplier_id,item_name,amount,transaction_date,category,contracted,organization_id)
SELECT 'TX-'||lpad(n::text,7,'0'), s.id, '품목'||(n%50), (n%20000)*5,
       current_date - (n%1000), (ARRAY['자재','용역','설비','IT','물류'])[1+(n%5)], (n%3)=0, s.organization_id
FROM generate_series(1,200000) n
JOIN LATERAL (SELECT id, organization_id FROM suppliers OFFSET (n%10000) LIMIT 1) s ON true;

-- Documents
INSERT INTO documents(supplier_id,document_type,name,version,storage_path,content_type,size,checksum,status,expires_at)
SELECT s.id, (ARRAY['contract','certificate','quotation','other'])[1+(n%4)], '문서'||n||'.pdf', 1,
       '/var/lib/vendra/documents/x'||n, 'application/pdf', 1024, md5(n::text), 'active',
       current_date + ((n%500)-100)
FROM generate_series(1,40000) n
JOIN LATERAL (SELECT id FROM suppliers OFFSET (n%10000) LIMIT 1) s ON true;

-- Risks and evaluations
INSERT INTO risks(supplier_id,risk_type,probability,impact,severity,status,description)
SELECT s.id, (ARRAY['재무','운영','보안','준법','품질'])[1+(n%5)], 1+(n%5), 1+(n%5),
       (ARRAY['LOW','MEDIUM','HIGH','CRITICAL'])[1+(n%4)], (ARRAY['open','mitigating','closed'])[1+(n%3)], '설명'||n
FROM generate_series(1,30000) n
JOIN LATERAL (SELECT id FROM suppliers OFFSET (n%10000) LIMIT 1) s ON true;

INSERT INTO evaluations(supplier_id,evaluation_type,status,total_score,grade)
SELECT s.id, '정기', (ARRAY['draft','completed'])[1+(n%2)], (n%100), (ARRAY['A','B','C','D'])[1+(n%4)]
FROM generate_series(1,30000) n
JOIN LATERAL (SELECT id FROM suppliers OFFSET (n%10000) LIMIT 1) s ON true;

INSERT INTO supplier_contacts(supplier_id,name,email,title)
SELECT s.id, '담당자'||n, 'c'||n||'@example.com', '과장'
FROM generate_series(1,30000) n
JOIN LATERAL (SELECT id FROM suppliers OFFSET (n%10000) LIMIT 1) s ON true;

-- Audit log: the table that only grows
INSERT INTO audit_logs(actor_email,action,object_type,object_id,previous_value,new_value,request_id,occurred_at)
SELECT 'user'||(n%200)||'@example.com', (ARRAY['create','update','read_sensitive_field','login','submit'])[1+(n%5)],
       (ARRAY['supplier','contract','document','user'])[1+(n%4)], gen_random_uuid(), 'null'::jsonb, '{}'::jsonb,
       'seed'||n, now() - ((n%200000)||' minutes')::interval
FROM generate_series(1,400000) n;

ANALYZE;
