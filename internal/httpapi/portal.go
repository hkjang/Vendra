package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hkjang/Vendra/internal/security"
)

func (a *App) portalProfile(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	s, err := scanSupplier(a.db.QueryRow(r.Context(), supplierSelect+` WHERE id=$1 AND deleted_at IS NULL`, *p.SupplierID))
	if err != nil {
		writeError(w, 404, "not_found", "공급업체를 찾을 수 없습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"supplier": redactSupplier(p, s), "user": p})
}

func (a *App) portalUpdateProfile(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	_, err = a.db.Exec(r.Context(), `UPDATE suppliers SET phone=COALESCE(NULLIF($2,''),phone),email=COALESCE(NULLIF($3,''),email),website=COALESCE(NULLIF($4,''),website),updated_at=now() WHERE id=$1`, *p.SupplierID, stringValue(in, "phone"), stringValue(in, "email"), stringValue(in, "website"))
	if err != nil {
		writeError(w, 400, "save_failed", "업체 정보를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "portal_update", "supplier", *p.SupplierID, nil, in)
	writeJSON(w, 200, map[string]any{"ok": true, "notice": "계좌정보와 법적 정보 변경은 내부 승인이 필요합니다"})
}

func (a *App) portalContacts(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',c.id,'name',c.name,'title',c.title,'department',c.department,'email',c.email,'phone',c.phone,'primary',c.primary_contact,'emailVerified',CASE WHEN c.email IS NULL THEN false ELSE EXISTS(SELECT 1 FROM email_verifications e WHERE lower(e.email)=lower(c.email) AND e.verified_at IS NOT NULL) END,'createdAt',c.created_at) FROM supplier_contacts c WHERE c.supplier_id=$1 ORDER BY c.primary_contact DESC,c.name`, *p.SupplierID)
	if err != nil {
		writeError(w, 500, "database_error", "담당자를 조회하지 못했습니다")
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

func (a *App) portalCreateContact(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	in, err := decodeMap(r)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if stringValue(in, "name") == "" {
		writeError(w, 400, "validation_error", "담당자 이름은 필수입니다")
		return
	}
	primary, _ := in["primary"].(bool)
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO supplier_contacts(supplier_id,name,title,department,email,phone,primary_contact) VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),NULLIF(lower($5),''),NULLIF($6,''),$7) RETURNING id`, *p.SupplierID, stringValue(in, "name"), stringValue(in, "title"), stringValue(in, "department"), stringValue(in, "email"), stringValue(in, "phone"), primary).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "담당자를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "portal_create", "supplier_contact", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id, "emailVerified": false})
}

func (a *App) portalRequestContactVerification(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	var email string
	if err := a.db.QueryRow(r.Context(), `SELECT email FROM supplier_contacts WHERE id=$1 AND supplier_id=$2 AND email IS NOT NULL`, r.PathValue("id"), *p.SupplierID).Scan(&email); err != nil {
		writeError(w, 404, "not_found", "인증할 담당자 이메일을 찾을 수 없습니다")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "token_error", "인증 링크를 만들지 못했습니다")
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO email_verifications(user_id,email,token_hash,expires_at) VALUES((SELECT id FROM users WHERE lower(email)=lower($1) LIMIT 1),lower($1),$2,now()+interval '24 hours')`, email, security.TokenHash(token))
	if err != nil {
		writeError(w, 500, "save_failed", "인증 요청을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "request_email_verification", "supplier_contact", r.PathValue("id"), nil, map[string]any{"email": email})
	writeJSON(w, 201, map[string]any{"verificationUrl": "/api/auth/verify-email?token=" + token, "expiresInHours": 24, "notice": "알림 Adapter 또는 사내 메일로 링크를 전달하세요"})
}

func (a *App) verifyEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		writeError(w, 400, "token_required", "인증 토큰이 필요합니다")
		return
	}
	var email string
	err := a.db.QueryRow(r.Context(), `UPDATE email_verifications SET verified_at=COALESCE(verified_at,now()) WHERE token_hash=$1 AND expires_at>now() RETURNING email`, security.TokenHash(token)).Scan(&email)
	if err != nil {
		writeError(w, 400, "invalid_token", "인증 링크가 유효하지 않거나 만료되었습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"verified": true, "email": email})
}

func (a *App) portalWork(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	rows, err := a.db.Query(r.Context(), objectSelect+` WHERE o.supplier_id=$1 AND o.deleted_at IS NULL AND o.object_type IN('rfq','rfp','contract','purchase_order','delivery','invoice','issue') ORDER BY o.updated_at DESC`, *p.SupplierID)
	if err != nil {
		writeError(w, 500, "database_error", "업무를 조회하지 못했습니다")
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

func (a *App) portalEvaluations(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "supplier" || p.SupplierID == nil {
		writeError(w, 403, "portal_scope", "공급업체 계정이 아닙니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',e.id,'evaluationType',e.evaluation_type,'periodStart',e.period_start,'periodEnd',e.period_end,'totalScore',e.total_score,'grade',e.grade,'comments',e.comments,'templateName',t.name,'completedAt',e.updated_at) FROM evaluations e LEFT JOIN scorecard_templates t ON t.id=e.template_id WHERE e.supplier_id=$1 AND e.status='completed' ORDER BY e.updated_at DESC`, *p.SupplierID)
	if err != nil {
		writeError(w, 500, "database_error", "평가 결과를 조회하지 못했습니다")
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

func (a *App) createInvitation(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Email         string `json:"email"`
		SupplierID    string `json:"supplierId"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Email == "" {
		writeError(w, 400, "validation_error", "이메일은 필수입니다")
		return
	}
	if in.ExpiresInDays <= 0 {
		in.ExpiresInDays = 7
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "token_error", "초대 링크를 만들지 못했습니다")
		return
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO invitations(email,supplier_id,token_hash,expires_at,invited_by) VALUES(lower($1),NULLIF($2,'')::uuid,$3,now()+make_interval(days=>$4),$5) RETURNING id`, in.Email, in.SupplierID, security.TokenHash(token), in.ExpiresInDays, p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "초대를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "invitation", id, nil, map[string]any{"email": in.Email, "supplierId": in.SupplierID})
	writeJSON(w, 201, map[string]any{"id": id, "invitationUrl": "/register?token=" + token, "expiresAt": time.Now().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour), "notice": "오프라인 환경에서는 이 링크를 사내 메일 또는 메신저로 전달하세요"})
}

func (a *App) registerSupplierUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token          string `json:"token"`
		DisplayName    string `json:"displayName"`
		Password       string `json:"password"`
		SupplierName   string `json:"supplierName"`
		BusinessNumber string `json:"businessNumber"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	// Check the policy before touching the database, but defer the expensive
	// bcrypt hash until the invitation is known to be valid.
	if err := a.passwordPolicy(r.Context()).validate(in.Password); err != nil {
		writePasswordError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "가입을 처리하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	var invitationID, email string
	var supplierID *string
	err = tx.QueryRow(r.Context(), `SELECT id,email,supplier_id FROM invitations WHERE token_hash=$1 AND expires_at>now() AND accepted_at IS NULL FOR UPDATE`, security.TokenHash(in.Token)).Scan(&invitationID, &email, &supplierID)
	if err != nil {
		writeError(w, 400, "invalid_invitation", "초대가 유효하지 않거나 만료되었습니다")
		return
	}
	if supplierID == nil {
		if in.SupplierName == "" || in.BusinessNumber == "" {
			writeError(w, 400, "validation_error", "업체명과 사업자번호는 필수입니다")
			return
		}
		number := "SUP-" + strings.ToUpper(timeNowID())
		var id string
		err = tx.QueryRow(r.Context(), `INSERT INTO suppliers(supplier_number,name,business_number,status,email) VALUES($1,$2,$3,'registration',$4) RETURNING id`, number, in.SupplierName, in.BusinessNumber, email).Scan(&id)
		supplierID = &id
	}
	var userID string
	if err == nil {
		var hash string
		if hash, err = a.hashPassword(r.Context(), in.Password); err != nil {
			writePasswordError(w, err)
			return
		}
		err = tx.QueryRow(r.Context(), `INSERT INTO users(email,display_name,password_hash,user_type,supplier_id,status) VALUES($1,$2,$3,'supplier',$4,'active') RETURNING id`, email, in.DisplayName, hash, *supplierID).Scan(&userID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='supplier_user'`, userID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE invitations SET accepted_at=now(),supplier_id=$2 WHERE id=$1`, invitationID, *supplierID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO email_verifications(user_id,email,token_hash,expires_at,verified_at) VALUES($1,lower($2),$3,now(),now()) ON CONFLICT(token_hash) DO UPDATE SET user_id=excluded.user_id,email=excluded.email,verified_at=now()`, userID, email, security.TokenHash(in.Token))
	}
	if err != nil {
		writeError(w, 400, "registration_failed", "가입을 완료하지 못했습니다")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "가입을 완료하지 못했습니다")
		return
	}
	auditRequest := r.WithContext(context.WithValue(r.Context(), principalKey, Principal{ID: userID, Email: email, DisplayName: in.DisplayName, UserType: "supplier", SupplierID: supplierID}))
	a.audit.record(auditRequest, "self_register", "supplier", *supplierID, nil, map[string]any{"email": email, "userId": userID})
	writeJSON(w, 201, map[string]any{"ok": true, "supplierId": *supplierID})
}
