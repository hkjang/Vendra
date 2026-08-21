package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type supplier struct {
	ID              string           `json:"id"`
	Number          string           `json:"supplierNumber"`
	Name            string           `json:"name"`
	LegalName       *string          `json:"legalName,omitempty"`
	BusinessNumber  string           `json:"businessNumber"`
	CorporateNumber *string          `json:"corporateNumber,omitempty"`
	Representative  *string          `json:"representative,omitempty"`
	Status          string           `json:"status"`
	Grade           *string          `json:"grade,omitempty"`
	RiskLevel       string           `json:"riskLevel"`
	SupplierType    *string          `json:"supplierType,omitempty"`
	Industry        *string          `json:"industry,omitempty"`
	Categories      []string         `json:"categories"`
	Addresses       []map[string]any `json:"addresses"`
	Phone           *string          `json:"phone,omitempty"`
	Email           *string          `json:"email,omitempty"`
	Website         *string          `json:"website,omitempty"`
	Financials      map[string]any   `json:"financials"`
	BankAccount     *string          `json:"bankAccount,omitempty"`
	TaxInfo         map[string]any   `json:"taxInfo"`
	ERPVendorID     *string          `json:"erpVendorId,omitempty"`
	OwnerID         *string          `json:"ownerId,omitempty"`
	OrganizationID  *string          `json:"organizationId,omitempty"`
	TradingSince    *string          `json:"tradingSince,omitempty"`
	AnnualSpend     float64          `json:"annualSpend"`
	Score           *float64         `json:"score,omitempty"`
	Metadata        map[string]any   `json:"metadata"`
	CreatedAt       string           `json:"createdAt"`
	UpdatedAt       string           `json:"updatedAt"`
}

const supplierSelect = `SELECT id,supplier_number,name,legal_name,business_number,corporate_number,representative,status,grade,risk_level,supplier_type,industry,categories,addresses,phone,email,website,financials,tax_info,erp_vendor_id,owner_id,organization_id,to_char(trading_since,'YYYY-MM-DD'),annual_spend,score,metadata,to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),to_char(updated_at,'YYYY-MM-DD"T"HH24:MI:SSOF') FROM suppliers`

func scanSupplier(row pgx.Row) (supplier, error) {
	var s supplier
	var cats, addresses, financials, tax, metadata []byte
	err := row.Scan(&s.ID, &s.Number, &s.Name, &s.LegalName, &s.BusinessNumber, &s.CorporateNumber, &s.Representative, &s.Status, &s.Grade, &s.RiskLevel, &s.SupplierType, &s.Industry, &cats, &addresses, &s.Phone, &s.Email, &s.Website, &financials, &tax, &s.ERPVendorID, &s.OwnerID, &s.OrganizationID, &s.TradingSince, &s.AnnualSpend, &s.Score, &metadata, &s.CreatedAt, &s.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal(cats, &s.Categories)
		_ = json.Unmarshal(addresses, &s.Addresses)
		_ = json.Unmarshal(financials, &s.Financials)
		_ = json.Unmarshal(tax, &s.TaxInfo)
		_ = json.Unmarshal(metadata, &s.Metadata)
	}
	return s, err
}

// auditableSupplierInput strips the fields that are deliberately encrypted at
// rest before the request is written to the audit trail. The account number is
// stored as AES-256-GCM ciphertext, so copying the submitted plaintext into
// audit_logs.new_value would hand it to every holder of audit.read and undo the
// encryption. The audit still records that the value changed.
func auditableSupplierInput(in map[string]any) map[string]any {
	if _, ok := in["bankAccount"]; !ok {
		return in
	}
	redacted := make(map[string]any, len(in))
	for key, value := range in {
		redacted[key] = value
	}
	changed := false
	if account, ok := in["bankAccount"].(string); ok {
		changed = strings.TrimSpace(account) != ""
	}
	delete(redacted, "bankAccount")
	redacted["bankAccountChanged"] = changed
	return redacted
}

func (a *App) listSuppliers(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	risk := strings.TrimSpace(r.URL.Query().Get("risk"))
	limit := parseLimit(r, 100)
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), supplierSelect+` WHERE deleted_at IS NULL AND ($1='' OR status=$1) AND ($2='' OR risk_level=$2) AND ($3='' OR name ILIKE '%'||$3||'%' OR business_number ILIKE '%'||$3||'%' OR supplier_number ILIKE '%'||$3||'%') AND (vendra_org_in_scope(organization_id,$5,NULLIF($6,'')::uuid) OR ($5='own' AND owner_id=$7::uuid)) ORDER BY updated_at DESC LIMIT $4`, status, risk, q, limit+1, p.DataScope, organizationID, p.ID)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "공급업체를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []supplier{}
	for rows.Next() {
		s, e := scanSupplier(rows)
		if e == nil {
			items = append(items, redactSupplier(p, s))
		}
	}
	items, truncated := truncate(items, limit)
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items), "limit": limit, "truncated": truncated})
}

func (a *App) createSupplier(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	name := stringValue(in, "name")
	businessNumber := stringValue(in, "businessNumber")
	if name == "" || businessNumber == "" {
		writeError(w, 400, "validation_error", "업체명과 사업자번호는 필수입니다")
		return
	}
	similarNameThreshold := 0.35
	var registrationSettings []byte
	if a.db.QueryRow(r.Context(), `SELECT value FROM settings WHERE key='supplier.registration'`).Scan(&registrationSettings) == nil {
		var registration struct {
			SimilarNameThreshold float64 `json:"similarNameThreshold"`
		}
		if json.Unmarshal(registrationSettings, &registration) == nil && registration.SimilarNameThreshold > 0 && registration.SimilarNameThreshold <= 1 {
			similarNameThreshold = registration.SimilarNameThreshold
		}
	}
	var similarID, similarName string
	var similarity float64
	if a.db.QueryRow(r.Context(), `SELECT id,name,similarity(lower(name),lower($1)) FROM suppliers WHERE deleted_at IS NULL AND similarity(lower(name),lower($1))>=$2 ORDER BY similarity(lower(name),lower($1)) DESC LIMIT 1`, name, similarNameThreshold).Scan(&similarID, &similarName, &similarity) == nil {
		writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "similar_supplier", "message": "유사한 공급업체가 이미 등록되어 있습니다"}, "similarSupplier": map[string]any{"id": similarID, "name": similarName, "similarity": similarity}})
		return
	}
	if stringValue(in, "organizationId") == "" && p.OrganizationID != nil {
		in["organizationId"] = *p.OrganizationID
	}
	number := stringValue(in, "supplierNumber")
	if number == "" {
		number = "SUP-" + strings.ToUpper(timeNowID())
	}
	cats, _ := in["categories"].([]any)
	addresses, _ := in["addresses"].([]any)
	financials, _ := in["financials"].(map[string]any)
	tax, _ := in["taxInfo"].(map[string]any)
	metadata, _ := in["metadata"].(map[string]any)
	var bankCipher any
	if bank := stringValue(in, "bankAccount"); bank != "" {
		c, e := a.vault.Encrypt(bank)
		if e != nil {
			writeError(w, 500, "encryption_error", "계좌정보를 암호화하지 못했습니다")
			return
		}
		bankCipher = c
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO suppliers(supplier_number,name,legal_name,business_number,corporate_number,representative,status,grade,risk_level,supplier_type,industry,categories,addresses,phone,email,website,financials,bank_account_encrypted,tax_info,erp_vendor_id,owner_id,organization_id,trading_since,annual_spend,metadata,created_by)
	 VALUES($1,$2,NULLIF($3,''),$4,NULLIF($5,''),NULLIF($6,''),COALESCE(NULLIF($7,''),'candidate'),NULLIF($8,''),COALESCE(NULLIF($9,''),'LOW'),NULLIF($10,''),NULLIF($11,''),$12,$13,NULLIF($14,''),NULLIF($15,''),NULLIF($16,''),$17,$18,$19,NULLIF($20,''),COALESCE(NULLIF($21,''),$26)::uuid,NULLIF($22,'')::uuid,NULLIF($23,'')::date,COALESCE($24,0),$25,$26::uuid) RETURNING id`, number, name, stringValue(in, "legalName"), businessNumber, stringValue(in, "corporateNumber"), stringValue(in, "representative"), stringValue(in, "status"), stringValue(in, "grade"), stringValue(in, "riskLevel"), stringValue(in, "supplierType"), stringValue(in, "industry"), raw(cats), raw(addresses), stringValue(in, "phone"), stringValue(in, "email"), stringValue(in, "website"), raw(financials), bankCipher, raw(tax), stringValue(in, "erpVendorId"), stringValue(in, "ownerId"), stringValue(in, "organizationId"), stringValue(in, "tradingSince"), numberValue(in, "annualSpend"), raw(metadata), p.ID).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "business_number") {
			writeError(w, 409, "duplicate_business_number", "이미 등록된 사업자번호입니다")
			return
		}
		logDB(err)
		writeError(w, 400, "save_failed", "공급업체를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "supplier", id, nil, auditableSupplierInput(in))
	s, _ := scanSupplier(a.db.QueryRow(r.Context(), supplierSelect+` WHERE id=$1`, id))
	writeJSON(w, 201, redactSupplier(p, s))
}

func (a *App) getSupplier(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s, err := scanSupplier(a.db.QueryRow(r.Context(), supplierSelect+` WHERE id=$1 AND deleted_at IS NULL`, id))
	if err == pgx.ErrNoRows {
		writeError(w, 404, "not_found", "공급업체를 찾을 수 없습니다")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "공급업체를 조회하지 못했습니다")
		return
	}
	p, _ := principalFrom(r.Context())
	if !a.canAccessSupplier(r.Context(), p, s) && !grantAuthorized(r.Context()) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	if hasPermission(p, "supplier.bank_account.read") || hasPermission(p, "*") {
		var cipher *string
		if a.db.QueryRow(r.Context(), `SELECT bank_account_encrypted FROM suppliers WHERE id=$1`, id).Scan(&cipher) == nil && cipher != nil {
			if value, decryptErr := a.vault.Decrypt(*cipher); decryptErr == nil {
				s.BankAccount = &value
				a.audit.record(r, "read_sensitive_field", "supplier", id, nil, map[string]any{"field": "bankAccount"})
			}
		}
	}
	var activeContracts, openIssues int
	var delivery, quality float64
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE object_type='contract' AND status IN('active','approved')),count(*) FILTER(WHERE object_type='issue' AND status NOT IN('closed','resolved')),COALESCE(100.0*count(*) FILTER(WHERE object_type='delivery' AND status IN('completed','accepted','closed') AND (due_date IS NULL OR CASE WHEN COALESCE(data->>'deliveredAt','') ~ '^\d{4}-\d{2}-\d{2}' THEN left(data->>'deliveredAt',10)::date ELSE updated_at::date END<=due_date))/NULLIF(count(*) FILTER(WHERE object_type='delivery' AND status IN('completed','accepted','closed')),0),0),COALESCE(avg(score) FILTER(WHERE object_type IN('quality','inspection')),0) FROM business_objects WHERE supplier_id=$1 AND deleted_at IS NULL`, id).Scan(&activeContracts, &openIssues, &delivery, &quality)
	writeJSON(w, 200, map[string]any{"supplier": redactSupplier(p, s), "metrics": map[string]any{"activeContracts": activeContracts, "openIssues": openIssues, "deliveryPerformance": delivery, "qualityPerformance": quality}})
}

func redactSupplier(p Principal, s supplier) supplier {
	if !hasPermission(p, "spend.read") && !hasPermission(p, "analytics.read") && !hasPermission(p, "supplier.financial.read") {
		s.AnnualSpend = 0
		s.Financials = map[string]any{}
	}
	if !hasPermission(p, "supplier.tax.read") {
		s.TaxInfo = map[string]any{}
	}
	return s
}

func canAccessSupplier(p Principal, s supplier) bool {
	if p.DataScope == "company" {
		return true
	}
	if p.DataScope == "department" || p.DataScope == "division" {
		return p.OrganizationID != nil && s.OrganizationID != nil && *p.OrganizationID == *s.OrganizationID
	}
	return s.OwnerID != nil && *s.OwnerID == p.ID
}

func (a *App) canAccessSupplier(ctx context.Context, p Principal, s supplier) bool {
	if p.DataScope != "division" {
		return canAccessSupplier(p, s)
	}
	if p.OrganizationID == nil || s.OrganizationID == nil {
		return false
	}
	var allowed bool
	_ = a.db.QueryRow(ctx, `SELECT vendra_org_in_scope($1,'division',$2)`, *s.OrganizationID, *p.OrganizationID).Scan(&allowed)
	return allowed
}

func (a *App) supplierScopeAllowed(r *http.Request, id string) bool {
	s, err := scanSupplier(a.db.QueryRow(r.Context(), supplierSelect+` WHERE id=$1 AND deleted_at IS NULL`, id))
	if err != nil {
		return false
	}
	p, _ := principalFrom(r.Context())
	return a.canAccessSupplier(r.Context(), p, s) || grantAuthorized(r.Context())
}

func (a *App) updateSupplier(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	before, err := scanSupplier(a.db.QueryRow(r.Context(), supplierSelect+` WHERE id=$1 AND deleted_at IS NULL`, id))
	if err != nil {
		writeError(w, 404, "not_found", "공급업체를 찾을 수 없습니다")
		return
	}
	p, _ := principalFrom(r.Context())
	if !a.canAccessSupplier(r.Context(), p, before) && !grantAuthorized(r.Context()) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	metadata := before.Metadata
	if v, ok := in["metadata"].(map[string]any); ok {
		metadata = v
	}
	financials := before.Financials
	if v, ok := in["financials"].(map[string]any); ok {
		financials = v
	}
	taxInfo := before.TaxInfo
	if v, ok := in["taxInfo"].(map[string]any); ok {
		taxInfo = v
	}
	cats := before.Categories
	if v, ok := in["categories"].([]any); ok {
		cats = make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				cats = append(cats, s)
			}
		}
	}
	addresses := before.Addresses
	if v, ok := in["addresses"].([]any); ok {
		addresses = []map[string]any{}
		for _, x := range v {
			if m, ok := x.(map[string]any); ok {
				addresses = append(addresses, m)
			}
		}
	}
	var bankCipher any
	bankChangePending := false
	if bank := stringValue(in, "bankAccount"); bank != "" {
		c, e := a.vault.Encrypt(bank)
		if e != nil {
			writeError(w, 500, "encryption_error", "계좌정보를 암호화하지 못했습니다")
			return
		}
		bankCipher = c
		var workflowEnabled bool
		_ = a.db.QueryRow(r.Context(), `SELECT COALESCE((value #>> '{}')::boolean,false) FROM settings WHERE key='workflow.approval_enabled'`).Scan(&workflowEnabled)
		var bankApproval bool
		_ = a.db.QueryRow(r.Context(), `SELECT COALESCE((value->>'bankChangeApproval')::boolean,true) FROM settings WHERE key='supplier.registration'`).Scan(&bankApproval)
		bankChangePending = workflowEnabled && bankApproval
	}
	if bankChangePending {
		var changeID, definitionID string
		var steps []byte
		err = a.db.QueryRow(r.Context(), `SELECT id,steps FROM workflow_definitions WHERE object_type='supplier_bank_change' AND enabled=true ORDER BY version DESC LIMIT 1`).Scan(&definitionID, &steps)
		if err != nil {
			writeError(w, 409, "bank_workflow_missing", "계좌 변경 승인 Workflow가 필요합니다")
			return
		}
		err = a.db.QueryRow(r.Context(), `INSERT INTO business_objects(object_type,number,supplier_id,title,status,owner_id,organization_id,data,created_by) VALUES('supplier_bank_change',$1,$2,'계좌정보 변경','pending_approval',$3,$4,$5,$3) RETURNING id`, `BANK-`+strings.ToUpper(timeNowID()), id, p.ID, before.OrganizationID, raw(map[string]any{"supplierId": id, "bankAccountCipher": bankCipher})).Scan(&changeID)
		if err == nil {
			_, err = a.db.Exec(r.Context(), `INSERT INTO workflow_instances(definition_id,object_type,object_id,requested_by,context) VALUES($1,'supplier_bank_change',$2,$3,$4)`, definitionID, changeID, p.ID, raw(map[string]any{"steps": json.RawMessage(steps)}))
		}
		if err != nil {
			writeError(w, 500, "workflow_failed", "계좌 변경 승인을 시작하지 못했습니다")
			return
		}
		bankCipher = nil
	}
	_, err = a.db.Exec(r.Context(), `UPDATE suppliers SET name=COALESCE(NULLIF($2,''),name),legal_name=COALESCE(NULLIF($3,''),legal_name),representative=COALESCE(NULLIF($4,''),representative),status=COALESCE(NULLIF($5,''),status),grade=COALESCE(NULLIF($6,''),grade),risk_level=COALESCE(NULLIF($7,''),risk_level),supplier_type=COALESCE(NULLIF($8,''),supplier_type),industry=COALESCE(NULLIF($9,''),industry),categories=$10,addresses=$11,phone=COALESCE(NULLIF($12,''),phone),email=COALESCE(NULLIF($13,''),email),website=COALESCE(NULLIF($14,''),website),bank_account_encrypted=COALESCE($15,bank_account_encrypted),metadata=$16,financials=$17,tax_info=$18,erp_vendor_id=COALESCE(NULLIF($19,''),erp_vendor_id),updated_at=now() WHERE id=$1`, id, stringValue(in, "name"), stringValue(in, "legalName"), stringValue(in, "representative"), stringValue(in, "status"), stringValue(in, "grade"), stringValue(in, "riskLevel"), stringValue(in, "supplierType"), stringValue(in, "industry"), raw(cats), raw(addresses), stringValue(in, "phone"), stringValue(in, "email"), stringValue(in, "website"), bankCipher, raw(metadata), raw(financials), raw(taxInfo), stringValue(in, "erpVendorId"))
	if err != nil {
		logDB(err)
		writeError(w, 400, "save_failed", "공급업체를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "supplier", id, before, auditableSupplierInput(in))
	s, _ := scanSupplier(a.db.QueryRow(r.Context(), supplierSelect+` WHERE id=$1`, id))
	writeJSON(w, 200, map[string]any{"supplier": redactSupplier(p, s), "bankChangePending": bankChangePending})
}

func (a *App) supplierObjects(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.supplierScopeAllowed(r, id) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), objectSelect+` WHERE o.supplier_id=$1 AND o.deleted_at IS NULL ORDER BY o.updated_at DESC`, id)
	if err != nil {
		writeError(w, 500, "database_error", "연관 데이터를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []businessObject{}
	for rows.Next() {
		o, e := scanObject(rows)
		if e == nil {
			items = append(items, o)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) supplierActivity(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('occurredAt',occurred_at,'actor',actor_email,'action',action,'objectType',object_type,'objectId',object_id,'value',new_value,'requestId',request_id) FROM audit_logs WHERE (object_type='supplier' AND object_id=$1) OR metadata->>'supplierId'=$1 OR new_value->>'supplierId'=$1 ORDER BY occurred_at DESC LIMIT 200`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "활동 이력을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var encoded []byte
		if rows.Scan(&encoded) == nil {
			var item map[string]any
			if json.Unmarshal(encoded, &item) == nil {
				items = append(items, item)
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) listContacts(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,title,department,email,phone,primary_contact,CASE WHEN email IS NULL THEN false ELSE EXISTS(SELECT 1 FROM email_verifications e WHERE lower(e.email)=lower(supplier_contacts.email) AND e.verified_at IS NOT NULL) END,created_at FROM supplier_contacts WHERE supplier_id=$1 ORDER BY primary_contact DESC,name`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "담당자를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name string
		var title, dept, email, phone *string
		var primary, emailVerified bool
		var created any
		if rows.Scan(&id, &name, &title, &dept, &email, &phone, &primary, &emailVerified, &created) == nil {
			items = append(items, map[string]any{"id": id, "name": name, "title": title, "department": dept, "email": email, "phone": phone, "primary": primary, "emailVerified": emailVerified, "createdAt": created})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createContact(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	name := stringValue(in, "name")
	if name == "" {
		writeError(w, 400, "validation_error", "담당자 이름은 필수입니다")
		return
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO supplier_contacts(supplier_id,name,title,department,email,phone,primary_contact) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7) RETURNING id`, r.PathValue("id"), name, stringValue(in, "title"), stringValue(in, "department"), stringValue(in, "email"), stringValue(in, "phone"), in["primary"]).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "담당자를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "supplier_contact", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func supplierNumberConflict(err error) bool {
	return err != nil && strings.Contains(fmt.Sprint(err), "business_number")
}
