package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	showAmounts := hasPermission(p, "spend.read") || hasPermission(p, "analytics.read") || hasPermission(p, "*")
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	var total, active, newCount, highRisk, mediumRisk, expiring, openIssues, activeRFQ, activeRFP, overdueDeliveries, pendingApprovals, pendingScreenings int
	var spend, avgScore, contractValue, deliveryCompliance, defectRate float64
	err := a.db.QueryRow(r.Context(), `SELECT count(*),count(*) FILTER(WHERE status='active'),count(*) FILTER(WHERE created_at>=date_trunc('month',now())),count(*) FILTER(WHERE risk_level IN('HIGH','CRITICAL')),count(*) FILTER(WHERE risk_level='MEDIUM'),COALESCE(sum(annual_spend),0),COALESCE(avg(score),0) FROM suppliers WHERE deleted_at IS NULL AND (`+orgInScope("organization_id", "$1", "$2")+` OR ($1='own' AND owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&total, &active, &newCount, &highRisk, &mediumRisk, &spend, &avgScore)
	if err != nil {
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	if !showAmounts {
		spend = 0
	}
	// deliveredAt lives in the free-form data blob, which any client can PATCH.
	// It used to be read as left(...,10)::date behind a regex loose enough to
	// admit "2026-13-45" — and that cast errors, so a single bad value made
	// this whole query fail. One PATCH the API answered with 200 left the
	// dashboard returning 500 to everyone in scope, permanently. The silent
	// flag on jsonb_path_query_first turns an unparseable value into NULL
	// instead, and COALESCE falls back the way a missing value already did.
	// Casting through timestamptz also means the date is the business day
	// rather than the UTC one, which is what compliance is measured against.
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FILTER(WHERE object_type='contract' AND end_date BETWEEN current_date AND current_date+180),count(*) FILTER(WHERE object_type='issue' AND status NOT IN('closed','resolved')),COALESCE(sum(amount) FILTER(WHERE object_type='contract' AND status IN('active','approved')),0),COALESCE(100.0*count(*) FILTER(WHERE object_type='delivery' AND status IN('completed','accepted','closed') AND (due_date IS NULL OR `+"COALESCE("+jsonDate("data", "deliveredAt")+", updated_at::date)"+`<=due_date)) / NULLIF(count(*) FILTER(WHERE object_type='delivery' AND status IN('completed','accepted','closed')),0),0),COALESCE(100.0*count(*) FILTER(WHERE object_type IN('quality','inspection') AND (status IN('rejected','defect','ncr','return') OR COALESCE(data->>'defect','false') IN ('true','1','yes'))) / NULLIF(count(*) FILTER(WHERE object_type IN('quality','inspection')),0),0),count(*) FILTER(WHERE object_type='rfq' AND status IN('open','active','pending_approval')),count(*) FILTER(WHERE object_type='rfp' AND status IN('open','active','pending_approval')),count(*) FILTER(WHERE object_type='delivery' AND due_date<current_date AND status NOT IN('completed','accepted','closed')) FROM business_objects WHERE deleted_at IS NULL AND (`+orgInScope("organization_id", "$1", "$2")+` OR ($1='own' AND owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&expiring, &openIssues, &contractValue, &deliveryCompliance, &defectRate, &activeRFQ, &activeRFP, &overdueDeliveries); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	if !showAmounts {
		contractValue = 0
	}
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM workflow_instances wi JOIN business_objects o ON o.id=wi.object_id WHERE wi.status='pending' AND (`+orgInScope("o.organization_id", "$1", "$2")+` OR ($1='own' AND o.owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&pendingApprovals); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM suppliers WHERE deleted_at IS NULL AND status='screening' AND (`+orgInScope("organization_id", "$1", "$2")+` OR ($1='own' AND owner_id=$3::uuid))`, p.DataScope, organizationID, p.ID).Scan(&pendingScreenings); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,annual_spend,risk_level,score FROM suppliers WHERE deleted_at IS NULL AND (`+orgInScope("organization_id", "$1", "$2")+` OR ($1='own' AND owner_id=$3::uuid)) ORDER BY annual_spend DESC LIMIT 5`, p.DataScope, organizationID, p.ID)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	top := []map[string]any{}
	for rows.Next() {
		var id, name, risk string
		var amount float64
		var score *float64
		if err := rows.Scan(&id, &name, &amount, &risk, &score); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
			return
		}
		if !showAmounts {
			amount = 0
		}
		top = append(top, map[string]any{"id": id, "name": name, "annualSpend": amount, "riskLevel": risk, "score": score})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "대시보드를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"kpis": map[string]any{"totalSuppliers": total, "activeSuppliers": active, "newSuppliers": newCount, "highRiskSuppliers": highRisk, "mediumRiskSuppliers": mediumRisk, "annualSpend": spend, "averageScore": avgScore, "expiringContracts": expiring, "openIssues": openIssues, "activeContractValue": contractValue, "deliveryCompliance": deliveryCompliance, "defectRate": defectRate, "activeRFQ": activeRFQ, "activeRFP": activeRFP, "overdueDeliveries": overdueDeliveries, "pendingApprovals": pendingApprovals, "pendingScreenings": pendingScreenings}, "topSuppliers": top})
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
	// A leg that fails must not be mistaken for a leg that matched nothing:
	// search quietly dropping a whole category reads as fact to the user, so a
	// failure fails the search.
	// Each leg is capped, and a cap the answer does not mention reads as "this
	// is everything". Someone looking for a supplier they know exists, in a
	// register full of similarly named ones, concludes it is not there. Every
	// other list in the application already says when it stopped short; this
	// one did not. Each query now asks for one row past its limit, and the
	// extra row answers the question without a second count.
	cut := []string{}
	add := func(category string, limit int, rows pgx.Rows, err error, scan func(pgx.Rows) (map[string]any, error)) bool {
		found, err := searchSource(rows, err, scan)
		if err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "검색하지 못했습니다")
			return false
		}
		kept, truncated := truncate(found, limit)
		if truncated {
			cut = append(cut, category)
		}
		items = append(items, kept...)
		return true
	}
	if hasPermission(p, "supplier.read") || hasPermission(p, "*.read") {
		rows, err := a.db.Query(r.Context(), `SELECT id,supplier_number,name,status FROM suppliers WHERE deleted_at IS NULL AND (name ILIKE '%'||$1||'%' OR business_number ILIKE '%'||$1||'%' OR supplier_number ILIKE '%'||$1||'%') AND (`+orgInScope("organization_id", "$2", "$3")+` OR ($2='own' AND owner_id=$4::uuid)) LIMIT 11`, q, p.DataScope, organizationID, p.ID)
		if !add("공급업체", 10, rows, err, func(rows pgx.Rows) (map[string]any, error) {
			var id, num, title, status string
			err := rows.Scan(&id, &num, &title, &status)
			return map[string]any{"id": id, "type": "supplier", "number": num, "title": title, "status": status, "url": "/suppliers/" + id}, err
		}) {
			return
		}
	}
	// Restrict by object type inside the query. Filtering after a LIMIT dropped
	// rows the user was allowed to see: a reviewer with only contract.read got
	// whichever contracts happened to fall inside the newest thirty rows of
	// every type, and silently lost the rest.
	if readable := readableObjectTypes(p); len(readable) > 0 {
		rows, err := a.db.Query(r.Context(), `SELECT id,object_type,number,title,status FROM business_objects WHERE deleted_at IS NULL AND object_type=ANY($5) AND (title ILIKE '%'||$1||'%' OR number ILIKE '%'||$1||'%') AND (`+orgInScope("organization_id", "$2", "$3")+` OR ($2='own' AND owner_id=$4::uuid)) ORDER BY updated_at DESC LIMIT 31`, q, p.DataScope, organizationID, p.ID, readable)
		if !add("업무", 30, rows, err, func(rows pgx.Rows) (map[string]any, error) {
			var id, typ, num, title, status string
			err := rows.Scan(&id, &typ, &num, &title, &status)
			return map[string]any{"id": id, "type": typ, "number": num, "title": title, "status": status}, err
		}) {
			return
		}
	}
	if hasPermission(p, "supplier.read") || hasPermission(p, "*.read") {
		rows, err := a.db.Query(r.Context(), `SELECT c.id,'contact',COALESCE(c.title,''),c.name,'active',c.supplier_id FROM supplier_contacts c JOIN suppliers s ON s.id=c.supplier_id WHERE (c.name ILIKE '%'||$1||'%' OR c.email ILIKE '%'||$1||'%') AND s.deleted_at IS NULL AND (`+orgInScope("s.organization_id", "$2", "$3")+` OR ($2='own' AND s.owner_id=$4::uuid)) LIMIT 11`, q, p.DataScope, organizationID, p.ID)
		if !add("담당자", 10, rows, err, func(rows pgx.Rows) (map[string]any, error) {
			var id, typ, num, title, status, supplierID string
			err := rows.Scan(&id, &typ, &num, &title, &status, &supplierID)
			return map[string]any{"id": id, "type": typ, "number": num, "title": title, "status": status, "url": "/suppliers/" + supplierID}, err
		}) {
			return
		}
	}
	if hasPermission(p, "document.read") || hasPermission(p, "*.read") {
		rows, err := a.db.Query(r.Context(), `SELECT d.id,d.document_type,d.name,d.status,d.supplier_id FROM documents d LEFT JOIN suppliers s ON s.id=d.supplier_id WHERE d.name ILIKE '%'||$1||'%' AND ((d.supplier_id IS NULL AND ($2='company' OR d.uploaded_by=$4::uuid)) OR `+orgInScope("s.organization_id", "$2", "$3")+` OR ($2='own' AND s.owner_id=$4::uuid)) LIMIT 11`, q, p.DataScope, organizationID, p.ID)
		if !add("문서", 10, rows, err, func(rows pgx.Rows) (map[string]any, error) {
			var id, num, title, status string
			var supplierID *string
			err := rows.Scan(&id, &num, &title, &status, &supplierID)
			item := map[string]any{"id": id, "type": "document", "number": num, "title": title, "status": status}
			if supplierID != nil {
				item["url"] = "/suppliers/" + *supplierID
			}
			return item, err
		}) {
			return
		}
	}
	if hasPermission(p, "evaluation.read") || hasPermission(p, "*.read") {
		rows, err := a.db.Query(r.Context(), `SELECT e.id,COALESCE(t.name,e.evaluation_type),s.name,e.status,e.supplier_id FROM evaluations e JOIN suppliers s ON s.id=e.supplier_id LEFT JOIN scorecard_templates t ON t.id=e.template_id WHERE (s.name ILIKE '%'||$1||'%' OR COALESCE(t.name,e.evaluation_type) ILIKE '%'||$1||'%') AND s.deleted_at IS NULL AND (`+orgInScope("s.organization_id", "$2", "$3")+` OR ($2='own' AND s.owner_id=$4::uuid)) LIMIT 11`, q, p.DataScope, organizationID, p.ID)
		if !add("평가", 10, rows, err, func(rows pgx.Rows) (map[string]any, error) {
			var id, title, supplierName, status, supplierID string
			err := rows.Scan(&id, &title, &supplierName, &status, &supplierID)
			return map[string]any{"id": id, "type": "evaluation", "number": supplierName, "title": title, "status": status, "url": "/suppliers/" + supplierID + "?tab=Evaluations"}, err
		}) {
			return
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "truncated": len(cut) > 0, "truncatedCategories": cut})
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
		if err := rows.Scan(&id, &typ, &prob, &impact, &score, &severity, &status, &desc, &mit, &owner, &review, &created, &updated); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "리스크를 조회하지 못했습니다")
			return
		}
		items = append(items, map[string]any{"id": id, "riskType": typ, "probability": prob, "impact": impact, "score": score, "severity": severity, "status": status, "description": desc, "mitigation": mit, "ownerId": owner, "reviewDate": review, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "리스크를 조회하지 못했습니다")
		return
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
	// The grade drives the supplier's risk_level rollup and every badge that
	// reads it, and the form offers exactly these four. Anything else was
	// stored verbatim and shown as if it meant something.
	switch severity {
	case "LOW", "MEDIUM", "HIGH", "CRITICAL":
	default:
		writeError(w, 400, "validation_error", "리스크 등급은 LOW, MEDIUM, HIGH, CRITICAL 중 하나여야 합니다")
		return
	}
	if utf8.RuneCountInString(typ) > maxIdentifierLen {
		writeError(w, 400, "validation_error", fmt.Sprintf("리스크 유형은 %d자를 넘을 수 없습니다", maxIdentifierLen))
		return
	}
	// score is a generated column, probability * impact, and every risk list
	// and the MCP tool order by it. The form rates both 0..10 and the API
	// rated neither: a risk entered as -5 by -8 scored 40, the same as a
	// genuine 5 by 8, and sorted to the top of the list beside it. Only 1000
	// was refused, by numeric(5,2) overflowing, and then as "리스크를 저장하지
	// 못했습니다".
	for _, field := range []struct {
		key, label string
	}{{"probability", "발생 가능성"}, {"impact", "영향도"}} {
		value, ok := numberValue(in, field.key).(float64)
		if !ok {
			continue
		}
		if value < 0 || value > 10 {
			writeError(w, 400, "validation_error", fmt.Sprintf("%s%s 0에서 10 사이여야 합니다", field.label, topicParticle(field.label)))
			return
		}
	}
	var id string
	if !validDateFields(w, in, dateField{"reviewDate", "검토일"}) {
		return
	}
	err = a.db.QueryRow(r.Context(), `INSERT INTO risks(supplier_id,risk_type,probability,impact,severity,status,description,mitigation,owner_id,review_date) VALUES($1,$2,COALESCE($3,0),COALESCE($4,0),$5,COALESCE(NULLIF($6,''),'open'),NULLIF($7,''),NULLIF($8,''),NULLIF($9,'')::uuid,NULLIF($10,'')::date) RETURNING id`, r.PathValue("id"), typ, numberValue(in, "probability"), numberValue(in, "impact"), severity, stringValue(in, "status"), stringValue(in, "description"), stringValue(in, "mitigation"), stringValue(in, "ownerId"), stringValue(in, "reviewDate")).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "리스크를 저장하지 못했습니다")
		return
	}
	// A cached rollup. The record itself saved, and the next write recomputes
	// this, so a stale value must not fail a successful create.
	if _, err := a.db.Exec(r.Context(), `UPDATE suppliers SET risk_level=$2,updated_at=now() WHERE id=$1 AND CASE $2 WHEN 'CRITICAL' THEN 4 WHEN 'HIGH' THEN 3 WHEN 'MEDIUM' THEN 2 ELSE 1 END > CASE risk_level WHEN 'CRITICAL' THEN 4 WHEN 'HIGH' THEN 3 WHEN 'MEDIUM' THEN 2 ELSE 1 END`, r.PathValue("id"), severity); err != nil {
		logDB(err)
	}
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
		if err := rows.Scan(&id, &typ, &status, &start, &end, &scores, &total, &grade, &evaluator, &comments, &created, &updated, &template); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "평가를 조회하지 못했습니다")
			return
		}
		var s any
		_ = json.Unmarshal(scores, &s)
		items = append(items, map[string]any{"id": id, "evaluationType": typ, "status": status, "periodStart": start, "periodEnd": end, "scores": s, "totalScore": total, "grade": grade, "evaluatorId": evaluator, "comments": comments, "templateName": template, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가를 조회하지 못했습니다")
		return
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
	// The score is derived from the criteria, never asserted by the caller, so
	// a submission that scores nothing derives zero — and zero is below every
	// grade rule, so it was stored as a genuine D. Three malformed requests
	// each planted one, and because the supplier's grade is the average across
	// evaluations, a supplier who actually scored 86.1 (an A) sat on the
	// register at 21.53 (a D). Every one of them was answered 201.
	//
	// A criterion scored zero is a real assessment; a criterion with no score
	// is a submission that could not have been graded, and grading it is a
	// fabrication.
	total := 0.0
	missing := []string{}
	for _, c := range criteriaList {
		code, _ := c["code"].(string)
		weight, _ := c["weight"].(float64)
		label, _ := c["name"].(string)
		if label == "" {
			label = code
		}
		score, scored := scores[code].(float64)
		if !scored {
			missing = append(missing, label)
			continue
		}
		if score < 0 || score > 100 {
			writeError(w, 400, "validation_error", fmt.Sprintf("%s 점수는 0에서 100 사이여야 합니다", label))
			return
		}
		total += score * weight / 100
	}
	if len(missing) > 0 {
		writeError(w, 400, "validation_error", "점수가 없는 평가 항목이 있습니다: "+strings.Join(missing, ", "))
		return
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
	if !validDateFields(w, in, dateField{"periodStart", "평가 시작일"}, dateField{"periodEnd", "평가 종료일"}) {
		return
	}
	err = a.db.QueryRow(r.Context(), `INSERT INTO evaluations(supplier_id,template_id,evaluation_type,status,period_start,period_end,scores,total_score,grade,evaluator_id,comments) VALUES($1,$2,COALESCE(NULLIF($3,''),'periodic'),COALESCE(NULLIF($4,''),'completed'),NULLIF($5,'')::date,NULLIF($6,'')::date,$7,$8,$9,$10,NULLIF($11,'')) RETURNING id`, r.PathValue("id"), templateID, stringValue(in, "evaluationType"), stringValue(in, "status"), stringValue(in, "periodStart"), stringValue(in, "periodEnd"), raw(scores), total, grade, p.ID, stringValue(in, "comments")).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "평가를 저장하지 못했습니다")
		return
	}
	aggregateScore := total
	if err := a.db.QueryRow(r.Context(), `SELECT COALESCE(avg(total_score),$2) FROM evaluations WHERE supplier_id=$1 AND status='completed'`, r.PathValue("id"), total).Scan(&aggregateScore); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가 결과를 조회하지 못했습니다")
		return
	}
	aggregateGrade := "D"
	for _, rule := range gradeRules {
		min, _ := rule["min"].(float64)
		if aggregateScore >= min {
			aggregateGrade, _ = rule["grade"].(string)
			break
		}
	}
	// A cached rollup. The record itself saved, and the next write recomputes
	// this, so a stale value must not fail a successful create.
	if _, err := a.db.Exec(r.Context(), `UPDATE suppliers SET score=$2,grade=$3,updated_at=now() WHERE id=$1`, r.PathValue("id"), aggregateScore, aggregateGrade); err != nil {
		logDB(err)
	}
	a.audit.record(r, "create", "evaluation", id, nil, map[string]any{"scores": scores, "totalScore": total, "grade": grade})
	writeJSON(w, 201, map[string]any{"id": id, "totalScore": total, "grade": grade, "aggregateScore": aggregateScore, "aggregateGrade": aggregateGrade})
}

func (a *App) listAllRisks(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',r.id,'supplierId',r.supplier_id,'supplierName',s.name,'riskType',r.risk_type,'probability',r.probability,'impact',r.impact,'score',r.score,'severity',r.severity,'status',r.status,'description',r.description,'mitigation',r.mitigation,'reviewDate',r.review_date) FROM risks r JOIN suppliers s ON s.id=r.supplier_id WHERE s.deleted_at IS NULL AND (`+orgInScope("s.organization_id", "$2", "$3")+` OR ($2='own' AND s.owner_id=$4::uuid)) ORDER BY CASE r.severity WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END,r.score DESC LIMIT $1`, parseLimit(r, 300), p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "리스크를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "리스크를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) listAllEvaluations(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',e.id,'supplierId',e.supplier_id,'supplierName',s.name,'evaluationType',e.evaluation_type,'status',e.status,'periodStart',e.period_start,'periodEnd',e.period_end,'totalScore',e.total_score,'grade',e.grade,'scores',e.scores,'createdAt',e.created_at) FROM evaluations e JOIN suppliers s ON s.id=e.supplier_id WHERE s.deleted_at IS NULL AND (`+orgInScope("s.organization_id", "$2", "$3")+` OR ($2='own' AND s.owner_id=$4::uuid)) ORDER BY e.created_at DESC LIMIT $1`, parseLimit(r, 300), p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "평가를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가를 조회하지 못했습니다")
		return
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
	from, ok := dateParam(w, r, "from", "시작일")
	if !ok {
		return
	}
	to, ok := dateParam(w, r, "to", "종료일")
	if !ok {
		return
	}
	var query string
	switch groupBy {
	case "category":
		query = `SELECT jsonb_build_object('key',COALESCE(t.category,'미분류'),'amount',sum(t.amount),'transactionCount',count(*),'contractedAmount',sum(t.amount) FILTER(WHERE t.contracted),'nonContractedAmount',sum(t.amount) FILTER(WHERE NOT t.contracted)) FROM spend_transactions t JOIN suppliers s ON s.id=t.supplier_id WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) AND (` + orgInScope("s.organization_id", "$4", "$5") + ` OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY t.category ORDER BY sum(t.amount) DESC LIMIT $3`
	case "organization":
		query = `SELECT jsonb_build_object('key',COALESCE(o.name,'미지정'),'organizationId',t.organization_id,'amount',sum(t.amount),'transactionCount',count(*)) FROM spend_transactions t JOIN suppliers s ON s.id=t.supplier_id LEFT JOIN organizations o ON o.id=t.organization_id WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) AND (` + orgInScope("s.organization_id", "$4", "$5") + ` OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY t.organization_id,o.name ORDER BY sum(t.amount) DESC LIMIT $3`
	case "month":
		query = `SELECT jsonb_build_object('key',to_char(date_trunc('month',t.transaction_date),'YYYY-MM'),'amount',sum(t.amount),'transactionCount',count(*),'contractedAmount',sum(t.amount) FILTER(WHERE t.contracted)) FROM spend_transactions t JOIN suppliers s ON s.id=t.supplier_id WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date) AND (` + orgInScope("s.organization_id", "$4", "$5") + ` OR ($4='own' AND s.owner_id=$6::uuid)) GROUP BY date_trunc('month',t.transaction_date) ORDER BY date_trunc('month',t.transaction_date) LIMIT $3`
	default:
		// The share each supplier holds is of the whole register, so the total
		// has to be known before the top few can be reported — the window
		// cannot be pushed past the limit. What can be pushed past it is
		// everything else. Grouping the transactions on their own keeps the
		// hash narrow, where grouping them joined to the supplier row dragged
		// the name, the categories and the rest through it and spilled to disk
		// at ten thousand suppliers. The answer is unchanged, byte for byte;
		// 84/92/80/83 ms became 67/60/61/65 ms on a 295 MB register.
		query = `WITH totals AS (
	 SELECT t.supplier_id AS id,sum(t.amount) AS spend,count(*) AS transactions,
	        sum(t.amount) FILTER(WHERE t.contracted) AS contracted,
	        sum(t.amount) FILTER(WHERE NOT t.contracted) AS non_contracted
	 FROM spend_transactions t
	 WHERE ($1='' OR t.transaction_date>=$1::date) AND ($2='' OR t.transaction_date<=$2::date)
	 GROUP BY t.supplier_id
	), ranked AS (
	 SELECT s.id,s.name,s.risk_level,s.score,s.categories,
	        COALESCE(x.spend,s.annual_spend) AS spend,
	        sum(COALESCE(x.spend,s.annual_spend)) OVER() AS total,
	        COALESCE(x.transactions,0) AS transactions,
	        COALESCE(x.contracted,0) AS contracted,
	        COALESCE(x.non_contracted,0) AS non_contracted
	 FROM suppliers s LEFT JOIN totals x ON x.id=s.id
	 WHERE s.deleted_at IS NULL AND (` + orgInScope("s.organization_id", "$4", "$5") + ` OR ($4='own' AND s.owner_id=$6::uuid))
	 ORDER BY COALESCE(x.spend,s.annual_spend) DESC LIMIT $3
	)
	SELECT jsonb_build_object('supplierId',id,'supplierName',name,'annualSpend',spend,
	 'sharePercent',round(100*spend/NULLIF(total,0),2),
	 'dependencyRisk',CASE WHEN spend/NULLIF(total,0)>=.4 THEN 'HIGH' WHEN spend/NULLIF(total,0)>=.2 THEN 'MEDIUM' ELSE 'LOW' END,
	 'riskLevel',risk_level,'score',score,'categories',categories,
	 'transactionCount',transactions,'contractedAmount',contracted,'nonContractedAmount',non_contracted) FROM ranked`
	}
	rows, err := a.db.Query(r.Context(), query, from, to, parseLimit(r, 300), p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "구매 분석을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "구매 분석을 조회하지 못했습니다")
		return
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
	// The organisation is the grouping key of the organisation-level spend
	// report, so an unchecked value attributes this spend to a division the
	// caller has nothing to do with. The supplier was already validated.
	if !a.objectScopeAllowed(r, in.OrganizationID, in.SupplierID) {
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
	// A cached rollup. The record itself saved, and the next write recomputes
	// this, so a stale value must not fail a successful create.
	if _, err := a.db.Exec(r.Context(), `UPDATE suppliers SET annual_spend=(SELECT COALESCE(sum(amount),0) FROM spend_transactions WHERE supplier_id=$1 AND transaction_date>=current_date-365),updated_at=now() WHERE id=$1`, in.SupplierID); err != nil {
		logDB(err)
	}
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
	nodeRows, err := a.db.Query(r.Context(), `SELECT id,name,risk_level,grade,CASE WHEN $4 THEN annual_spend ELSE 0 END,categories FROM suppliers WHERE deleted_at IS NULL AND (`+orgInScope("organization_id", "$1", "$2")+` OR ($1='own' AND owner_id=$3::uuid)) ORDER BY annual_spend DESC LIMIT 500`, p.DataScope, organizationID, p.ID, showSpend)
	if err != nil {
		writeError(w, 500, "database_error", "공급망을 조회하지 못했습니다")
		return
	}
	nodes := []map[string]any{}
	nodeIDs := []string{}
	for nodeRows.Next() {
		var id, name, risk string
		var grade *string
		var spend float64
		var categories []byte
		if err := nodeRows.Scan(&id, &name, &risk, &grade, &spend, &categories); err != nil {
			nodeRows.Close()
			logDB(err)
			writeError(w, 500, "database_error", "공급망을 조회하지 못했습니다")
			return
		}
		var cats any
		_ = json.Unmarshal(categories, &cats)
		nodes = append(nodes, map[string]any{"id": id, "name": name, "riskLevel": risk, "grade": grade, "annualSpend": spend, "categories": cats})
		nodeIDs = append(nodeIDs, id)
	}
	nodeRows.Close()
	if err := nodeRows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "공급망을 조회하지 못했습니다")
		return
	}
	// The nodes above are the whole universe of this response, so an edge exists
	// only between two of them. Selecting edges on their own terms returned ones
	// with an end the caller never received: a soft-deleted supplier kept its
	// relationships, and so did anyone past the 500-node limit. The canvas drops
	// such an edge silently while the inspector still counts it, so "직접 연결"
	// disagreed with the lines actually drawn.
	edgeRows, err := a.db.Query(r.Context(), `SELECT r.id,r.source_supplier_id,r.target_supplier_id,r.relationship_type,r.criticality,r.supplied_categories,r.dependency_percent,r.notes FROM supplier_relationships r WHERE (r.valid_until IS NULL OR r.valid_until>=current_date) AND r.source_supplier_id=ANY($1) AND r.target_supplier_id=ANY($1)`, nodeIDs)
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
		if err := edgeRows.Scan(&id, &source, &target, &typ, &criticality, &categories, &dependency, &notes); err != nil {
			edgeRows.Close()
			logDB(err)
			writeError(w, 500, "database_error", "공급망 관계를 조회하지 못했습니다")
			return
		}
		var cats any
		_ = json.Unmarshal(categories, &cats)
		edges = append(edges, map[string]any{"id": id, "source": source, "target": target, "relationshipType": typ, "criticality": criticality, "categories": cats, "dependencyPercent": dependency, "notes": notes})
	}
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "공급망 관계를 조회하지 못했습니다")
		return
	}
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
	// A supplier does not supply itself. The edge drew as a loop on the graph
	// and counted toward whatever dependency the reader was working out.
	if in.SourceSupplierID == in.TargetSupplierID {
		writeError(w, 400, "validation_error", "같은 공급업체를 원천과 대상으로 지정할 수 없습니다")
		return
	}
	// A share of supply is a share. The column is numeric(7,2), so it took
	// -30 and 1000 without complaint and the graph handed both back; only
	// 100000 failed, and then as "저장하지 못했습니다".
	if in.DependencyPercent != nil && (*in.DependencyPercent < 0 || *in.DependencyPercent > 100) {
		writeError(w, 400, "validation_error", "의존도는 0에서 100 사이여야 합니다")
		return
	}
	for _, field := range []struct{ label, value string }{
		{"관계 유형", in.RelationshipType},
		{"중요도", in.Criticality},
	} {
		if utf8.RuneCountInString(field.value) > maxIdentifierLen {
			writeError(w, 400, "validation_error", fmt.Sprintf("%s%s %d자를 넘을 수 없습니다", field.label, topicParticle(field.label), maxIdentifierLen))
			return
		}
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
