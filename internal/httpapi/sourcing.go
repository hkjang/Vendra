package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (a *App) sourcingObject(r *http.Request, id string) (businessObject, error) {
	o, err := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1 AND o.object_type IN('rfq','rfp') AND o.deleted_at IS NULL`, id))
	if err != nil {
		return o, err
	}
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" && !a.canAccessObject(r.Context(), p, o) {
		return o, fmt.Errorf("data scope denied")
	}
	return o, nil
}

func (a *App) listSourcingParticipants(w http.ResponseWriter, r *http.Request) {
	if _, err := a.sourcingObject(r, r.PathValue("id")); err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',p.id,'supplierId',p.supplier_id,'supplierName',s.name,'status',p.status,'invitedAt',p.invited_at,'viewedAt',p.viewed_at,'declinedAt',p.declined_at,'declineReason',p.decline_reason) FROM sourcing_participants p JOIN suppliers s ON s.id=p.supplier_id WHERE p.sourcing_id=$1 ORDER BY p.invited_at`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "참여업체를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "참여업체를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) addSourcingParticipants(w http.ResponseWriter, r *http.Request) {
	o, err := a.sourcingObject(r, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	if o.DueDate != nil && *o.DueDate < time.Now().Format("2006-01-02") {
		writeError(w, 409, "sourcing_closed", "제출 마감일이 지났습니다")
		return
	}
	var in struct {
		SupplierIDs []string `json:"supplierIds"`
	}
	if decodeJSON(r, &in) != nil || len(in.SupplierIDs) == 0 {
		writeError(w, 400, "validation_error", "초대할 공급업체를 선택하세요")
		return
	}
	// sourcingObject checked the RFQ; the suppliers being invited need the same
	// treatment, or a buyer can pull a supplier outside their scope into their
	// tender and hand that supplier portal visibility of it.
	for _, supplierID := range in.SupplierIDs {
		if !a.supplierScopeAllowed(r, supplierID) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 공급업체입니다")
			return
		}
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "초대를 저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	count := 0
	for _, supplierID := range in.SupplierIDs {
		tag, e := tx.Exec(r.Context(), `INSERT INTO sourcing_participants(sourcing_id,supplier_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, o.ID, supplierID)
		if e != nil {
			writeError(w, 400, "save_failed", "초대를 저장하지 못했습니다")
			return
		}
		count += int(tag.RowsAffected())
	}
	if _, err = tx.Exec(r.Context(), `UPDATE business_objects SET status='open',updated_at=now() WHERE id=$1`, o.ID); err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 500, "save_failed", "초대를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "invite", o.ObjectType, o.ID, nil, map[string]any{"supplierIds": in.SupplierIDs})
	writeJSON(w, 200, map[string]any{"invited": count})
}

func (a *App) portalSourcing(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',o.id,'objectType',o.object_type,'number',o.number,'title',o.title,'status',CASE WHEN o.due_date<current_date THEN 'closed' ELSE p.status END,'dueDate',o.due_date,'data',o.data,'response',CASE WHEN sr.id IS NULL THEN NULL ELSE jsonb_build_object('id',sr.id,'status',sr.status,'currency',sr.currency,'totalAmount',sr.total_amount,'deliveryDays',sr.delivery_days,'warranty',sr.warranty,'validityDate',sr.validity_date,'commercialTerms',sr.commercial_terms,'technicalResponse',sr.technical_response,'lineItems',sr.line_items,'submittedAt',sr.submitted_at) END) FROM sourcing_participants p JOIN business_objects o ON o.id=p.sourcing_id LEFT JOIN sourcing_responses sr ON sr.sourcing_id=o.id AND sr.supplier_id=p.supplier_id WHERE p.supplier_id=$1 AND o.deleted_at IS NULL ORDER BY o.due_date NULLS LAST,o.updated_at DESC`, *p.SupplierID)
	if err != nil {
		writeError(w, 500, "database_error", "견적·입찰 요청을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "견적·입찰 요청을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) portalSourcingResponse(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	var dueDate *time.Time
	var participantStatus string
	if err := a.db.QueryRow(r.Context(), `SELECT o.due_date,p.status FROM sourcing_participants p JOIN business_objects o ON o.id=p.sourcing_id WHERE p.sourcing_id=$1 AND p.supplier_id=$2`, r.PathValue("id"), *p.SupplierID).Scan(&dueDate, &participantStatus); err != nil {
		writeError(w, 404, "not_invited", "참여 요청을 찾을 수 없습니다")
		return
	}
	if dueDate != nil && dueDate.Before(time.Now().Truncate(24*time.Hour)) {
		writeError(w, 409, "submission_closed", "제출 마감일이 지났습니다")
		return
	}
	if participantStatus == "declined" {
		writeError(w, 409, "participation_declined", "이미 참여를 거절했습니다")
		return
	}
	var in struct {
		Submit            bool     `json:"submit"`
		Currency          string   `json:"currency"`
		TotalAmount       *float64 `json:"totalAmount"`
		DeliveryDays      *int     `json:"deliveryDays"`
		Warranty          string   `json:"warranty"`
		ValidityDate      string   `json:"validityDate"`
		CommercialTerms   any      `json:"commercialTerms"`
		TechnicalResponse any      `json:"technicalResponse"`
		LineItems         any      `json:"lineItems"`
		Attachments       any      `json:"attachments"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	status := "draft"
	var submitted any
	if in.Submit {
		if in.TotalAmount == nil {
			writeError(w, 400, "validation_error", "총 견적금액은 필수입니다")
			return
		}
		status = "submitted"
		submitted = time.Now()
	}
	if in.Currency == "" {
		in.Currency = "KRW"
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO sourcing_responses(sourcing_id,supplier_id,status,currency,total_amount,delivery_days,warranty,validity_date,commercial_terms,technical_response,line_items,attachments,submitted_by,submitted_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,'')::date,$9,$10,$11,$12,$13,$14) ON CONFLICT(sourcing_id,supplier_id) DO UPDATE SET status=excluded.status,currency=excluded.currency,total_amount=excluded.total_amount,delivery_days=excluded.delivery_days,warranty=excluded.warranty,validity_date=excluded.validity_date,commercial_terms=excluded.commercial_terms,technical_response=excluded.technical_response,line_items=excluded.line_items,attachments=excluded.attachments,submitted_by=excluded.submitted_by,submitted_at=excluded.submitted_at,updated_at=now() RETURNING id`, r.PathValue("id"), *p.SupplierID, status, in.Currency, in.TotalAmount, in.DeliveryDays, in.Warranty, in.ValidityDate, raw(in.CommercialTerms), raw(in.TechnicalResponse), raw(in.LineItems), raw(in.Attachments), p.ID, submitted).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "응답을 저장하지 못했습니다")
		return
	}
	// A read receipt, not part of what the response reports.
	if _, err := a.db.Exec(r.Context(), `UPDATE sourcing_participants SET status=$3,viewed_at=COALESCE(viewed_at,now()) WHERE sourcing_id=$1 AND supplier_id=$2`, r.PathValue("id"), *p.SupplierID, status); err != nil {
		logDB(err)
	}
	a.audit.record(r, status, "sourcing_response", id, nil, map[string]any{"sourcingId": r.PathValue("id"), "supplierId": *p.SupplierID, "totalAmount": in.TotalAmount})
	if in.Submit {
		_ = a.recalculateSourcing(r, r.PathValue("id"))
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": status})
}

func (a *App) portalDeclineSourcing(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &in)
	tag, err := a.db.Exec(r.Context(), `UPDATE sourcing_participants SET status='declined',declined_at=now(),decline_reason=NULLIF($3,'') WHERE sourcing_id=$1 AND supplier_id=$2 AND status='invited'`, r.PathValue("id"), *p.SupplierID, in.Reason)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 409, "cannot_decline", "참여 요청을 거절할 수 없습니다")
		return
	}
	a.audit.record(r, "decline", "sourcing", r.PathValue("id"), nil, map[string]any{"reason": in.Reason})
	writeJSON(w, 200, map[string]any{"status": "declined"})
}

func (a *App) sourcingComparison(w http.ResponseWriter, r *http.Request) {
	if _, err := a.sourcingObject(r, r.PathValue("id")); err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	_ = a.recalculateSourcing(r, r.PathValue("id"))
	// Only submitted bids. A supplier saving without submitting keeps a draft
	// row carrying their working price and line items; showing it here handed
	// the buyer a quote the supplier had not committed to, which is the one
	// thing a sealed tender must not do. Scoring already ignored drafts, so they
	// appeared with real amounts and no scores.
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('responseId',sr.id,'supplierId',sr.supplier_id,'supplierName',s.name,'status',sr.status,'currency',sr.currency,'totalAmount',sr.total_amount,'deliveryDays',sr.delivery_days,'warranty',sr.warranty,'validityDate',sr.validity_date,'lineItems',sr.line_items,'priceScore',sr.price_score,'qualityScore',sr.quality_score,'deliveryScore',sr.delivery_score,'riskScore',sr.risk_score,'technicalScore',sr.technical_score,'finalScore',sr.final_score,'supplierRisk',s.risk_level,'supplierGrade',s.grade) FROM sourcing_responses sr JOIN suppliers s ON s.id=sr.supplier_id WHERE sr.sourcing_id=$1 AND sr.status='submitted' ORDER BY sr.final_score DESC NULLS LAST,sr.total_amount`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "비교표를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "비교표를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "selectionPolicy": "price + quality + delivery + risk + technical"})
}

func (a *App) recalculateSourcing(r *http.Request, sourcingID string) error {
	var weights struct{ Price, Quality, Delivery, Risk, Technical float64 }
	var value []byte
	if a.db.QueryRow(r.Context(), `SELECT value FROM settings WHERE key='sourcing.score_weights'`).Scan(&value) == nil {
		_ = json.Unmarshal(value, &weights)
	}
	if weights.Price+weights.Quality+weights.Delivery+weights.Risk+weights.Technical == 0 {
		weights = struct{ Price, Quality, Delivery, Risk, Technical float64 }{30, 20, 15, 15, 20}
	}
	_, err := a.db.Exec(r.Context(), `WITH base AS (SELECT sr.id,sr.total_amount,sr.delivery_days,s.score,s.risk_level,COALESCE((SELECT avg(se.total_score) FROM sourcing_evaluations se WHERE se.response_id=sr.id),50) technical,min(sr.total_amount) FILTER(WHERE sr.total_amount>0) OVER() min_amount,min(sr.delivery_days) FILTER(WHERE sr.delivery_days>0) OVER() min_days FROM sourcing_responses sr JOIN suppliers s ON s.id=sr.supplier_id WHERE sr.sourcing_id=$1 AND sr.status='submitted'),calc AS(SELECT *,CASE WHEN total_amount>0 THEN 100*min_amount/total_amount ELSE 0 END price,COALESCE(score,50) quality,CASE WHEN delivery_days>0 THEN 100.0*min_days/delivery_days ELSE 50 END delivery,CASE risk_level WHEN 'LOW' THEN 100 WHEN 'MEDIUM' THEN 70 WHEN 'HIGH' THEN 30 ELSE 0 END risk FROM base) UPDATE sourcing_responses sr SET price_score=c.price,quality_score=c.quality,delivery_score=c.delivery,risk_score=c.risk,technical_score=c.technical,final_score=(c.price*$2+c.quality*$3+c.delivery*$4+c.risk*$5+c.technical*$6)/NULLIF($2+$3+$4+$5+$6,0) FROM calc c WHERE sr.id=c.id`, sourcingID, weights.Price, weights.Quality, weights.Delivery, weights.Risk, weights.Technical)
	return err
}

func (a *App) evaluateSourcingResponse(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var committeeCount, membership int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*),count(*) FILTER(WHERE user_id=$2) FROM sourcing_committee WHERE sourcing_id=(SELECT sourcing_id FROM sourcing_responses WHERE id=$1)`, r.PathValue("id"), p.ID).Scan(&committeeCount, &membership); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가위원을 조회하지 못했습니다")
		return
	}
	if committeeCount > 0 && membership == 0 && !hasPermission(p, "*") {
		writeError(w, 403, "not_evaluator", "지정된 평가위원만 제안서를 평가할 수 있습니다")
		return
	}
	var in struct {
		Scores  map[string]float64 `json:"scores"`
		Comment string             `json:"comment"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	total := 0.0
	for _, score := range in.Scores {
		total += score
	}
	if len(in.Scores) > 0 {
		total /= float64(len(in.Scores))
	}
	var sourcingID string
	err := a.db.QueryRow(r.Context(), `INSERT INTO sourcing_evaluations(response_id,evaluator_id,scores,total_score,comment) VALUES($1,$2,$3,$4,NULLIF($5,'')) ON CONFLICT(response_id,evaluator_id) DO UPDATE SET scores=excluded.scores,total_score=excluded.total_score,comment=excluded.comment,updated_at=now() RETURNING (SELECT sourcing_id FROM sourcing_responses WHERE id=$1)`, r.PathValue("id"), p.ID, raw(in.Scores), total, in.Comment).Scan(&sourcingID)
	if err != nil {
		writeError(w, 400, "save_failed", "평가를 저장하지 못했습니다")
		return
	}
	_ = a.recalculateSourcing(r, sourcingID)
	a.audit.record(r, "evaluate", "sourcing_response", r.PathValue("id"), nil, map[string]any{"scores": in.Scores, "totalScore": total})
	writeJSON(w, 200, map[string]any{"totalScore": total})
}

func (a *App) listSourcingQuestions(w http.ResponseWriter, r *http.Request) {
	if _, err := a.sourcingObject(r, r.PathValue("id")); err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	a.writeSourcingQuestions(w, r, r.PathValue("id"), "")
}

func (a *App) writeSourcingQuestions(w http.ResponseWriter, r *http.Request, sourcingID, supplierID string) {
	// A participant-visible question carries the asker's identity. Showing it to
	// the other bidders would disclose who else was invited, so for portal
	// viewers the attribution survives only on their own questions. Buyer
	// announcements have no supplier and stay attributed.
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object(
		 'id',q.id,'sourcingId',q.sourcing_id,
		 'supplierId',CASE WHEN $2='' OR q.supplier_id IS NULL OR q.supplier_id=$2::uuid THEN q.supplier_id END,
		 'supplierName',CASE WHEN $2='' OR q.supplier_id IS NULL OR q.supplier_id=$2::uuid THEN s.name END,
		 'askedBy',CASE WHEN $2='' OR q.supplier_id IS NULL OR q.supplier_id=$2::uuid THEN u.display_name END,
		 'mine',$2<>'' AND q.supplier_id=$2::uuid,
		 'question',q.question,'answer',q.answer,'answeredBy',a.display_name,'visibility',q.visibility,'askedAt',q.asked_at,'answeredAt',q.answered_at)
		FROM sourcing_questions q JOIN users u ON u.id=q.asked_by LEFT JOIN users a ON a.id=q.answered_by LEFT JOIN suppliers s ON s.id=q.supplier_id
		WHERE q.sourcing_id=$1 AND ($2='' OR q.visibility='participants' OR (q.visibility='private' AND q.supplier_id=$2::uuid)) AND ($2='' OR q.visibility<>'internal') ORDER BY q.asked_at`, sourcingID, supplierID)
	if err != nil {
		writeError(w, 500, "database_error", "질의응답을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "질의응답을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createInternalSourcingQuestion(w http.ResponseWriter, r *http.Request) {
	o, err := a.sourcingObject(r, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	p, _ := principalFrom(r.Context())
	var in struct {
		Question   string `json:"question"`
		Visibility string `json:"visibility"`
	}
	if decodeJSON(r, &in) != nil || in.Question == "" {
		writeError(w, 400, "validation_error", "질문은 필수입니다")
		return
	}
	if in.Visibility == "" {
		in.Visibility = "participants"
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO sourcing_questions(sourcing_id,asked_by,question,visibility) VALUES($1,$2,$3,$4) RETURNING id`, o.ID, p.ID, in.Question, in.Visibility).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "질문을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "ask", "sourcing_question", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) answerSourcingQuestion(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Answer string `json:"answer"`
	}
	if decodeJSON(r, &in) != nil || in.Answer == "" {
		writeError(w, 400, "validation_error", "답변은 필수입니다")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE sourcing_questions SET answer=$2,answered_by=$3,answered_at=now() WHERE id=$1`, r.PathValue("id"), in.Answer, p.ID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "질문을 찾을 수 없습니다")
		return
	}
	a.audit.record(r, "answer", "sourcing_question", r.PathValue("id"), nil, in)
	writeJSON(w, 200, map[string]any{"answered": true})
}

func (a *App) portalSourcingQuestions(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	var invited bool
	_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sourcing_participants WHERE sourcing_id=$1 AND supplier_id=$2)`, r.PathValue("id"), *p.SupplierID).Scan(&invited)
	if !invited {
		writeError(w, 404, "not_invited", "참여 요청을 찾을 수 없습니다")
		return
	}
	a.writeSourcingQuestions(w, r, r.PathValue("id"), *p.SupplierID)
}

func (a *App) portalAskSourcingQuestion(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	var in struct {
		Question string `json:"question"`
		Private  bool   `json:"private"`
	}
	if decodeJSON(r, &in) != nil || in.Question == "" {
		writeError(w, 400, "validation_error", "질문은 필수입니다")
		return
	}
	var invited bool
	_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM sourcing_participants WHERE sourcing_id=$1 AND supplier_id=$2 AND status<>'declined')`, r.PathValue("id"), *p.SupplierID).Scan(&invited)
	if !invited {
		writeError(w, 404, "not_invited", "참여 요청을 찾을 수 없습니다")
		return
	}
	visibility := "participants"
	if in.Private {
		visibility = "private"
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO sourcing_questions(sourcing_id,supplier_id,asked_by,question,visibility) VALUES($1,$2,$3,$4,$5) RETURNING id`, r.PathValue("id"), *p.SupplierID, p.ID, in.Question, visibility).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "질문을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "portal_ask", "sourcing_question", id, nil, map[string]any{"sourcingId": r.PathValue("id"), "private": in.Private})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) listSourcingCommittee(w http.ResponseWriter, r *http.Request) {
	if _, err := a.sourcingObject(r, r.PathValue("id")); err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('userId',c.user_id,'displayName',u.display_name,'email',u.email,'role',c.role,'appointedAt',c.appointed_at) FROM sourcing_committee c JOIN users u ON u.id=c.user_id WHERE c.sourcing_id=$1 ORDER BY c.appointed_at`, r.PathValue("id"))
	if err != nil {
		writeError(w, 500, "database_error", "평가위원을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가위원을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) listSourcingCommitteeCandidates(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',u.id,'displayName',u.display_name,'email',u.email,'organizationId',u.organization_id,'roles',COALESCE(jsonb_agg(DISTINCT ro.name) FILTER(WHERE ro.id IS NOT NULL),'[]')) FROM users u LEFT JOIN user_roles ur ON ur.user_id=u.id LEFT JOIN roles ro ON ro.id=ur.role_id WHERE u.user_type='internal' AND u.status='active' AND (`+orgInScope("u.organization_id", "$1", "$2")+` OR ($1='own' AND u.id=$3)) GROUP BY u.id ORDER BY u.display_name LIMIT 500`, p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "평가위원 후보를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "평가위원 후보를 조회하지 못했습니다")
			return
		}
		var item any
		_ = json.Unmarshal(encoded, &item)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가위원 후보를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) addSourcingCommittee(w http.ResponseWriter, r *http.Request) {
	if _, err := a.sourcingObject(r, r.PathValue("id")); err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	var in struct {
		UserIDs []string `json:"userIds"`
		Role    string   `json:"role"`
	}
	if decodeJSON(r, &in) != nil || len(in.UserIDs) == 0 {
		writeError(w, 400, "validation_error", "평가위원을 선택하세요")
		return
	}
	if in.Role == "" {
		in.Role = "evaluator"
	}
	for _, userID := range in.UserIDs {
		_, err := a.db.Exec(r.Context(), `INSERT INTO sourcing_committee(sourcing_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT(sourcing_id,user_id) DO UPDATE SET role=excluded.role`, r.PathValue("id"), userID, in.Role)
		if err != nil {
			writeError(w, 400, "save_failed", "평가위원을 저장하지 못했습니다")
			return
		}
	}
	a.audit.record(r, "appoint_committee", "sourcing", r.PathValue("id"), nil, in)
	writeJSON(w, 200, map[string]any{"appointed": len(in.UserIDs)})
}

func (a *App) selectSourcingResponse(w http.ResponseWriter, r *http.Request) {
	o, err := a.sourcingObject(r, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "not_found", "RFQ/RFP를 찾을 수 없습니다")
		return
	}
	p, _ := principalFrom(r.Context())
	var in struct {
		ResponseID    string `json:"responseId"`
		SelectionType string `json:"selectionType"`
		Reason        string `json:"reason"`
	}
	if decodeJSON(r, &in) != nil || in.ResponseID == "" || (in.SelectionType != "preferred" && in.SelectionType != "final") {
		writeError(w, 400, "validation_error", "응답과 선정 유형(preferred/final)은 필수입니다")
		return
	}
	var supplierID, status string
	err = a.db.QueryRow(r.Context(), `SELECT supplier_id,status FROM sourcing_responses WHERE id=$1 AND sourcing_id=$2`, in.ResponseID, o.ID).Scan(&supplierID, &status)
	if err != nil || status != "submitted" {
		writeError(w, 409, "invalid_response", "제출 완료된 응답만 선정할 수 있습니다")
		return
	}
	// Awarding writes the selection, the request's status and every bidder's
	// standing. A partial award tells the buyer the award landed while the RFQ
	// still reads open and the losing bidders still see themselves in the
	// running, so the three writes go together or not at all.
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "선정 결과를 저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	var selectionID string
	err = tx.QueryRow(r.Context(), `INSERT INTO sourcing_selections(sourcing_id,response_id,selection_type,reason,selected_by) VALUES($1,$2,$3,NULLIF($4,''),$5) ON CONFLICT(sourcing_id,selection_type) DO UPDATE SET response_id=excluded.response_id,reason=excluded.reason,selected_by=excluded.selected_by,selected_at=now() RETURNING id`, o.ID, in.ResponseID, in.SelectionType, in.Reason, p.ID).Scan(&selectionID)
	if err != nil {
		logDB(err)
		writeError(w, 400, "save_failed", "선정 결과를 저장하지 못했습니다")
		return
	}
	objectStatus := "preferred_negotiation"
	if in.SelectionType == "final" {
		objectStatus = "selected"
	}
	_, err = tx.Exec(r.Context(), `UPDATE business_objects SET status=$2,data=data||jsonb_build_object('selectedSupplierId',$3::text,'selectionType',$4),updated_at=now() WHERE id=$1`, o.ID, objectStatus, supplierID, in.SelectionType)
	if err != nil {
		logDB(err)
		writeError(w, 500, "save_failed", "선정 결과를 저장하지 못했습니다")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE sourcing_participants SET status=CASE WHEN supplier_id=$2 THEN $3 ELSE CASE WHEN $3='selected' THEN 'not_selected' ELSE status END END WHERE sourcing_id=$1`, o.ID, supplierID, objectStatus)
	if err != nil {
		logDB(err)
		writeError(w, 500, "save_failed", "선정 결과를 저장하지 못했습니다")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		logDB(err)
		writeError(w, 500, "save_failed", "선정 결과를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "select_"+in.SelectionType, o.ObjectType, o.ID, nil, map[string]any{"selectionId": selectionID, "responseId": in.ResponseID, "supplierId": supplierID, "reason": in.Reason})
	writeJSON(w, 200, map[string]any{"selectionId": selectionID, "status": objectStatus, "supplierId": supplierID})
}

func (a *App) portalConfirmPurchaseOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE business_objects SET status='confirmed',data=jsonb_set(data,'{supplierConfirmedAt}',to_jsonb(now()::text)),updated_at=now() WHERE id=$1 AND object_type='purchase_order' AND supplier_id=$2 AND status IN('approved','sent')`, r.PathValue("id"), *p.SupplierID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 409, "cannot_confirm", "확인 가능한 발주가 아닙니다")
		return
	}
	a.audit.record(r, "confirm", "purchase_order", r.PathValue("id"), nil, map[string]any{"supplierId": *p.SupplierID})
	writeJSON(w, 200, map[string]any{"status": "confirmed"})
}

func (a *App) portalConfirmContract(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE business_objects SET data=data||jsonb_build_object('supplierAcknowledgedAt',now()::text,'supplierAcknowledgedBy',$3::text),updated_at=now() WHERE id=$1 AND object_type='contract' AND supplier_id=$2 AND status IN('approved','active','sent','executed')`, r.PathValue("id"), *p.SupplierID, p.ID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 409, "cannot_confirm", "확인 가능한 계약이 아닙니다")
		return
	}
	a.audit.record(r, "portal_acknowledge", "contract", r.PathValue("id"), nil, map[string]any{"supplierId": *p.SupplierID})
	writeJSON(w, 200, map[string]any{"acknowledged": true})
}

func (a *App) portalCreateBusinessObject(objectType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalFrom(r.Context())
		if p.SupplierID == nil {
			writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
			return
		}
		in, err := decodeMap(r)
		if err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return
		}
		title := stringValue(in, "title")
		if title == "" {
			writeError(w, 400, "validation_error", "제목은 필수입니다")
			return
		}
		number := objectNumber(objectType)
		data, _ := in["data"].(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		if objectType == "delivery" {
			data["deliveredAt"] = time.Now().UTC().Format(time.RFC3339)
		}
		var id string
		err = a.db.QueryRow(r.Context(), `INSERT INTO business_objects(object_type,number,supplier_id,parent_id,title,status,amount,currency,organization_id,due_date,data,created_by) VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,'submitted',$6,COALESCE(NULLIF($7,''),'KRW'),(SELECT organization_id FROM suppliers WHERE id=$3),NULLIF($8,'')::date,$9,$10) RETURNING id`, objectType, number, *p.SupplierID, stringValue(in, "parentId"), title, numberValue(in, "amount"), stringValue(in, "currency"), stringValue(in, "dueDate"), raw(data), p.ID).Scan(&id)
		if err != nil {
			writeError(w, 400, "save_failed", "업무를 등록하지 못했습니다")
			return
		}
		a.audit.record(r, "portal_create", objectType, id, nil, in)
		writeJSON(w, 201, map[string]any{"id": id, "number": number, "status": "submitted"})
	}
}

func scanSourcingRow(row pgx.Row) (json.RawMessage, error) {
	var b []byte
	err := row.Scan(&b)
	return b, err
}
