package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// screeningThresholds is a template's result_rules. A template that omits them
// leaves every bound at zero, which made "total >= passMin" true for any score:
// the screening reported PASS and the supplier was approved automatically.
type screeningThresholds struct {
	PassMin               float64 `json:"passMin"`
	ConditionalMin        float64 `json:"conditionalMin"`
	ReviewMin             float64 `json:"reviewMin"`
	RequiredFailureResult string  `json:"requiredFailureResult"`
}

// usable reports whether the bounds can decide an outcome. They must be
// positive and ordered; anything else cannot distinguish a pass from a failure.
func (t screeningThresholds) usable() bool {
	return t.PassMin > 0 && t.PassMin >= t.ConditionalMin && t.ConditionalMin >= t.ReviewMin && t.ReviewMin >= 0
}

// decide maps a score to a screening result. An unusable rule set never passes
// anyone; it asks for a human decision and leaves the misconfiguration visible.
func (t screeningThresholds) decide(total float64, missingRequired bool) string {
	if !t.usable() {
		return "REVIEW_REQUIRED"
	}
	if missingRequired {
		if t.RequiredFailureResult != "" {
			return t.RequiredFailureResult
		}
		return "REVIEW_REQUIRED"
	}
	switch {
	case total >= t.PassMin:
		return "PASS"
	case total >= t.ConditionalMin:
		return "CONDITIONAL_PASS"
	case total >= t.ReviewMin:
		return "REVIEW_REQUIRED"
	default:
		return "REJECT"
	}
}

func (a *App) listScreeningTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT id,name,active,items,result_rules,required_document_types,created_at,updated_at FROM screening_templates ORDER BY active DESC,name`)
	if err != nil {
		writeError(w, 500, "database_error", "심사 템플릿을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name string
		var active bool
		var templateItems, rules, documents []byte
		var created, updated any
		if err := rows.Scan(&id, &name, &active, &templateItems, &rules, &documents, &created, &updated); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "심사 템플릿을 조회하지 못했습니다")
			return
		}
		var ti, rr, dd any
		_ = json.Unmarshal(templateItems, &ti)
		_ = json.Unmarshal(rules, &rr)
		_ = json.Unmarshal(documents, &dd)
		items = append(items, map[string]any{"id": id, "name": name, "active": active, "items": ti, "resultRules": rr, "requiredDocumentTypes": dd, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "심사 템플릿을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createScreeningTemplate(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Name                  string   `json:"name"`
		Active                bool     `json:"active"`
		Items                 any      `json:"items"`
		ResultRules           any      `json:"resultRules"`
		RequiredDocumentTypes []string `json:"requiredDocumentTypes"`
	}
	if decodeJSON(r, &in) != nil || in.Name == "" || in.Items == nil {
		writeError(w, 400, "validation_error", "이름과 심사항목은 필수입니다")
		return
	}
	// Without ordered, positive bounds the template cannot decide an outcome,
	// and every screening run against it would ask for a manual review.
	var thresholds screeningThresholds
	_ = json.Unmarshal(raw(in.ResultRules), &thresholds)
	if !thresholds.usable() {
		writeError(w, 400, "validation_error", "합격 기준은 passMin > conditionalMin > reviewMin >= 0 순서로 지정해야 합니다")
		return
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO screening_templates(name,active,items,result_rules,required_document_types,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, in.Name, in.Active, raw(in.Items), raw(in.ResultRules), raw(in.RequiredDocumentTypes), p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "심사 템플릿을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "screening_template", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) listScreenings(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',s.id,'supplierId',s.supplier_id,'templateId',s.template_id,'templateName',t.name,'status',s.status,'responses',s.responses,'domainResults',s.domain_results,'result',s.result,'reviewerId',s.reviewer_id,'comments',s.comments,'submittedAt',s.submitted_at,'completedAt',s.completed_at,'createdAt',s.created_at,'updatedAt',s.updated_at) FROM supplier_screenings s JOIN screening_templates t ON t.id=s.template_id WHERE s.supplier_id=$1 ORDER BY s.created_at DESC`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "심사 이력을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "심사 이력을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createScreening(w http.ResponseWriter, r *http.Request) {
	if !a.supplierScopeAllowed(r, r.PathValue("id")) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	var in struct {
		TemplateID string `json:"templateId"`
	}
	_ = decodeJSON(r, &in)
	if in.TemplateID == "" {
		_ = a.db.QueryRow(r.Context(), `SELECT id FROM screening_templates WHERE active=true ORDER BY created_at LIMIT 1`).Scan(&in.TemplateID)
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO supplier_screenings(supplier_id,template_id) VALUES($1,$2) RETURNING id`, r.PathValue("id"), in.TemplateID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "심사를 시작하지 못했습니다")
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE suppliers SET status='screening',updated_at=now() WHERE id=$1`, r.PathValue("id"))
	a.audit.record(r, "create", "screening", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) updateScreening(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Responses map[string]any `json:"responses"`
		Comments  string         `json:"comments"`
		Complete  bool           `json:"complete"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	var supplierID string
	var templateItems, rules, requiredDocuments []byte
	if err := a.db.QueryRow(r.Context(), `SELECT s.supplier_id,t.items,t.result_rules,t.required_document_types FROM supplier_screenings s JOIN screening_templates t ON t.id=s.template_id WHERE s.id=$1`, r.PathValue("id")).Scan(&supplierID, &templateItems, &rules, &requiredDocuments); err != nil {
		writeError(w, 404, "not_found", "심사를 찾을 수 없습니다")
		return
	}
	if !a.supplierScopeAllowed(r, supplierID) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
		return
	}
	domainResults, total, missingRequired := calculateScreening(templateItems, in.Responses)
	var requiredTypes []string
	_ = json.Unmarshal(requiredDocuments, &requiredTypes)
	missingDocumentTypes := []string{}
	for _, documentType := range requiredTypes {
		var exists bool
		_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM documents WHERE supplier_id=$1 AND document_type=$2 AND status IN('active','approved') AND (expires_at IS NULL OR expires_at>=current_date))`, supplierID, documentType).Scan(&exists)
		if !exists {
			missingDocumentTypes = append(missingDocumentTypes, documentType)
		}
	}
	if len(missingDocumentTypes) > 0 {
		missingRequired = true
	}
	result := ""
	status := "in_progress"
	var completed any
	if in.Complete {
		var thresholds screeningThresholds
		_ = json.Unmarshal(rules, &thresholds)
		if !thresholds.usable() {
			slog.Warn("screening template has no usable result thresholds; refusing to pass automatically",
				"screening_id", r.PathValue("id"), "supplier_id", supplierID, "request_id", requestID(r.Context()))
		}
		result = thresholds.decide(total, missingRequired)
		status = "completed"
		completed = time.Now()
	}
	_, err := a.db.Exec(r.Context(), `UPDATE supplier_screenings SET responses=$2,domain_results=$3,result=NULLIF($4,''),status=$5,reviewer_id=$6,comments=NULLIF($7,''),submitted_at=COALESCE(submitted_at,now()),completed_at=$8,updated_at=now() WHERE id=$1`, r.PathValue("id"), raw(in.Responses), raw(domainResults), result, status, p.ID, in.Comments, completed)
	if err != nil {
		writeError(w, 400, "save_failed", "심사를 저장하지 못했습니다")
		return
	}
	if in.Complete {
		supplierStatus := "screening"
		if result == "PASS" || result == "CONDITIONAL_PASS" {
			supplierStatus = "approved"
		}
		if result == "REJECT" {
			supplierStatus = "suspended"
		}
		_, _ = a.db.Exec(r.Context(), `UPDATE suppliers SET status=$2,updated_at=now() WHERE id=$1`, supplierID, supplierStatus)
	}
	a.audit.record(r, "update", "screening", r.PathValue("id"), nil, map[string]any{"responses": in.Responses, "totalScore": total, "result": result, "missingDocumentTypes": missingDocumentTypes})
	writeJSON(w, 200, map[string]any{"status": status, "domainResults": domainResults, "totalScore": total, "result": result, "missingRequired": missingRequired, "missingDocumentTypes": missingDocumentTypes})
}

func calculateScreening(itemsJSON []byte, responses map[string]any) (map[string]any, float64, bool) {
	var items []struct {
		Code     string  `json:"code"`
		Name     string  `json:"name"`
		Weight   float64 `json:"weight"`
		Required bool    `json:"required"`
	}
	_ = json.Unmarshal(itemsJSON, &items)
	results := map[string]any{}
	total := 0.0
	missing := false
	for _, item := range items {
		score, ok := responses[item.Code].(float64)
		if !ok {
			if item.Required {
				missing = true
			}
			score = 0
		}
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		weighted := score * item.Weight / 100
		total += weighted
		results[item.Code] = map[string]any{"name": item.Name, "score": score, "weight": item.Weight, "weightedScore": weighted, "required": item.Required}
	}
	return results, total, missing
}
