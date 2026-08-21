package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	showAmounts := hasPermission(p, "spend.read") || hasPermission(p, "analytics.read") || hasPermission(p, "*")
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	var total, active, newCount, highRisk, expiring, openIssues, activeRFQ, activeRFP, overdueDeliveries, pendingApprovals, pendingScreenings int
	var spend, avgScore, contractValue, deliveryCompliance, defectRate float64
	err := a.db.QueryRow(r.Context(), `SELECT count(*),count(*) FILTER(WHERE status='active'),count(*) FILTER(WHERE created_at>=date_trunc('month',now())),count(*) FILTER(WHERE risk_level IN('HIGH','CRITICAL')),COALESCE(sum(annual_spend),0),COALESCE(avg(score),0) FROM suppliers WHERE deleted_at IS NULL AND (vendra_org_in_scope(organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&total, &active, &newCount, &highRisk, &spend, &avgScore)
	if err != nil {
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	if !showAmounts {
		spend = 0
	}
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE object_type='contract' AND end_date BETWEEN current_date AND current_date+180),count(*) FILTER(WHERE object_type='issue' AND status NOT IN('closed','resolved')),COALESCE(sum(amount) FILTER(WHERE object_type='contract' AND status IN('active','approved')),0),COALESCE(100.0*count(*) FILTER(WHERE object_type='delivery' AND status IN('completed','accepted','closed') AND (due_date IS NULL OR CASE WHEN COALESCE(data->>'deliveredAt','') ~ '^\d{4}-\d{2}-\d{2}' THEN left(data->>'deliveredAt',10)::date ELSE updated_at::date END<=due_date)) / NULLIF(count(*) FILTER(WHERE object_type='delivery' AND status IN('completed','accepted','closed')),0),0),COALESCE(100.0*count(*) FILTER(WHERE object_type IN('quality','inspection') AND (status IN('rejected','defect','ncr','return') OR COALESCE(data->>'defect','false') IN ('true','1','yes'))) / NULLIF(count(*) FILTER(WHERE object_type IN('quality','inspection')),0),0),count(*) FILTER(WHERE object_type='rfq' AND status IN('open','active','pending_approval')),count(*) FILTER(WHERE object_type='rfp' AND status IN('open','active','pending_approval')),count(*) FILTER(WHERE object_type='delivery' AND due_date<current_date AND status NOT IN('completed','accepted','closed')) FROM business_objects WHERE deleted_at IS NULL AND (vendra_org_in_scope(organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&expiring, &openIssues, &contractValue, &deliveryCompliance, &defectRate, &activeRFQ, &activeRFP, &overdueDeliveries)
	if !showAmounts {
		contractValue = 0
	}
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM workflow_instances wi JOIN business_objects o ON o.id=wi.object_id WHERE wi.status='pending' AND (vendra_org_in_scope(o.organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND o.owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&pendingApprovals)
	_ = a.db.QueryRow(r.Context(), `SELECT count(*) FROM suppliers WHERE deleted_at IS NULL AND status='screening' AND (vendra_org_in_scope(organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&pendingScreenings)
	rows, _ := a.db.Query(r.Context(), `SELECT id,name,annual_spend,risk_level,score FROM suppliers WHERE deleted_at IS NULL AND (vendra_org_in_scope(organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND owner_id=$3::uuid)) ORDER BY annual_spend DESC LIMIT 5`, p.DataScope, organizationID, p.ID)
	top := []map[string]any{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id, name, risk string
			var amount float64
			var score *float64
			if rows.Scan(&id, &name, &amount, &risk, &score) == nil {
				if !showAmounts {
					amount = 0
				}
				top = append(top, map[string]any{"id": id, "name": name, "annualSpend": amount, "riskLevel": risk, "score": score})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"kpis": map[string]any{"totalSuppliers": total, "activeSuppliers": active, "newSuppliers": newCount, "highRiskSuppliers": highRisk, "annualSpend": spend, "averageScore": avgScore, "expiringContracts": expiring, "openIssues": openIssues, "activeContractValue": contractValue, "deliveryCompliance": deliveryCompliance, "defectRate": defectRate, "activeRFQ": activeRFQ, "activeRFP": activeRFP, "overdueDeliveries": overdueDeliveries, "pendingApprovals": pendingApprovals, "pendingScreenings": pendingScreenings}, "topSuppliers": top})
}

func (a *App) globalSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeJSON(w, 200, map[string]any{"items": []any{}})
		return
	}
	p, _ := principalFrom(r.Context())
	// Global search spans every supplier and narrows by organisation, which is
	// not how a portal account is isolated. The portal has its own screens.
	if p.UserType == "supplier" {
		writeJSON(w, 200, map[string]any{"items": []any{}})
		return
	}
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	items := []map[string]any{}
	if hasPermission(p, "supplier.read") || hasPermission(p, "*.read") {
		rows, _ := a.db.Query(r.Context(), `SELECT id,supplier_number,name,status FROM suppliers WHERE deleted_at IS NULL AND (name ILIKE '%'||$1||'%' OR business_number ILIKE '%'||$1||'%' OR supplier_number ILIKE '%'||$1||'%') AND (vendra_org_in_scope(organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND owner_id=$4::uuid)) LIMIT 10`, q, p.DataScope, organizationID, p.ID)
		if rows != nil {
			for rows.Next() {
				var id, num, title, status string
				if rows.Scan(&id, &num, &title, &status) == nil {
					items = append(items, map[string]any{"id": id, "type": "supplier", "number": num, "title": title, "status": status, "url": "/suppliers/" + id})
				}
			}
			rows.Close()
		}
	}
	// Restrict by object type inside the query. Filtering after a LIMIT dropped
	// rows the user was allowed to see: a reviewer with only contract.read got
	// whichever contracts happened to fall inside the newest thirty rows of
	// every type, and silently lost the rest.
	if readable := readableObjectTypes(p); len(readable) > 0 {
		rows, err := a.db.Query(r.Context(), `SELECT id,object_type,number,title,status FROM business_objects WHERE deleted_at IS NULL AND object_type=ANY($5) AND (title ILIKE '%'||$1||'%' OR number ILIKE '%'||$1||'%') AND (vendra_org_in_scope(organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND owner_id=$4::uuid)) ORDER BY updated_at DESC LIMIT 30`, q, p.DataScope, organizationID, p.ID, readable)
		if err != nil {
			logDB(err)
		} else {
			for rows.Next() {
				var id, typ, num, title, status string
				if rows.Scan(&id, &typ, &num, &title, &status) == nil {
					items = append(items, map[string]any{"id": id, "type": typ, "number": num, "title": title, "status": status})
				}
			}
			rows.Close()
		}
	}
	if hasPermission(p, "supplier.read") || hasPermission(p, "*.read") {
		rows, _ := a.db.Query(r.Context(), `SELECT c.id,'contact',COALESCE(c.title,''),c.name,'active',c.supplier_id FROM supplier_contacts c JOIN suppliers s ON s.id=c.supplier_id WHERE (c.name ILIKE '%'||$1||'%' OR c.email ILIKE '%'||$1||'%') AND s.deleted_at IS NULL AND (vendra_org_in_scope(s.organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND s.owner_id=$4::uuid)) LIMIT 10`, q, p.DataScope, organizationID, p.ID)
		if rows != nil {
			for rows.Next() {
				var id, typ, num, title, status, supplierID string
				if rows.Scan(&id, &typ, &num, &title, &status, &supplierID) == nil {
					items = append(items, map[string]any{"id": id, "type": typ, "number": num, "title": title, "status": status, "url": "/suppliers/" + supplierID})
				}
			}
			rows.Close()
		}
	}
	if hasPermission(p, "document.read") || hasPermission(p, "*.read") {
		rows, _ := a.db.Query(r.Context(), `SELECT d.id,d.document_type,d.name,d.status,d.supplier_id FROM documents d LEFT JOIN suppliers s ON s.id=d.supplier_id WHERE d.name ILIKE '%'||$1||'%' AND ((d.supplier_id IS NULL AND ($2='company' OR d.uploaded_by=$4::uuid)) OR vendra_org_in_scope(s.organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND s.owner_id=$4::uuid)) LIMIT 10`, q, p.DataScope, organizationID, p.ID)
		if rows != nil {
			for rows.Next() {
				var id, num, title, status string
				var supplierID *string
				if rows.Scan(&id, &num, &title, &status, &supplierID) == nil {
					item := map[string]any{"id": id, "type": "document", "number": num, "title": title, "status": status}
					if supplierID != nil {
						item["url"] = "/suppliers/" + *supplierID
					}
					items = append(items, item)
				}
			}
			rows.Close()
		}
	}
	if hasPermission(p, "evaluation.read") || hasPermission(p, "*.read") {
		rows, _ := a.db.Query(r.Context(), `SELECT e.id,COALESCE(t.name,e.evaluation_type),s.name,e.status,e.supplier_id FROM evaluations e JOIN suppliers s ON s.id=e.supplier_id LEFT JOIN scorecard_templates t ON t.id=e.template_id WHERE (s.name ILIKE '%'||$1||'%' OR COALESCE(t.name,e.evaluation_type) ILIKE '%'||$1||'%') AND s.deleted_at IS NULL AND (vendra_org_in_scope(s.organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND s.owner_id=$4::uuid)) LIMIT 10`, q, p.DataScope, organizationID, p.ID)
		if rows != nil {
			for rows.Next() {
				var id, title, supplierName, status, supplierID string
				if rows.Scan(&id, &title, &supplierName, &status, &supplierID) == nil {
					items = append(items, map[string]any{"id": id, "type": "evaluation", "number": supplierName, "title": title, "status": status, "url": "/suppliers/" + supplierID + "?tab=Evaluations"})
				}
			}
			rows.Close()
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) listSupplierRisks(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,risk_type,probability,impact,score,severity,status,description,mitigation,owner_id,to_char(review_date,'YYYY-MM-DD'),created_at,updated_at FROM risks WHERE supplier_id=$1 ORDER BY CASE severity WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END,created_at DESC`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "리스크를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, typ, severity, status string
		var prob, impact, score float64
		var desc, mit, owner, review *string
		var created, updated any
		if rows.Scan(&id, &typ, &prob, &impact, &score, &severity, &status, &desc, &mit, &owner, &review, &created, &updated) == nil {
			items = append(items, map[string]any{"id": id, "riskType": typ, "probability": prob, "impact": impact, "score": score, "severity": severity, "status": status, "description": desc, "mitigation": mit, "ownerId": owner, "reviewDate": review, "createdAt": created, "updatedAt": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createRisk(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	typ := stringValue(in, "riskType")
	severity := stringValue(in, "severity")
	if typ == "" || severity == "" {
		writeError(w, 400, "validation_error", "리스크 유형과 등급은 필수입니다")
		return
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO risks(supplier_id,risk_type,probability,impact,severity,status,description,mitigation,owner_id,review_date) VALUES($1,$2,COALESCE($3,0),COALESCE($4,0),$5,COALESCE(NULLIF($6,''),'open'),NULLIF($7,''),NULLIF($8,''),NULLIF($9,'')::uuid,NULLIF($10,'')::date) RETURNING id`, r.PathValue("id"), typ, numberValue(in, "probability"), numberValue(in, "impact"), severity, stringValue(in, "status"), stringValue(in, "description"), stringValue(in, "mitigation"), stringValue(in, "ownerId"), stringValue(in, "reviewDate")).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "리스크를 저장하지 못했습니다")
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE suppliers SET risk_level=$2,updated_at=now() WHERE id=$1 AND CASE $2 WHEN 'CRITICAL' THEN 4 WHEN 'HIGH' THEN 3 WHEN 'MEDIUM' THEN 2 ELSE 1 END > CASE risk_level WHEN 'CRITICAL' THEN 4 WHEN 'HIGH' THEN 3 WHEN 'MEDIUM' THEN 2 ELSE 1 END`, r.PathValue("id"), severity)
	a.audit.record(r, "create", "risk", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) listEvaluations(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT e.id,e.evaluation_type,e.status,e.period_start,e.period_end,e.scores,e.total_score,e.grade,e.evaluator_id,e.comments,e.created_at,e.updated_at,t.name FROM evaluations e LEFT JOIN scorecard_templates t ON t.id=e.template_id WHERE e.supplier_id=$1 ORDER BY e.created_at DESC`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "평가를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, typ, status string
		var start, end *string
		var scores []byte
		var total *float64
		var grade, evaluator, comments, template *string
		var created, updated any
		if rows.Scan(&id, &typ, &status, &start, &end, &scores, &total, &grade, &evaluator, &comments, &created, &updated, &template) == nil {
			var s any
			_ = json.Unmarshal(scores, &s)
			items = append(items, map[string]any{"id": id, "evaluationType": typ, "status": status, "periodStart": start, "periodEnd": end, "scores": s, "totalScore": total, "grade": grade, "evaluatorId": evaluator, "comments": comments, "templateName": template, "createdAt": created, "updatedAt": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createEvaluation(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	p, _ := principalFrom(r.Context())
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	templateID := stringValue(in, "templateId")
	if templateID == "" {
		_ = a.db.QueryRow(r.Context(), `SELECT id FROM scorecard_templates WHERE active=true ORDER BY created_at LIMIT 1`).Scan(&templateID)
	}
	var criteria, grades []byte
	if err := a.db.QueryRow(r.Context(), `SELECT criteria,grade_rules FROM scorecard_templates WHERE id=$1`, templateID).Scan(&criteria, &grades); err != nil {
		writeError(w, 400, "invalid_template", "평가 템플릿을 찾을 수 없습니다")
		return
	}
	scores, _ := in["scores"].(map[string]any)
	var criteriaList []map[string]any
	_ = json.Unmarshal(criteria, &criteriaList)
	total := 0.0
	for _, c := range criteriaList {
		code, _ := c["code"].(string)
		weight, _ := c["weight"].(float64)
		score, _ := scores[code].(float64)
		total += score * weight / 100
	}
	grade := "D"
	var gradeRules []map[string]any
	_ = json.Unmarshal(grades, &gradeRules)
	for _, rule := range gradeRules {
		min, _ := rule["min"].(float64)
		if total >= min {
			grade, _ = rule["grade"].(string)
			break
		}
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO evaluations(supplier_id,template_id,evaluation_type,status,period_start,period_end,scores,total_score,grade,evaluator_id,comments) VALUES($1,$2,COALESCE(NULLIF($3,''),'periodic'),COALESCE(NULLIF($4,''),'completed'),NULLIF($5,'')::date,NULLIF($6,'')::date,$7,$8,$9,$10,NULLIF($11,'')) RETURNING id`, r.PathValue("id"), templateID, stringValue(in, "evaluationType"), stringValue(in, "status"), stringValue(in, "periodStart"), stringValue(in, "periodEnd"), raw(scores), total, grade, p.ID, stringValue(in, "comments")).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "평가를 저장하지 못했습니다")
		return
	}
	aggregateScore := total
	_ = a.db.QueryRow(r.Context(), `SELECT COALESCE(avg(total_score),$2) FROM evaluations WHERE supplier_id=$1 AND status='completed'`, r.PathValue("id"), total).Scan(&aggregateScore)
	aggregateGrade := "D"
	for _, rule := range gradeRules {
		min, _ := rule["min"].(float64)
		if aggregateScore >= min {
			aggregateGrade, _ = rule["grade"].(string)
			break
		}
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE suppliers SET score=$2,grade=$3,updated_at=now() WHERE id=$1`, r.PathValue("id"), aggregateScore, aggregateGrade)
	a.audit.record(r, "create", "evaluation", id, nil, map[string]any{"scores": scores, "totalScore": total, "grade": grade})
	writeJSON(w, 201, map[string]any{"id": id, "totalScore": total, "grade": grade, "aggregateScore": aggregateScore, "aggregateGrade": aggregateGrade})
}

func (a *App) listAllRisks(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',r.id,'supplierId',r.supplier_id,'supplierName',s.name,'riskType',r.risk_type,'probability',r.probability,'impact',r.impact,'score',r.score,'severity',r.severity,'status',r.status,'description',r.description,'mitigation',r.mitigation,'reviewDate',r.review_date) FROM risks r JOIN suppliers s ON s.id=r.supplier_id WHERE s.deleted_at IS NULL AND (vendra_org_in_scope(s.organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND s.owner_id=$4::uuid)) ORDER BY CASE r.severity WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END,r.score DESC LIMIT $1`, parseLimit(r, 300), p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "리스크를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) == nil {
			var v any
			_ = json.Unmarshal(b, &v)
			items = append(items, v)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) listAllEvaluations(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',e.id,'supplierId',e.supplier_id,'supplierName',s.name,'evaluationType',e.evaluation_type,'status',e.status,'periodStart',e.period_start,'periodEnd',e.period_end,'totalScore',e.total_score,'grade',e.grade,'scores',e.scores,'createdAt',e.created_at) FROM evaluations e JOIN suppliers s ON s.id=e.supplier_id WHERE s.deleted_at IS NULL AND (vendra_org_in_scope(s.organization_id,$2,NULLIF($3,'')::uuid) OR ($2='own' AND s.owner_id=$4::uuid)) ORDER BY e.created_at DESC LIMIT $1`, parseLimit(r, 300), p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "평가를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) == nil {
			var v any
			_ = json.Unmarshal(b, &v)
			items = append(items, v)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) spendAnalysis(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	groupBy := r.URL.Query().Get("groupBy")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	var query string
	switch groupBy {
	case "category":
		query = `SELECT jsonb_build_object('key',COALESCE(t.category,'미분류'),'amount',sum(t.amount),'transactionCount',count(*),'contractedAmount',sum(t.amount) FILTER(WHERE t.contracted),'nonContractedAmount',sum(t.amount) FILTER(WHERE NOT t.contracted)) FROM spend_transactions t JOIN suppliers s ON s.id=t.supplier_id WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) AND (vendra_org_in_scope(s.organization_id,$4,NULLIF($5,'')::uuid) OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY t.category ORDER BY sum(t.amount) DESC LIMIT $3`
	case "organization":
		query = `SELECT jsonb_build_object('key',COALESCE(o.name,'미지정'),'organizationId',t.organization_id,'amount',sum(t.amount),'transactionCount',count(*)) FROM spend_transactions t JOIN suppliers s ON s.id=t.supplier_id LEFT JOIN organizations o ON o.id=t.organization_id WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) AND (vendra_org_in_scope(s.organization_id,$4,NULLIF($5,'')::uuid) OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY t.organization_id,o.name ORDER BY sum(t.amount) DESC LIMIT $3`
	case "month":
		query = `SELECT jsonb_build_object('key',to_char(date_trunc('month',t.transaction_date),'YYYY-MM'),'amount',sum(t.amount),'transactionCount',count(*),'contractedAmount',sum(t.amount) FILTER(WHERE t.contracted)) FROM spend_transactions t JOIN suppliers s ON s.id=t.supplier_id WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) AND (vendra_org_in_scope(s.organization_id,$4,NULLIF($5,'')::uuid) OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY date_trunc('month',t.transaction_date) ORDER BY date_trunc('month',t.transaction_date) LIMIT $3`
	default:
		query = `SELECT jsonb_build_object('supplierId',s.id,'supplierName',s.name,'annualSpend',COALESCE(sum(t.amount),s.annual_spend),'sharePercent',round(100*COALESCE(sum(t.amount),s.annual_spend)/NULLIF(sum(COALESCE(sum(t.amount),s.annual_spend)) OVER(),0),2),'dependencyRisk',CASE WHEN COALESCE(sum(t.amount),s.annual_spend)/NULLIF(sum(COALESCE(sum(t.amount),s.annual_spend)) OVER(),0)>=.4 THEN 'HIGH' WHEN COALESCE(sum(t.amount),s.annual_spend)/NULLIF(sum(COALESCE(sum(t.amount),s.annual_spend)) OVER(),0)>=.2 THEN 'MEDIUM' ELSE 'LOW' END,'riskLevel',s.risk_level,'score',s.score,'categories',s.categories,'transactionCount',count(t.id),'contractedAmount',COALESCE(sum(t.amount) FILTER(WHERE t.contracted),0),'nonContractedAmount',COALESCE(sum(t.amount) FILTER(WHERE NOT t.contracted),0)) FROM suppliers s LEFT JOIN spend_transactions t ON t.supplier_id=s.id AND ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) WHERE s.deleted_at IS NULL AND (vendra_org_in_scope(s.organization_id,$4,NULLIF($5,'')::uuid) OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY s.id ORDER BY COALESCE(sum(t.amount),s.annual_spend) DESC LIMIT $3`
	}
	rows, err := a.db.Query(r.Context(), query, from, to, parseLimit(r, 300), p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "구매 분석을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) == nil {
			var v any
			_ = json.Unmarshal(b, &v)
			items = append(items, v)
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createSpendTransaction(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		TransactionNumber string   `json:"transactionNumber"`
		SupplierID        string   `json:"supplierId"`
		OrganizationID    string   `json:"organizationId"`
		PurchaseOrderID   string   `json:"purchaseOrderId"`
		ContractID        string   `json:"contractId"`
		InvoiceID         string   `json:"invoiceId"`
		ItemCode          string   `json:"itemCode"`
		ItemName          string   `json:"itemName"`
		Category          string   `json:"category"`
		Quantity          *float64 `json:"quantity"`
		Unit              string   `json:"unit"`
		UnitPrice         *float64 `json:"unitPrice"`
		Amount            float64  `json:"amount"`
		Currency          string   `json:"currency"`
		TransactionDate   string   `json:"transactionDate"`
		Contracted        bool     `json:"contracted"`
		PaymentStatus     string   `json:"paymentStatus"`
		Metadata          any      `json:"metadata"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.SupplierID == "" || in.ItemName == "" || in.Amount < 0 || in.TransactionDate == "" {
		writeError(w, 400, "validation_error", "공급업체, 품목명, 금액, 거래일은 필수입니다")
		return
	}
	if !a.supplierScopeAllowed(r, in.SupplierID) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	if in.TransactionNumber == "" {
		in.TransactionNumber = "SPN-" + timeNowID()
	}
	if in.Currency == "" {
		in.Currency = "KRW"
	}
	if in.PaymentStatus == "" {
		in.PaymentStatus = "pending"
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO spend_transactions(transaction_number,supplier_id,organization_id,purchase_order_id,contract_id,invoice_id,item_code,item_name,category,quantity,unit,unit_price,amount,currency,transaction_date,contracted,payment_status,metadata,created_by) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,NULLIF($7,''),$8,NULLIF($9,''),$10,NULLIF($11,''),$12,$13,$14,$15::date,$16,$17,$18,$19) RETURNING id`, in.TransactionNumber, in.SupplierID, in.OrganizationID, in.PurchaseOrderID, in.ContractID, in.InvoiceID, in.ItemCode, in.ItemName, in.Category, in.Quantity, in.Unit, in.UnitPrice, in.Amount, in.Currency, in.TransactionDate, in.Contracted, in.PaymentStatus, raw(in.Metadata), p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "구매 원장을 저장하지 못했습니다")
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE suppliers SET annual_spend=(SELECT COALESCE(sum(amount),0) FROM spend_transactions WHERE supplier_id=$1 AND transaction_date>=current_date-365),updated_at=now() WHERE id=$1`, in.SupplierID)
	a.audit.record(r, "create", "spend_transaction", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id, "transactionNumber": in.TransactionNumber})
}

func (a *App) supplierNetwork(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	showSpend := hasPermission(p, "spend.read") || hasPermission(p, "analytics.read") || hasPermission(p, "*")
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	nodeRows, err := a.db.Query(r.Context(), `SELECT id,name,risk_level,grade,CASE WHEN $4 THEN annual_spend ELSE 0 END,categories FROM suppliers WHERE deleted_at IS NULL AND (vendra_org_in_scope(organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND owner_id=$3::uuid)) ORDER BY annual_spend DESC LIMIT 500`, p.DataScope, organizationID, p.ID, showSpend)
	if err != nil {
		writeError(w, 500, "database_error", "공급망을 조회하지 못했습니다")
		return
	}
	nodes := []map[string]any{}
	for nodeRows.Next() {
		var id, name, risk string
		var grade *string
		var spend float64
		var categories []byte
		if nodeRows.Scan(&id, &name, &risk, &grade, &spend, &categories) == nil {
			var cats any
			_ = json.Unmarshal(categories, &cats)
			nodes = append(nodes, map[string]any{"id": id, "name": name, "riskLevel": risk, "grade": grade, "annualSpend": spend, "categories": cats})
		}
	}
	nodeRows.Close()
	edgeRows, err := a.db.Query(r.Context(), `SELECT r.id,r.source_supplier_id,r.target_supplier_id,r.relationship_type,r.criticality,r.supplied_categories,r.dependency_percent,r.notes FROM supplier_relationships r JOIN suppliers s ON s.id=r.source_supplier_id JOIN suppliers t ON t.id=r.target_supplier_id WHERE (r.valid_until IS NULL OR r.valid_until>=current_date) AND ((vendra_org_in_scope(s.organization_id,$1,NULLIF($2,'')::uuid) AND vendra_org_in_scope(t.organization_id,$1,NULLIF($2,'')::uuid)) OR ($1='own' AND s.owner_id=$3::uuid AND t.owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "공급망 관계를 조회하지 못했습니다")
		return
	}
	edges := []map[string]any{}
	for edgeRows.Next() {
		var id, source, target, typ, criticality string
		var categories []byte
		var dependency *float64
		var notes *string
		if edgeRows.Scan(&id, &source, &target, &typ, &criticality, &categories, &dependency, &notes) == nil {
			var cats any
			_ = json.Unmarshal(categories, &cats)
			edges = append(edges, map[string]any{"id": id, "source": source, "target": target, "relationshipType": typ, "criticality": criticality, "categories": cats, "dependencyPercent": dependency, "notes": notes})
		}
	}
	edgeRows.Close()
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges})
}

func (a *App) createSupplierRelationship(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		SourceSupplierID  string   `json:"sourceSupplierId"`
		TargetSupplierID  string   `json:"targetSupplierId"`
		RelationshipType  string   `json:"relationshipType"`
		Criticality       string   `json:"criticality"`
		Categories        []string `json:"categories"`
		DependencyPercent *float64 `json:"dependencyPercent"`
		Notes             string   `json:"notes"`
	}
	if decodeJSON(r, &in) != nil || in.SourceSupplierID == "" || in.TargetSupplierID == "" || in.RelationshipType == "" {
		writeError(w, 400, "validation_error", "원천·대상 공급업체와 관계 유형은 필수입니다")
		return
	}
	if in.Criticality == "" {
		in.Criticality = "normal"
	}
	// Both ends must be in scope. The network graph filters every edge by the
	// organisation of both suppliers, so an unchecked write let a caller draw a
	// supply relationship between suppliers they cannot see: the edge then
	// showed up in another organisation's graph and cascade-risk analysis while
	// staying invisible to its author. The spend transaction beside this one
	// already performed the same check.
	if !a.supplierScopeAllowed(r, in.SourceSupplierID) || !a.supplierScopeAllowed(r, in.TargetSupplierID) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 공급업체입니다")
		return
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO supplier_relationships(source_supplier_id,target_supplier_id,relationship_type,criticality,supplied_categories,dependency_percent,notes,created_by) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8) ON CONFLICT(source_supplier_id,target_supplier_id,relationship_type) DO UPDATE SET criticality=excluded.criticality,supplied_categories=excluded.supplied_categories,dependency_percent=excluded.dependency_percent,notes=excluded.notes RETURNING id`, in.SourceSupplierID, in.TargetSupplierID, in.RelationshipType, in.Criticality, raw(in.Categories), in.DependencyPercent, in.Notes, p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "공급망 관계를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "supplier_relationship", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}
