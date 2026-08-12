CREATE OR REPLACE FUNCTION vendra_org_in_scope(item_organization uuid, data_scope text, principal_organization uuid)
RETURNS boolean
LANGUAGE sql
STABLE
PARALLEL SAFE
AS $$
  SELECT CASE
    WHEN data_scope = 'company' THEN true
    WHEN item_organization IS NULL OR principal_organization IS NULL THEN false
    WHEN data_scope = 'department' THEN item_organization = principal_organization
    WHEN data_scope = 'division' THEN EXISTS (
      SELECT 1
      FROM organizations item_org, organizations principal_org
      WHERE item_org.id = item_organization
        AND principal_org.id = principal_organization
        AND (item_org.path || item_org.id || '/') LIKE
            (principal_org.path || principal_org.id || '/') || '%'
    )
    ELSE false
  END
$$;

INSERT INTO lifecycle_states(entity_type,code,name,color,sort_order,terminal) VALUES
 ('contract','draft','초안','#3b82f6',10,false),
 ('contract','internal_review','내부 검토','#6366f1',20,false),
 ('contract','legal_review','법무 검토','#f59e0b',30,false),
 ('contract','negotiation','공급업체 협상','#8b5cf6',40,false),
 ('contract','pending_approval','결재','#ec4899',50,false),
 ('contract','executed','체결','#10b981',60,false),
 ('contract','active','수행','#22c55e',70,false),
 ('contract','renewal_review','갱신 검토','#14b8a6',80,false),
 ('contract','renewed','갱신','#06b6d4',90,false),
 ('contract','ended','종료','#64748b',100,true),
 ('purchase_request','draft','초안','#3b82f6',10,false),
 ('purchase_request','pending_approval','부서 승인','#f59e0b',20,false),
 ('purchase_request','procurement_review','구매 검토','#8b5cf6',30,false),
 ('purchase_request','approved','승인','#10b981',40,true),
 ('rfq','draft','초안','#3b82f6',10,false),
 ('rfq','open','진행','#10b981',20,false),
 ('rfq','preferred_negotiation','우선협상','#f59e0b',30,false),
 ('rfq','selected','선정','#22c55e',40,true),
 ('rfp','draft','초안','#3b82f6',10,false),
 ('rfp','open','진행','#10b981',20,false),
 ('rfp','preferred_negotiation','우선협상','#f59e0b',30,false),
 ('rfp','selected','선정','#22c55e',40,true)
ON CONFLICT(entity_type,code) DO NOTHING;
