package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hkjang/Vendra/internal/security"
)

func (a *App) listSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT key,value,secret,secret_value IS NOT NULL,category,updated_at FROM settings ORDER BY category,key`)
	if err != nil {
		writeError(w, 500, "database_error", "설정을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var key, category string
		var value []byte
		var secret, configured bool
		var updated any
		if err := rows.Scan(&key, &value, &secret, &configured, &category, &updated); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "설정을 조회하지 못했습니다")
			return
		}
		var v any
		_ = json.Unmarshal(value, &v)
		items = append(items, map[string]any{"key": key, "value": v, "secret": secret, "secretConfigured": configured, "category": category, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "설정을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) putSetting(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	key := r.PathValue("key")
	var in struct {
		Value       any     `json:"value"`
		SecretValue *string `json:"secretValue"`
		Category    string  `json:"category"`
		Secret      bool    `json:"secret"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(key) == "" {
		writeError(w, 400, "validation_error", "설정 키는 필수입니다")
		return
	}
	var cipher any
	if in.SecretValue != nil && *in.SecretValue != "" {
		v, err := a.vault.Encrypt(*in.SecretValue)
		if err != nil {
			writeError(w, 500, "encryption_error", "비밀 값을 암호화하지 못했습니다")
			return
		}
		cipher = v
	}
	if in.Value == nil {
		in.Value = map[string]any{}
	}
	_, err := a.db.Exec(r.Context(), `INSERT INTO settings(key,value,secret_value,secret,category,updated_by,updated_at) VALUES($1,$2,$3,$4,COALESCE(NULLIF($5,''),'general'),$6,now()) ON CONFLICT(key) DO UPDATE SET value=excluded.value,secret_value=COALESCE(excluded.secret_value,settings.secret_value),secret=excluded.secret,category=excluded.category,updated_by=excluded.updated_by,updated_at=now()`, key, raw(in.Value), cipher, in.Secret, in.Category, p.ID)
	if err != nil {
		writeError(w, 400, "save_failed", "설정을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "setting", key, nil, map[string]any{"value": in.Value, "secretChanged": cipher != nil, "category": in.Category})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) listUsers(w http.ResponseWriter, r *http.Request) {
	// The list is bounded and searchable: an organisation with thousands of
	// accounts would otherwise serialise all of them into a single response.
	limit := parseLimit(r, 200)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := a.db.Query(r.Context(), `SELECT u.id,u.email,u.display_name,u.user_type,u.status,u.organization_id,u.supplier_id,u.locale,u.timezone,u.last_login_at,u.created_at,COALESCE(jsonb_agg(jsonb_build_object('id',ro.id,'code',ro.code,'name',ro.name)) FILTER(WHERE ro.id IS NOT NULL),'[]') FROM users u LEFT JOIN user_roles ur ON ur.user_id=u.id LEFT JOIN roles ro ON ro.id=ur.role_id WHERE ($1='' OR u.email ILIKE '%'||$1||'%' OR u.display_name ILIKE '%'||$1||'%') GROUP BY u.id ORDER BY u.created_at DESC LIMIT $2`, query, limit+1)
	if err != nil {
		writeError(w, 500, "database_error", "사용자를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, email, name, typ, status, locale, tz string
		var org, supplier *string
		var last any
		var created any
		var roles []byte
		if err := rows.Scan(&id, &email, &name, &typ, &status, &org, &supplier, &locale, &tz, &last, &created, &roles); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "사용자를 조회하지 못했습니다")
			return
		}
		var rs any
		_ = json.Unmarshal(roles, &rs)
		items = append(items, map[string]any{"id": id, "email": email, "displayName": name, "userType": typ, "status": status, "organizationId": org, "supplierId": supplier, "locale": locale, "timezone": tz, "lastLoginAt": last, "createdAt": created, "roles": rs})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "사용자를 조회하지 못했습니다")
		return
	}
	// One extra row was requested so the caller can be told the list is cut off
	// rather than silently believing it saw everyone.
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "truncated": truncated})
}

func (a *App) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email          string   `json:"email"`
		DisplayName    string   `json:"displayName"`
		Password       string   `json:"password"`
		UserType       string   `json:"userType"`
		Status         string   `json:"status"`
		OrganizationID string   `json:"organizationId"`
		SupplierID     string   `json:"supplierId"`
		RoleCodes      []string `json:"roleCodes"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Email == "" || in.DisplayName == "" {
		writeError(w, 400, "validation_error", "이메일과 이름은 필수입니다")
		return
	}
	// The display name is the byline on every audit line, approval step and
	// notification the account ever produces.
	if !validText(w, in.Email, "이메일") || !validText(w, in.DisplayName, "이름") ||
		!validText(w, in.UserType, "사용자 구분") || !validText(w, in.Status, "상태") {
		return
	}
	var hash any
	if in.Password != "" {
		b, err := a.hashPassword(r.Context(), in.Password)
		if err != nil {
			writePasswordError(w, err)
			return
		}
		hash = b
	}
	if in.UserType == "" {
		in.UserType = "internal"
	}
	if in.Status == "" {
		in.Status = "active"
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "사용자를 저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO users(email,display_name,password_hash,user_type,status,organization_id,supplier_id) VALUES(lower($1),$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid) RETURNING id`, in.Email, in.DisplayName, hash, in.UserType, in.Status, in.OrganizationID, in.SupplierID).Scan(&id)
	if err == nil && len(in.RoleCodes) > 0 {
		_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=ANY($2) ON CONFLICT DO NOTHING`, id, in.RoleCodes)
	}
	if err != nil {
		writeError(w, 400, "save_failed", "사용자를 저장하지 못했습니다")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "사용자를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "user", id, nil, map[string]any{"email": in.Email, "userType": in.UserType, "roles": in.RoleCodes})
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		DisplayName    string    `json:"displayName"`
		Status         string    `json:"status"`
		RoleCodes      *[]string `json:"roleCodes"`
		OrganizationID string    `json:"organizationId"`
		SupplierID     string    `json:"supplierId"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !validText(w, in.DisplayName, "이름") || !validText(w, in.Status, "상태") {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE users SET display_name=COALESCE(NULLIF($2,''),display_name),status=COALESCE(NULLIF($3,''),status),organization_id=COALESCE(NULLIF($4,'')::uuid,organization_id),supplier_id=COALESCE(NULLIF($5,'')::uuid,supplier_id),updated_at=now() WHERE id=$1`, id, in.DisplayName, in.Status, in.OrganizationID, in.SupplierID)
	if err == nil && in.RoleCodes != nil {
		_, err = tx.Exec(r.Context(), `DELETE FROM user_roles WHERE user_id=$1`, id)
		if err == nil {
			_, err = tx.Exec(r.Context(), `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code=ANY($2)`, id, *in.RoleCodes)
		}
	}
	if err != nil {
		writeError(w, 400, "save_failed", "사용자를 저장하지 못했습니다")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "사용자를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "user", id, nil, in)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) listRoles(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 500)
	rows, err := a.db.Query(r.Context(), `SELECT id,code,name,permissions,data_scope,system,created_at FROM roles ORDER BY system DESC,name LIMIT $1`, limit+1)
	if err != nil {
		writeError(w, 500, "database_error", "역할을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, code, name, scope string
		var perms []byte
		var system bool
		var created any
		if err := rows.Scan(&id, &code, &name, &perms, &scope, &system, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "역할을 조회하지 못했습니다")
			return
		}
		var p any
		_ = json.Unmarshal(perms, &p)
		items = append(items, map[string]any{"id": id, "code": code, "name": name, "permissions": p, "dataScope": scope, "system": system, "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "역할을 조회하지 못했습니다")
		return
	}
	items, truncated := truncate(items, limit)
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "truncated": truncated})
}

func (a *App) createRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code        string   `json:"code"`
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
		DataScope   string   `json:"dataScope"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Code == "" || in.Name == "" {
		writeError(w, 400, "validation_error", "역할 코드와 이름은 필수입니다")
		return
	}
	if !validText(w, in.Code, "역할 코드") || !validText(w, in.Name, "역할 이름") ||
		!validText(w, in.DataScope, "데이터 범위") {
		return
	}
	if in.DataScope == "" {
		in.DataScope = "own"
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO roles(code,name,permissions,data_scope) VALUES($1,$2,$3,$4) RETURNING id`, in.Code, in.Name, raw(in.Permissions), in.DataScope).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "역할을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "role", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) updateRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
		DataScope   string   `json:"dataScope"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if !validText(w, in.Name, "역할 이름") || !validText(w, in.DataScope, "데이터 범위") {
		return
	}
	_, err := a.db.Exec(r.Context(), `UPDATE roles SET name=COALESCE(NULLIF($2,''),name),permissions=CASE WHEN $3::jsonb='null'::jsonb THEN permissions ELSE $3 END,data_scope=COALESCE(NULLIF($4,''),data_scope) WHERE id=$1 AND system=false`, r.PathValue("id"), in.Name, raw(in.Permissions), in.DataScope)
	if err != nil {
		writeError(w, 400, "save_failed", "역할을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "role", r.PathValue("id"), nil, in)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) listOrganizations(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 500)
	rows, err := a.db.Query(r.Context(), `SELECT id,name,parent_id,path,created_at FROM organizations ORDER BY path,name LIMIT $1`, limit+1)
	if err != nil {
		writeError(w, 500, "database_error", "조직을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, path string
		var parent *string
		var created any
		if err := rows.Scan(&id, &name, &parent, &path, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "조직을 조회하지 못했습니다")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "parentId": parent, "path": path, "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "조직을 조회하지 못했습니다")
		return
	}
	items, truncated := truncate(items, limit)
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "truncated": truncated})
}

func (a *App) createOrganization(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, 400, "validation_error", "조직 이름은 필수입니다")
		return
	}
	// The organisation tree is a picker on every form and the grouping key of
	// the spend report; one unreadable node is in front of everybody.
	if !validText(w, in.Name, "조직 이름") {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "조직을 저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	parentPath := "/"
	if in.ParentID != "" {
		if err = tx.QueryRow(r.Context(), `SELECT path||id||'/' FROM organizations WHERE id=$1`, in.ParentID).Scan(&parentPath); err != nil {
			writeError(w, 400, "invalid_parent", "상위 조직을 찾을 수 없습니다")
			return
		}
	}
	var id string
	err = tx.QueryRow(r.Context(), `INSERT INTO organizations(name,parent_id,path) VALUES($1,NULLIF($2,'')::uuid,$3) RETURNING id`, in.Name, in.ParentID, parentPath).Scan(&id)
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, 400, "save_failed", "조직을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "organization", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) listAccessGrants(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 500)
	userID, ok := uuidParam(w, r, "userId", "사용자 ID")
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT g.id,g.user_id,u.email,g.permission,g.resource_type,g.resource_id,g.conditions,g.valid_from,g.valid_until,g.delegated_by,g.created_at FROM access_grants g JOIN users u ON u.id=g.user_id WHERE ($1='' OR g.user_id=$1::uuid) ORDER BY g.created_at DESC LIMIT $2`, userID, limit+1)
	if err != nil {
		writeError(w, 500, "database_error", "임시 권한을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, uid, email, permission string
		var typ, rid, delegator *string
		var conditions []byte
		var from, until, created any
		if err := rows.Scan(&id, &uid, &email, &permission, &typ, &rid, &conditions, &from, &until, &delegator, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "임시 권한을 조회하지 못했습니다")
			return
		}
		var c any
		_ = json.Unmarshal(conditions, &c)
		items = append(items, map[string]any{"id": id, "userId": uid, "email": email, "permission": permission, "resourceType": typ, "resourceId": rid, "conditions": c, "validFrom": from, "validUntil": until, "delegatedBy": delegator, "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "임시 권한을 조회하지 못했습니다")
		return
	}
	items, truncated := truncate(items, limit)
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "truncated": truncated})
}

func (a *App) createAccessGrant(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		UserID       string `json:"userId"`
		Permission   string `json:"permission"`
		ResourceType string `json:"resourceType"`
		ResourceID   string `json:"resourceId"`
		Conditions   any    `json:"conditions"`
		ValidFrom    string `json:"validFrom"`
		ValidUntil   string `json:"validUntil"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.UserID == "" || in.Permission == "" {
		writeError(w, 400, "validation_error", "사용자와 권한은 필수입니다")
		return
	}
	if in.ResourceID != "" && in.ResourceType == "" {
		writeError(w, 400, "validation_error", "리소스 ID를 지정할 때는 리소스 유형이 필요합니다")
		return
	}
	conditions := map[string]any{}
	if in.Conditions != nil {
		var ok bool
		conditions, ok = in.Conditions.(map[string]any)
		if !ok || !grantConditionsValid(conditions) {
			writeError(w, 400, "validation_error", "지원하지 않는 임시 권한 조건입니다")
			return
		}
	}
	var id string
	if !validInstant(w, in.ValidFrom, "시작 시각") || !validInstant(w, in.ValidUntil, "종료 시각") {
		return
	}
	err := a.db.QueryRow(r.Context(), `INSERT INTO access_grants(user_id,permission,resource_type,resource_id,conditions,valid_from,valid_until,delegated_by) VALUES($1,$2,NULLIF($3,''),NULLIF($4,'')::uuid,$5,COALESCE(NULLIF($6,'')::timestamptz,now()),NULLIF($7,'')::timestamptz,$8) RETURNING id`, in.UserID, in.Permission, in.ResourceType, in.ResourceID, raw(conditions), in.ValidFrom, in.ValidUntil, p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "임시 권한을 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "grant", "access_grant", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) deleteAccessGrant(w http.ResponseWriter, r *http.Request) {
	tag, err := a.db.Exec(r.Context(), `DELETE FROM access_grants WHERE id=$1`, r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "임시 권한을 찾을 수 없습니다")
		return
	}
	a.audit.record(r, "revoke", "access_grant", r.PathValue("id"), nil, nil)
	w.WriteHeader(204)
}

func (a *App) listLifecycle(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT id,entity_type,code,name,color,sort_order,terminal,enabled FROM lifecycle_states ORDER BY entity_type,sort_order`)
	if err != nil {
		writeError(w, 500, "database_error", "상태를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, typ, code, name, color string
		var order int
		var terminal, enabled bool
		if err := rows.Scan(&id, &typ, &code, &name, &color, &order, &terminal, &enabled); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "상태를 조회하지 못했습니다")
			return
		}
		items = append(items, map[string]any{"id": id, "entityType": typ, "code": code, "name": name, "color": color, "sortOrder": order, "terminal": terminal, "enabled": enabled})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "상태를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) putLifecycle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []struct {
			Code      string `json:"code"`
			Name      string `json:"name"`
			Color     string `json:"color"`
			SortOrder int    `json:"sortOrder"`
			Terminal  bool   `json:"terminal"`
			Enabled   bool   `json:"enabled"`
		} `json:"items"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	typ := r.PathValue("entityType")
	// The editor saves the whole set at once. Skipping an unusable row while
	// committing the rest reported success for a save that only partly happened:
	// clearing a state's display name left it with its old one, and nothing said
	// so. Check every row before opening the transaction.
	for _, item := range in.Items {
		if strings.TrimSpace(item.Code) == "" {
			writeError(w, 400, "validation_error", "상태 코드는 필수입니다")
			return
		}
		if strings.TrimSpace(item.Name) == "" {
			writeError(w, 400, "validation_error", "상태 표시명은 필수입니다: "+item.Code)
			return
		}
		if utf8.RuneCountInString(item.Code) > maxIdentifierLen || utf8.RuneCountInString(item.Name) > maxIdentifierLen {
			writeError(w, 400, "validation_error", "상태 코드와 표시명이 너무 깁니다")
			return
		}
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	for _, item := range in.Items {
		_, err = tx.Exec(r.Context(), `INSERT INTO lifecycle_states(entity_type,code,name,color,sort_order,terminal,enabled) VALUES($1,$2,$3,COALESCE(NULLIF($4,''),'#64748b'),$5,$6,$7) ON CONFLICT(entity_type,code) DO UPDATE SET name=excluded.name,color=excluded.color,sort_order=excluded.sort_order,terminal=excluded.terminal,enabled=excluded.enabled`, typ, item.Code, item.Name, item.Color, item.SortOrder, item.Terminal, item.Enabled)
		if err != nil {
			writeError(w, 400, "save_failed", "상태를 저장하지 못했습니다")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "상태를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "lifecycle", typ, nil, in)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) listAudit(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 200)
	objectType := r.URL.Query().Get("objectType")
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	// An audit entry carries the whole record, before and after. Without this,
	// the trail was a way around every other boundary: a reader whose contract
	// list correctly returned nothing for another division's contract could
	// read that contract's amount, its title and its change history here.
	//
	// Company scope still sees everything, which is what an auditor is for and
	// what both seeded roles holding audit.read carry. Anything narrower sees
	// the trail of the records it can reach. Entries about users, settings and
	// tool calls are not records in a department at all, so they need the
	// company view — a partial trail that pretended to be complete would be
	// worse than none.
	scoped := `($3='company'
	 OR EXISTS(SELECT 1 FROM business_objects o WHERE o.id::text=a.object_id
	   AND (` + orgInScope("o.organization_id", "$3", "$4") + ` OR ($3='own' AND o.owner_id=$5::uuid)))
	 OR EXISTS(SELECT 1 FROM suppliers s WHERE s.id::text=a.object_id
	   AND (` + orgInScope("s.organization_id", "$3", "$4") + ` OR ($3='own' AND s.owner_id=$5::uuid))))`
	rows, err := a.db.Query(r.Context(), `SELECT jsonb_build_object('id',a.id,'occurredAt',a.occurred_at,'actor',a.actor_email,'actorEmail',a.actor_email,'action',a.action,'objectType',a.object_type,'objectId',a.object_id,'previousValue',a.previous_value,'newValue',a.new_value,'ip',a.ip,'sessionId',a.session_id,'requestId',a.request_id) FROM audit_logs a WHERE ($1='' OR a.object_type=$1) AND `+scoped+` ORDER BY a.occurred_at DESC LIMIT $2`, objectType, limit+1, p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "감사로그를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "감사로그를 조회하지 못했습니다")
			return
		}
		var item map[string]any
		if json.Unmarshal(encoded, &item) == nil {
			items = append(items, item)
		}
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "감사로그를 조회하지 못했습니다")
		return
	}
	items, truncated := truncate(items, limit)
	writeJSON(w, 200, map[string]any{"items": items, "limit": limit, "truncated": truncated})
}

func (a *App) listServerLogs(w http.ResponseWriter, r *http.Request) {
	if a.logs == nil {
		writeJSON(w, 200, map[string]any{"items": []any{}, "stats": map[string]int{}, "capacity": 0})
		return
	}
	items, stats := a.logs.Query(r.URL.Query().Get("level"), r.URL.Query().Get("query"), parseLimit(r, 200))
	writeJSON(w, 200, map[string]any{
		"items":       items,
		"stats":       stats,
		"capacity":    a.logs.Capacity(),
		"startedAt":   a.logs.StartedAt(),
		"generatedAt": time.Now().UTC(),
	})
}

func (a *App) listScorecards(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT id,name,evaluation_type,active,criteria,grade_rules,created_at,updated_at FROM scorecard_templates ORDER BY active DESC,name`)
	if err != nil {
		writeError(w, 500, "database_error", "평가표를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, typ string
		var active bool
		var c, g []byte
		var created, updated any
		if err := rows.Scan(&id, &name, &typ, &active, &c, &g, &created, &updated); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "평가표를 조회하지 못했습니다")
			return
		}
		var criteria, grades any
		_ = json.Unmarshal(c, &criteria)
		_ = json.Unmarshal(g, &grades)
		items = append(items, map[string]any{"id": id, "name": name, "evaluationType": typ, "active": active, "criteria": criteria, "gradeRules": grades, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "평가표를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createScorecard(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string `json:"name"`
		EvaluationType string `json:"evaluationType"`
		Criteria       any    `json:"criteria"`
		GradeRules     any    `json:"gradeRules"`
		Active         bool   `json:"active"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Name == "" {
		writeError(w, 400, "validation_error", "평가표 이름은 필수입니다")
		return
	}
	if !validText(w, in.Name, "평가표 이름") || !validText(w, in.EvaluationType, "평가 구분") {
		return
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO scorecard_templates(name,evaluation_type,active,criteria,grade_rules) VALUES($1,$2,$3,$4,$5) RETURNING id`, in.Name, in.EvaluationType, in.Active, raw(in.Criteria), raw(in.GradeRules)).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "평가표를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "scorecard", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rows, err := a.db.Query(r.Context(), `SELECT id,name,prefix,scopes,expires_at,last_used_at,revoked_at,created_at FROM api_keys WHERE user_id=$1 ORDER BY created_at DESC`, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "키를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, prefix string
		var scopes []byte
		var expires, last, revoked any
		var created any
		if err := rows.Scan(&id, &name, &prefix, &scopes, &expires, &last, &revoked, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "키를 조회하지 못했습니다")
			return
		}
		var s any
		_ = json.Unmarshal(scopes, &s)
		items = append(items, map[string]any{"id": id, "name": name, "prefix": prefix, "scopes": s, "expiresAt": expires, "lastUsedAt": last, "revokedAt": revoked, "createdAt": created})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "키를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expiresInDays"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Name == "" {
		writeError(w, 400, "validation_error", "키 이름은 필수입니다")
		return
	}
	if len(in.Scopes) == 0 {
		if hasPermission(p, "*.read") {
			in.Scopes = []string{"*.read"}
		} else {
			for _, permission := range p.Permissions {
				if strings.HasSuffix(permission, ".read") {
					in.Scopes = append(in.Scopes, permission)
				}
			}
		}
	}
	if len(in.Scopes) == 0 {
		writeError(w, 400, "validation_error", "API 키에 부여할 읽기 권한이 없습니다")
		return
	}
	seen := map[string]bool{}
	scopes := make([]string, 0, len(in.Scopes))
	for _, scope := range in.Scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || !hasPermission(p, scope) {
			writeError(w, 403, "scope_escalation", "현재 사용자 권한을 초과하는 API 키 scope는 부여할 수 없습니다")
			return
		}
		if !seen[scope] {
			seen[scope] = true
			scopes = append(scopes, scope)
		}
	}
	in.Scopes = scopes
	if in.ExpiresInDays < 1 || in.ExpiresInDays > 365 {
		writeError(w, 400, "validation_error", "API 키 만료 기간은 1일 이상 365일 이하여야 합니다")
		return
	}
	// The name is how the key is told apart from the others in the list it is
	// revoked and rotated from, and it survives the key itself in the audit.
	if !validText(w, in.Name, "키 이름") {
		return
	}
	tokenPart, err := randomToken(32)
	if err != nil {
		writeError(w, 500, "key_error", "키를 생성하지 못했습니다")
		return
	}
	token := "vnd_" + tokenPart
	prefix := token[:12]
	var id string
	err = a.db.QueryRow(r.Context(), `INSERT INTO api_keys(user_id,name,prefix,key_hash,scopes,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, p.ID, in.Name, prefix, securityTokenHash(token), raw(in.Scopes), expiry(in.ExpiresInDays)).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "키를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "api_key", id, nil, map[string]any{"name": in.Name, "scopes": in.Scopes})
	writeJSON(w, 201, map[string]any{"id": id, "key": token, "prefix": prefix, "notice": "이 키는 다시 표시되지 않습니다"})
}

func (a *App) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	oldID := r.PathValue("id")
	var name string
	var scopes []byte
	var expires *time.Time
	if err := a.db.QueryRow(r.Context(), `SELECT name,scopes,expires_at FROM api_keys WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, oldID, p.ID).Scan(&name, &scopes, &expires); err != nil {
		writeError(w, 404, "not_found", "활성 키를 찾을 수 없습니다")
		return
	}
	part, _ := randomToken(32)
	token := "vnd_" + part
	prefix := token[:12]
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "키를 회전하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `UPDATE api_keys SET revoked_at=now() WHERE id=$1`, oldID)
	var id string
	if err == nil {
		err = tx.QueryRow(r.Context(), `INSERT INTO api_keys(user_id,name,prefix,key_hash,scopes,expires_at,rotated_from) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, p.ID, name, prefix, securityTokenHash(token), scopes, expires, oldID).Scan(&id)
	}
	if err != nil {
		writeError(w, 500, "save_failed", "키를 회전하지 못했습니다")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "save_failed", "키를 회전하지 못했습니다")
		return
	}
	a.audit.record(r, "rotate", "api_key", id, map[string]any{"oldId": oldID}, nil)
	writeJSON(w, 201, map[string]any{"id": id, "key": token, "prefix": prefix, "notice": "기존 키는 즉시 폐기되었습니다. 새 키는 다시 표시되지 않습니다"})
}

func (a *App) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tag, err := a.db.Exec(r.Context(), `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, r.PathValue("id"), p.ID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "활성 키를 찾을 수 없습니다")
		return
	}
	a.audit.record(r, "revoke", "api_key", r.PathValue("id"), nil, nil)
	w.WriteHeader(204)
}

func securityTokenHash(token string) []byte { return security.TokenHash(token) }
