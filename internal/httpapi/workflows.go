package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (a *App) listWorkflows(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT id,name,object_type,enabled,conditions,steps,version,created_by,created_at,updated_at FROM workflow_definitions ORDER BY object_type,name,version DESC`)
	if err != nil {
		writeError(w, 500, "database_error", "워크플로를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, typ string
		var enabled bool
		var conditions, steps []byte
		var version int
		var creator *string
		var created, updated any
		if rows.Scan(&id, &name, &typ, &enabled, &conditions, &steps, &version, &creator, &created, &updated) == nil {
			var c, s any
			_ = json.Unmarshal(conditions, &c)
			_ = json.Unmarshal(steps, &s)
			items = append(items, map[string]any{"id": id, "name": name, "objectType": typ, "enabled": enabled, "conditions": c, "steps": s, "version": version, "createdBy": creator, "createdAt": created, "updatedAt": updated})
		}
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (a *App) createWorkflow(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Name       string           `json:"name"`
		ObjectType string           `json:"objectType"`
		Enabled    bool             `json:"enabled"`
		Conditions any              `json:"conditions"`
		Steps      []map[string]any `json:"steps"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Name == "" || in.ObjectType == "" || len(in.Steps) == 0 {
		writeError(w, 400, "validation_error", "이름, 대상 유형, 승인 단계가 필요합니다")
		return
	}
	for i, step := range in.Steps {
		if strings.TrimSpace(stringValue(step, "name")) == "" {
			writeError(w, 400, "validation_error", "각 승인 단계에는 이름이 필요합니다")
			return
		}
		step["order"] = i
	}
	var id string
	err := a.db.QueryRow(r.Context(), `INSERT INTO workflow_definitions(name,object_type,enabled,conditions,steps,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, in.Name, in.ObjectType, in.Enabled, raw(in.Conditions), raw(in.Steps), p.ID).Scan(&id)
	if err != nil {
		writeError(w, 400, "save_failed", "워크플로를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "create", "workflow", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		Enabled    *bool  `json:"enabled"`
		Conditions any    `json:"conditions"`
		Steps      any    `json:"steps"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	var currentEnabled bool
	var currentConditions, currentSteps []byte
	if err := a.db.QueryRow(r.Context(), `SELECT enabled,conditions,steps FROM workflow_definitions WHERE id=$1`, r.PathValue("id")).Scan(&currentEnabled, &currentConditions, &currentSteps); err != nil {
		writeError(w, 404, "not_found", "워크플로를 찾을 수 없습니다")
		return
	}
	if in.Enabled == nil {
		in.Enabled = &currentEnabled
	}
	conditions := json.RawMessage(currentConditions)
	if in.Conditions != nil {
		conditions = raw(in.Conditions)
	}
	steps := json.RawMessage(currentSteps)
	if in.Steps != nil {
		steps = raw(in.Steps)
	}
	_, err := a.db.Exec(r.Context(), `UPDATE workflow_definitions SET name=COALESCE(NULLIF($2,''),name),enabled=$3,conditions=$4,steps=$5,version=version+1,updated_at=now() WHERE id=$1`, r.PathValue("id"), in.Name, *in.Enabled, conditions, steps)
	if err != nil {
		writeError(w, 400, "save_failed", "워크플로를 저장하지 못했습니다")
		return
	}
	a.audit.record(r, "update", "workflow", r.PathValue("id"), nil, in)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) listApprovals(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,i.object_type,i.object_id,i.status,i.current_step,i.context,i.requested_by,i.created_at,d.name,d.steps,o.number,o.title,o.amount,s.name FROM workflow_instances i JOIN workflow_definitions d ON d.id=i.definition_id LEFT JOIN business_objects o ON o.id=i.object_id LEFT JOIN suppliers s ON s.id=o.supplier_id WHERE i.status='pending' AND (vendra_org_in_scope(o.organization_id,$1,NULLIF($2,'')::uuid) OR ($1='own' AND (o.owner_id=$3::uuid OR i.requested_by=$3::uuid))) ORDER BY i.created_at`, p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "승인함을 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, typ, obj, status string
		var step int
		var context, steps []byte
		var requester *string
		var created any
		var workflow string
		var number, title, supplier *string
		var amount *float64
		if rows.Scan(&id, &typ, &obj, &status, &step, &context, &requester, &created, &workflow, &steps, &number, &title, &amount, &supplier) != nil {
			continue
		}
		var stepList []map[string]any
		_ = json.Unmarshal(steps, &stepList)
		if step >= len(stepList) {
			continue
		}
		current := stepList[step]
		role, _ := current["role"].(string)
		if role != "" && !principalHasRole(r.Context(), a, p.ID, role) && !hasPermission(p, "*") {
			continue
		}
		var c any
		_ = json.Unmarshal(context, &c)
		items = append(items, map[string]any{"id": id, "objectType": typ, "objectId": obj, "status": status, "currentStep": step, "currentStepDefinition": current, "context": c, "requestedBy": requester, "createdAt": created, "workflowName": workflow, "number": number, "title": title, "amount": amount, "supplierName": supplier})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func principalHasRole(ctx context.Context, a *App, userID, role string) bool {
	var ok bool
	_ = a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1 AND r.code=$2)`, userID, role).Scan(&ok)
	return ok
}

func (a *App) workflowAction(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	id := r.PathValue("id")
	var in struct {
		Action  string `json:"action"`
		Comment string `json:"comment"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if in.Action != "approve" && in.Action != "reject" && in.Action != "return" {
		writeError(w, 400, "validation_error", "approve, reject, return 중 하나를 선택하세요")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "승인을 처리하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	var current int
	var status, objectType, objectID string
	var steps []byte
	var objectOwnerID, objectOrganizationID *string
	err = tx.QueryRow(r.Context(), `SELECT i.current_step,i.status,i.object_type,i.object_id,d.steps,o.owner_id,o.organization_id FROM workflow_instances i JOIN workflow_definitions d ON d.id=i.definition_id LEFT JOIN business_objects o ON o.id=i.object_id WHERE i.id=$1 FOR UPDATE OF i`, id).Scan(&current, &status, &objectType, &objectID, &steps, &objectOwnerID, &objectOrganizationID)
	if err == pgx.ErrNoRows || status != "pending" {
		writeError(w, 409, "not_pending", "이미 처리된 승인입니다")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "승인을 처리하지 못했습니다")
		return
	}
	if p.DataScope != "company" {
		allowed := p.DataScope == "own" && objectOwnerID != nil && *objectOwnerID == p.ID
		if (p.DataScope == "department" || p.DataScope == "division") && p.OrganizationID != nil && objectOrganizationID != nil {
			_ = tx.QueryRow(r.Context(), `SELECT vendra_org_in_scope($1,$2,$3)`, *objectOrganizationID, p.DataScope, *p.OrganizationID).Scan(&allowed)
		}
		if !allowed {
			writeError(w, 403, "data_scope", "승인 데이터 접근 범위를 벗어났습니다")
			return
		}
	}
	var stepList []map[string]any
	_ = json.Unmarshal(steps, &stepList)
	if current >= len(stepList) {
		writeError(w, 409, "invalid_workflow", "워크플로 단계가 올바르지 않습니다")
		return
	}
	role, _ := stepList[current]["role"].(string)
	if role != "" && !principalHasRole(r.Context(), a, p.ID, role) && !hasPermission(p, "*") {
		writeError(w, 403, "forbidden", "현재 단계의 승인 권한이 없습니다")
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO workflow_actions(instance_id,step,action,actor_id,comment) VALUES($1,$2,$3,$4,NULLIF($5,''))`, id, current, in.Action, p.ID, in.Comment)
	nextStatus := "pending"
	objectStatus := "pending_approval"
	completed := false
	if in.Action == "reject" {
		nextStatus = "rejected"
		objectStatus = "rejected"
		completed = true
	} else if in.Action == "return" {
		nextStatus = "returned"
		objectStatus = "returned"
		completed = true
	} else if current+1 >= len(stepList) {
		nextStatus = "approved"
		objectStatus = "approved"
		completed = true
	}
	if err == nil {
		if completed {
			_, err = tx.Exec(r.Context(), `UPDATE workflow_instances SET status=$2,completed_at=now() WHERE id=$1`, id, nextStatus)
		} else {
			_, err = tx.Exec(r.Context(), `UPDATE workflow_instances SET current_step=current_step+1 WHERE id=$1`, id)
		}
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), `UPDATE business_objects SET status=$2,updated_at=now() WHERE id=$1`, objectID, objectStatus)
	}
	if err == nil && objectStatus == "approved" && objectType == "supplier_bank_change" {
		_, err = tx.Exec(r.Context(), `UPDATE suppliers s SET bank_account_encrypted=o.data->>'bankAccountCipher',updated_at=now() FROM business_objects o WHERE o.id=$1 AND s.id=o.supplier_id`, objectID)
	}
	if err != nil {
		writeError(w, 500, "save_failed", "승인을 처리하지 못했습니다")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "save_failed", "승인을 처리하지 못했습니다")
		return
	}
	a.audit.record(r, in.Action, objectType, objectID, nil, map[string]any{"workflowInstanceId": id, "step": current, "comment": in.Comment})
	writeJSON(w, 200, map[string]any{"status": nextStatus, "currentStep": current})
}
