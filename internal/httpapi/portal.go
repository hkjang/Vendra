package httpapi

import (
	"context"
	"fmt"
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
	// The three fields the portal lets a supplier write onto their own record.
	// They are the contact block the buyer's register and Supplier 360 show, and
	// the internal door onto the same columns reads them the same way — which is
	// the point of sharing the lists rather than spelling the pair out here.
	if !validSupplierContactDetails(w, in) ||
		!validEmailFields(w, in, supplierEmailFields...) {
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
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "담당자를 조회하지 못했습니다")
		return
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
	if !validTextFields(w, in, contactTextFields...) || !validEmailFields(w, in, contactEmailFields...) ||
		!validPhoneFields(w, in, contactPhoneFields...) {
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
			// The detail blob carries the buyer's side too. Only the shared
			// part of it belongs to the supplier reading this list.
			o.Data = supplierVisibleData(o.Data)
			items = append(items, o)
		}
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "업무를 조회하지 못했습니다")
		return
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
	items, err := scanJSONRows(rows)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가 결과를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// maxInvitationDays bounds how long a registration link stays usable.
const maxInvitationDays = 30

func (a *App) createInvitation(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Email      string `json:"email"`
		SupplierID string `json:"supplierId"`
		// A pointer so an omitted field takes the default rather than reading
		// as an explicit zero.
		ExpiresInDays *int `json:"expiresInDays"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Email == "" {
		writeError(w, 400, "validation_error", "이메일은 필수입니다")
		return
	}
	// This address is where the invitation is sent, and registration copies it
	// onto both the supplier record and the portal account it creates — so a
	// typo here is not one wrong field but a supplier the buyer cannot write to
	// and an account its owner cannot sign in to, discovered weeks later.
	email, ok := validEmail(w, in.Email, "이메일")
	if !ok {
		return
	}
	in.Email = email
	// The link is a bearer credential: whoever holds it registers a portal
	// account bound to this supplier. The form offers at most 14 days, and
	// nothing bounded the API — 100000 wrote an invitation valid until the year
	// 2300, sitting in an internal mailbox long after the person who was sent it
	// left.
	expiresInDays := 7
	if in.ExpiresInDays != nil {
		expiresInDays = *in.ExpiresInDays
		if expiresInDays < 1 || expiresInDays > maxInvitationDays {
			writeError(w, 400, "validation_error", fmt.Sprintf("초대 유효기간은 1일 이상 %d일 이하여야 합니다", maxInvitationDays))
			return
		}
	}
	// Before the scope check below, which reads a malformed id as a supplier the
	// inviter may not see rather than as a typo in the one field the form has.
	if !validRecordID(w, in.SupplierID, "공급업체 ID") {
		return
	}
	// The invitation binds the future portal account to this supplier, so an
	// unchecked id grants someone access to a supplier the inviter cannot see.
	if in.SupplierID != "" && !a.supplierScopeAllowed(r, in.SupplierID) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 공급업체입니다")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "token_error", "초대 링크를 만들지 못했습니다")
		return
	}
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO invitations(email,supplier_id,token_hash,expires_at,invited_by) VALUES(lower($1),NULLIF($2,'')::uuid,$3,now()+make_interval(days=>$4),$5) RETURNING id`, in.Email, in.SupplierID, security.TokenHash(token), expiresInDays, p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "초대를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "invitation", id, nil, map[string]any{"email": in.Email, "supplierId": in.SupplierID})
	writeJSON(w, 201, map[string]any{"id": id, "invitationUrl": "/register?token=" + token, "expiresAt": time.Now().Add(time.Duration(expiresInDays) * 24 * time.Hour), "notice": "오프라인 환경에서는 이 링크를 사내 메일 또는 메신저로 전달하세요"})
}

// invitationStanding is what an invitation is at the moment it is read, written
// once so the list and the recall agree on which ones are still live. The order
// matters: an accepted invitation is spent whatever else is true of it, and a
// recalled one stays recalled after its time would have run out anyway.
const invitationStanding = `CASE WHEN accepted_at IS NOT NULL THEN 'accepted'
	 WHEN revoked_at IS NOT NULL THEN 'revoked'
	 WHEN expires_at<=now() THEN 'expired' ELSE 'pending' END`

// listInvitations says what is outstanding. Issuing a link was the whole of the
// feature: it was handed over as a one-time URL, and after the modal closed
// there was nowhere in the application that said who had been invited, when it
// runs out, or whether they ever used it — so "did that invitation go to the
// old address?" had no answer, and neither did "is it still open?".
func (a *App) listInvitations(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	supplierID := strings.TrimSpace(r.URL.Query().Get("supplierId"))
	if !validRecordID(w, supplierID, "공급업체 ID") {
		return
	}
	// Two ways to ask, and each is in scope by construction. Naming a supplier
	// makes the same check the issue endpoint makes, so the list holds exactly
	// the invitations the caller could have written. Naming none answers with
	// the ones this caller issued, which is the only way an invitation with no
	// supplier bound to it is ever visible.
	where, arg := `supplier_id=$1`, any(supplierID)
	if supplierID == "" {
		where, arg = `invited_by=$1`, any(p.ID)
	} else if !a.supplierScopeAllowed(r, supplierID) {
		writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 공급업체입니다")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,email,supplier_id,expires_at,created_at,accepted_at,revoked_at,`+invitationStanding+
		` FROM invitations WHERE `+where+` ORDER BY created_at DESC LIMIT 100`, arg)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "초대 목록을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, email, standing string
		var supplier *string
		var expires, created, accepted, revoked any
		if err := rows.Scan(&id, &email, &supplier, &expires, &created, &accepted, &revoked, &standing); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "초대 목록을 조회하지 못했습니다")
			return
		}
		items = append(items, map[string]any{"id": id, "email": email, "supplierId": supplier, "expiresAt": expires,
			"createdAt": created, "acceptedAt": accepted, "revokedAt": revoked, "status": standing})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "초대 목록을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// revokeInvitation calls a link back. The link is a bearer credential — whoever
// holds it registers a portal account bound to the supplier, and that account
// then reads the supplier's contracts, orders, deliveries and evaluations — and
// nothing but expires_at, up to 14 days out, ever ended one. An invitation sent
// to a mistyped address, or to the person who has since left that supplier,
// stayed live for those two weeks with no way to stop it.
func (a *App) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id := r.PathValue("id")
	if !validRecordID(w, id, "초대 ID") {
		return
	}
	var email string
	var supplierID, invitedBy *string
	var accepted, revoked any
	err := a.db.QueryRow(r.Context(), `SELECT email,supplier_id,invited_by,accepted_at,revoked_at FROM invitations WHERE id=$1`, id).
		Scan(&email, &supplierID, &invitedBy, &accepted, &revoked)
	if err != nil {
		writeError(w, 404, "not_found", "초대를 찾을 수 없습니다")
		return
	}
	// The same reach the list has: an invitation bound to a supplier belongs to
	// whoever can see that supplier, and an unbound one to whoever issued it.
	if supplierID != nil {
		if !a.supplierScopeAllowed(r, *supplierID) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 공급업체입니다")
			return
		}
	} else if invitedBy == nil || *invitedBy != p.ID {
		writeError(w, 403, "data_scope", "직접 발급한 초대만 회수할 수 있습니다")
		return
	}
	if accepted != nil {
		// The account exists, so there is no link left to call back and saying
		// "회수되었습니다" would be a lie about what happened to it.
		writeError(w, 409, "invitation_accepted", "이미 가입에 사용된 초대입니다. 계정 자체를 막으려면 사용자 관리에서 비활성화하세요")
		return
	}
	if revoked != nil {
		// Already where the caller is asking for it to be.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE invitations SET revoked_at=now() WHERE id=$1 AND accepted_at IS NULL AND revoked_at IS NULL`, id)
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "초대를 회수하지 못했습니다")
		return
	}
	if tag.RowsAffected() == 0 {
		// It was redeemed between the read above and this statement.
		writeError(w, 409, "invitation_accepted", "이미 가입에 사용된 초대입니다. 계정 자체를 막으려면 사용자 관리에서 비활성화하세요")
		return
	}
	a.audit.record(r, "revoke", "invitation", id, nil, map[string]any{"email": email, "supplierId": supplierID})
	w.WriteHeader(http.StatusNoContent)
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
	// The other door onto suppliers.name. createSupplier has bounded it since
	// the register existed; self-registration writes the same column and never
	// did, so the one name a stranger supplies was the one nobody measured.
	if !validText(w, in.SupplierName, "업체명") || !validText(w, in.BusinessNumber, "사업자번호") ||
		!validText(w, in.DisplayName, "이름") {
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
	// revoked_at alongside the other two ways an invitation stops working. A
	// recall that this statement did not read would be a button that reports
	// success and changes nothing about who can still sign up with the link.
	err = tx.QueryRow(r.Context(), `SELECT id,email,supplier_id FROM invitations WHERE token_hash=$1 AND expires_at>now() AND accepted_at IS NULL AND revoked_at IS NULL FOR UPDATE`, security.TokenHash(in.Token)).Scan(&invitationID, &email, &supplierID)
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
		if err != nil {
			// The company is already on file — a colleague self-registered it,
			// or the buyer typed it into the register first. Told as "가입을
			// 완료하지 못했습니다" this is unescapable: the number is correct,
			// so retrying repeats it, and the only way through the form is to
			// mistype the 사업자번호 until it is a number nobody holds, which
			// puts the company in the register twice under a wrong one.
			//
			// The company is named but not joined: an invitation is only an
			// email, and attaching this stranger to the existing record would
			// hand them its contracts, orders and evaluations. The way in is a
			// new invitation bound to that supplier, which only the buyer can
			// issue and which lands here with supplier_id already set.
			if duplicateBusinessNumber(err) {
				writeError(w, 409, "duplicate_business_number", "이미 등록된 사업자번호입니다. 담당 구매 담당자에게 기존 업체 계정으로 초대를 요청하세요")
				return
			}
			logDB(err)
			writeError(w, 400, "registration_failed", "회사 정보를 등록하지 못했습니다")
			return
		}
		supplierID = &id
	}
	var userID string
	var hash string
	if hash, err = a.hashPassword(r.Context(), in.Password); err != nil {
		writePasswordError(w, err)
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO users(email,display_name,password_hash,user_type,supplier_id,status) VALUES($1,$2,$3,'supplier',$4,'active') RETURNING id`, email, in.DisplayName, hash, *supplierID).Scan(&userID)
	if err != nil {
		// The address on the invitation already has an account. The person
		// holding the link has nothing to fix — the box the clash is in is not
		// on this form, the invitation carries it — so the only useful thing to
		// say is that they already have the account and should sign in.
		if duplicateUserEmail(err) {
			writeError(w, 409, "email_registered", "이미 가입된 이메일입니다. 기존 계정으로 로그인하세요")
			return
		}
		logDB(err)
		writeError(w, 400, "registration_failed", "가입을 완료하지 못했습니다")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='supplier_user'`, userID)
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE invitations SET accepted_at=now(),supplier_id=$2 WHERE id=$1`, invitationID, *supplierID)
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO email_verifications(user_id,email,token_hash,expires_at,verified_at) VALUES($1,lower($2),$3,now(),now()) ON CONFLICT(token_hash) DO UPDATE SET user_id=excluded.user_id,email=excluded.email,verified_at=now()`, userID, email, security.TokenHash(in.Token))
	}
	if err != nil {
		// Nothing left here is a value the registrant typed, so this is the
		// tail the caller cannot act on. It goes to the log so somebody can.
		logDB(err)
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
