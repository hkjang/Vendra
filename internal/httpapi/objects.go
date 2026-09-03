package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

type objectRoute struct{ path, objectType string }

var objectRoutes = []objectRoute{
	{"/api/v1/contracts", "contract"},
	{"/api/v1/purchase-requests", "purchase_request"},
	{"/api/v1/rfq", "rfq"}, {"/api/v1/rfp", "rfp"},
	{"/api/v1/purchase-orders", "purchase_order"},
	{"/api/v1/deliveries", "delivery"}, {"/api/v1/inspections", "inspection"},
	{"/api/v1/quality", "quality"}, {"/api/v1/issues", "issue"},
	{"/api/v1/invoices", "invoice"},
	{"/api/v1/payments", "payment"},
}

type businessObject struct {
	ID             string         `json:"id"`
	ObjectType     string         `json:"objectType"`
	Number         string         `json:"number"`
	SupplierID     *string        `json:"supplierId,omitempty"`
	SupplierName   *string        `json:"supplierName,omitempty"`
	ParentID       *string        `json:"parentId,omitempty"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	Amount         *float64       `json:"amount,omitempty"`
	Currency       string         `json:"currency"`
	OwnerID        *string        `json:"ownerId,omitempty"`
	OrganizationID *string        `json:"organizationId,omitempty"`
	StartDate      *string        `json:"startDate,omitempty"`
	DueDate        *string        `json:"dueDate,omitempty"`
	EndDate        *string        `json:"endDate,omitempty"`
	RiskLevel      *string        `json:"riskLevel,omitempty"`
	Score          *float64       `json:"score,omitempty"`
	Data           map[string]any `json:"data"`
	CreatedAt      string         `json:"createdAt"`
	UpdatedAt      string         `json:"updatedAt"`
}

const objectSelect = `SELECT o.id,o.object_type,o.number,o.supplier_id,s.name,o.parent_id,o.title,o.status,o.amount,o.currency,o.owner_id,o.organization_id,
	to_char(o.start_date,'YYYY-MM-DD'),to_char(o.due_date,'YYYY-MM-DD'),to_char(o.end_date,'YYYY-MM-DD'),o.risk_level,o.score,o.data,
	to_char(o.created_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),to_char(o.updated_at,'YYYY-MM-DD"T"HH24:MI:SSOF') FROM business_objects o LEFT JOIN suppliers s ON s.id=o.supplier_id`

func scanObject(row pgx.Row) (businessObject, error) {
	var o businessObject
	var data []byte
	err := row.Scan(&o.ID, &o.ObjectType, &o.Number, &o.SupplierID, &o.SupplierName, &o.ParentID, &o.Title, &o.Status, &o.Amount, &o.Currency, &o.OwnerID, &o.OrganizationID, &o.StartDate, &o.DueDate, &o.EndDate, &o.RiskLevel, &o.Score, &data, &o.CreatedAt, &o.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal(data, &o.Data)
	}
	return o, err
}

// truncate reports whether more rows exist than the caller asked for. Queries
// fetch limit+1 so the extra row answers the question without a second count;
// it is dropped before the response goes out. A list that silently stops at its
// limit reads as "this is everything", which is worse than no limit at all.
func truncate[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

func (a *App) listObjects(objectType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalFrom(r.Context())
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		supplierID, ok := uuidParam(w, r, "supplierId", "공급업체 ID")
		if !ok {
			return
		}
		order := strings.TrimSpace(r.URL.Query().Get("order"))
		limit := parseLimit(r, 100)
		organizationID := ""
		if p.OrganizationID != nil {
			organizationID = *p.OrganizationID
		}
		orderBy := objectOrderBy(order, hasPermission(p, objectType+".amount.read"))
		query := objectSelect + ` WHERE o.object_type=$1 AND o.deleted_at IS NULL AND ($2='' OR o.status=$2) AND ($3='' OR o.supplier_id=$3::uuid) AND ($4='' OR o.title ILIKE '%'||$4||'%' OR o.number ILIKE '%'||$4||'%') AND (` + orgInScope("o.organization_id", "$6", "$7") + ` OR ($6='own' AND o.owner_id=$8::uuid)) ORDER BY ` + orderBy + ` LIMIT $5`
		rows, err := a.db.Query(r.Context(), query, objectType, status, supplierID, q, limit+1, p.DataScope, organizationID, p.ID)
		if err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "목록을 조회하지 못했습니다")
			return
		}
		defer rows.Close()
		items := []businessObject{}
		for rows.Next() {
			o, e := scanObject(rows)
			if e != nil {
				logDB(e)
				continue
			}
			items = append(items, redactObject(p, o))
		}
		if err := rows.Err(); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "목록을 조회하지 못했습니다")
			return
		}
		items, truncated := truncate(items, limit)
		writeJSON(w, 200, map[string]any{"items": items, "count": len(items), "limit": limit, "truncated": truncated})
	}
}

func objectOrderBy(order string, amountVisible bool) string {
	switch order {
	case "due_asc":
		return "COALESCE(o.due_date,o.end_date) ASC NULLS LAST, o.updated_at DESC"
	case "amount_desc":
		if amountVisible {
			return "o.amount DESC NULLS LAST, o.updated_at DESC"
		}
	case "title_asc":
		return "o.title ASC, o.updated_at DESC"
	}
	return "o.updated_at DESC"
}

// objectScopeAllowed checks the organisation and supplier a caller is trying to
// attach a record to. Reads are scoped by query, but writes took whatever the
// client supplied: a department user could file a purchase order against another
// organisation, where it entered that organisation's lists, dashboards and
// approval routing while staying invisible to its author. Document upload
// already performed this check; business objects did not.
func (a *App) objectScopeAllowed(r *http.Request, organizationID, supplierID string) bool {
	p, _ := principalFrom(r.Context())
	if grantAuthorized(r.Context()) || p.DataScope == "company" {
		return true
	}
	if organizationID != "" {
		if p.OrganizationID == nil {
			return false
		}
		var allowed bool
		if a.db.QueryRow(r.Context(), `SELECT vendra_org_in_scope($1::uuid,$2,$3::uuid)`, organizationID, p.DataScope, *p.OrganizationID).Scan(&allowed) != nil || !allowed {
			return false
		}
	}
	return supplierID == "" || a.supplierScopeAllowed(r, supplierID)
}

// pendingApproval reports the approval already in flight for an object, if any.
func (a *App) pendingApproval(ctx context.Context, objectID string) (string, bool) {
	var id string
	if a.db.QueryRow(ctx, `SELECT id FROM workflow_instances WHERE object_id=$1 AND status='pending' ORDER BY created_at LIMIT 1`, objectID).Scan(&id) != nil {
		return "", false
	}
	return id, true
}

func writeAlreadySubmitted(w http.ResponseWriter, instanceID string) {
	writeJSON(w, 200, map[string]any{"status": "pending_approval", "workflowApplied": true, "instanceId": instanceID, "alreadySubmitted": true})
}

// workflowOwnedStatus lists the states the approval workflow awards.
// submitObject moves an object to pending_approval, and workflowAction settles
// it as approved, rejected or returned. Nothing else may write them: an object
// that names one for itself never reaches the approvers its amount was supposed
// to route to, and one already sitting in an inbox can be walked past them while
// the instance still reads pending.
var workflowOwnedStatus = map[string]bool{
	"pending_approval": true, "approved": true, "rejected": true, "returned": true,
}

// refusedWorkflowStatus reports whether the request claims a workflow-owned
// status the object does not already hold. Restating the current status is a
// no-op and stays allowed; passing none at all leaves it untouched.
func refusedWorkflowStatus(w http.ResponseWriter, in map[string]any, current string) bool {
	status := stringValue(in, "status")
	if status == "" || status == current || !workflowOwnedStatus[status] {
		return false
	}
	writeError(w, 400, "validation_error", "결재 상태는 승인 절차로만 바뀝니다")
	return true
}

func (a *App) createObject(objectType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalFrom(r.Context())
		in, err := decodeMap(r)
		if err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return
		}
		title, _ := in["title"].(string)
		if strings.TrimSpace(title) == "" {
			writeError(w, 400, "validation_error", "제목은 필수입니다")
			return
		}
		// Ahead of the scope check: objectScopeAllowed selects the organisation
		// and the supplier and treats a failed query as a denial, so a typo in
		// either was answered as 403 "데이터 접근 범위를 벗어난 조직 또는
		// 공급업체입니다" for a department account and as 400 "데이터를 저장하지
		// 못했습니다" for a company one — two different wrong answers to the
		// same mistake, neither naming the field.
		if !validUUIDFields(w, in, objectUUIDFields...) {
			return
		}
		if stringValue(in, "organizationId") == "" && p.OrganizationID != nil {
			in["organizationId"] = *p.OrganizationID
		}
		if !a.objectScopeAllowed(r, stringValue(in, "organizationId"), stringValue(in, "supplierId")) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 조직 또는 공급업체입니다")
			return
		}
		if refusedWorkflowStatus(w, in, "") {
			return
		}
		number, _ := in["number"].(string)
		if number == "" {
			number = objectNumber(objectType)
		}
		data, _ := in["data"].(map[string]any)
		if data == nil {
			data = map[string]any{}
		}
		var id string
		if !validDateFields(w, in, dateField{"startDate", "시작일"}, dateField{"dueDate", "마감일"}, dateField{"endDate", "종료일"}) {
			return
		}
		if !validNumberFields(w, in, amountField("amount", "금액"), objectScoreField) {
			return
		}
		if !validTextFields(w, in, objectTextFields...) {
			return
		}
		if !validEnumFields(w, in, riskGradeField("riskLevel", "리스크 등급")) {
			return
		}
		err = a.db.QueryRow(r.Context(), `INSERT INTO business_objects(object_type,number,supplier_id,parent_id,title,status,amount,currency,owner_id,organization_id,start_date,due_date,end_date,risk_level,score,data,created_by)
	 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,COALESCE(NULLIF($6,''),'draft'),$7,COALESCE(NULLIF($8,''),'KRW'),COALESCE(NULLIF($9,''),$16)::uuid,COALESCE(NULLIF($10,'')::uuid,(SELECT organization_id FROM suppliers WHERE id=NULLIF($3,'')::uuid)),NULLIF($11,'')::date,NULLIF($12,'')::date,NULLIF($13,'')::date,NULLIF($14,''),$15,$17,$16::uuid) RETURNING id`,
			objectType, number, stringValue(in, "supplierId"), stringValue(in, "parentId"), title, stringValue(in, "status"), numberValue(in, "amount"), stringValue(in, "currency"), stringValue(in, "ownerId"), stringValue(in, "organizationId"), stringValue(in, "startDate"), stringValue(in, "dueDate"), stringValue(in, "endDate"), stringValue(in, "riskLevel"), numberValue(in, "score"), p.ID, raw(data)).Scan(&id)
		if err != nil {
			logDB(err)
			writeError(w, 400, "save_failed", "데이터를 저장하지 못했습니다")
			return
		}
		a.audit.record(r, "create", objectType, id, nil, in)
		o, err := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1`, id))
		if err != nil {
			writeJSON(w, 201, map[string]any{"id": id})
			return
		}
		writeJSON(w, 201, redactObject(p, o))
	}
}

func (a *App) getObject(objectType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, err := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1 AND o.object_type=$2 AND o.deleted_at IS NULL`, r.PathValue("id"), objectType))
		if err == pgx.ErrNoRows {
			writeError(w, 404, "not_found", "대상을 찾을 수 없습니다")
			return
		}
		if err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "조회하지 못했습니다")
			return
		}
		p, _ := principalFrom(r.Context())
		if !a.canAccessObject(r.Context(), p, o) && !grantAuthorized(r.Context()) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
			return
		}
		writeJSON(w, 200, redactObject(p, o))
	}
}

func canAccessObject(p Principal, o businessObject) bool {
	if p.DataScope == "company" {
		return true
	}
	if p.DataScope == "department" || p.DataScope == "division" {
		return p.OrganizationID != nil && o.OrganizationID != nil && *p.OrganizationID == *o.OrganizationID
	}
	return o.OwnerID != nil && *o.OwnerID == p.ID
}

func (a *App) canAccessObject(ctx context.Context, p Principal, o businessObject) bool {
	if p.DataScope != "division" {
		return canAccessObject(p, o)
	}
	if p.OrganizationID == nil || o.OrganizationID == nil {
		return false
	}
	var allowed bool
	_ = a.db.QueryRow(ctx, `SELECT vendra_org_in_scope($1,'division',$2)`, *o.OrganizationID, *p.OrganizationID).Scan(&allowed)
	return allowed
}

// supplierVisibleData is the part of a business object's detail blob that
// belongs to the supplier the object names.
//
// The portal returned the blob whole, and the buyer's own form writes their
// position into it: a purchase order carried budget — the ceiling, above what
// the supplier was actually awarded — alongside anything typed into the free
// notes. The supplier read it through their own work list.
//
// An allowlist, for the same reason as the tender one: a denylist would leak
// whatever field somebody adds to the buyer's form next.
//
// rootCause and capa are deliberately not here. An improvement request is
// shared with the supplier — the portal has a tab for it — so they could
// belong; but nothing in the form marked 원인분석 (RCA) tells the person
// filling it in that the supplier will read it, and one seeded with an
// internal judgement said the supplier was being lined up for replacement.
// Ambiguity in what to send resolves toward not sending. If they are meant to
// be shared, adding them back is one line and the form should say so.
func supplierVisibleData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	shared := []string{
		"description",
		"category", "purchasePurpose",
		"item", "quantity", "unit", "unitPrice",
		"deliveryLocation", "desiredDate", "deliveredAt",
		"paymentTerms", "contractType", "autoRenewal",
		"sla", "warranty",
		"issueType", "severity",
		"invoiceNumber", "taxInvoiceNumber",
	}
	out := map[string]any{}
	for _, key := range shared {
		if v, ok := data[key]; ok {
			out[key] = v
		}
	}
	return out
}

func redactObject(p Principal, o businessObject) businessObject {
	if !hasPermission(p, o.ObjectType+".amount.read") {
		o.Amount = nil
	}
	return o
}

func (a *App) updateObject(objectType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		before, err := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1 AND o.object_type=$2 AND o.deleted_at IS NULL`, id, objectType))
		if err != nil {
			writeError(w, 404, "not_found", "대상을 찾을 수 없습니다")
			return
		}
		p, _ := principalFrom(r.Context())
		if !a.canAccessObject(r.Context(), p, before) && !grantAuthorized(r.Context()) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
			return
		}
		in, err := decodeMap(r)
		if err != nil {
			writeError(w, 400, "invalid_request", err.Error())
			return
		}
		// Only supplierId: the update statement is the one id it rewrites, and
		// refusing a request over a field it does not read would be a new way
		// to fail rather than a clearer one.
		if !validUUIDFields(w, in, uuidField{"supplierId", "공급업체 ID"}) {
			return
		}
		if supplierID := stringValue(in, "supplierId"); supplierID != "" && !a.objectScopeAllowed(r, "", supplierID) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어난 공급업체입니다")
			return
		}
		if refusedWorkflowStatus(w, in, before.Status) {
			return
		}
		data := before.Data
		if d, ok := in["data"].(map[string]any); ok {
			data = d
		}
		if !validDateFields(w, in, dateField{"startDate", "시작일"}, dateField{"dueDate", "마감일"}, dateField{"endDate", "종료일"}) {
			return
		}
		if !validNumberFields(w, in, amountField("amount", "금액"), objectScoreField) {
			return
		}
		if !validTextFields(w, in, objectTextFields...) {
			return
		}
		if !validEnumFields(w, in, riskGradeField("riskLevel", "리스크 등급")) {
			return
		}
		_, err = a.db.Exec(r.Context(), `UPDATE business_objects SET title=COALESCE(NULLIF($3,''),title),status=COALESCE(NULLIF($4,''),status),supplier_id=COALESCE(NULLIF($5,'')::uuid,supplier_id),amount=COALESCE($6,amount),start_date=COALESCE(NULLIF($7,'')::date,start_date),due_date=COALESCE(NULLIF($8,'')::date,due_date),end_date=COALESCE(NULLIF($9,'')::date,end_date),risk_level=COALESCE(NULLIF($10,''),risk_level),score=COALESCE($11,score),data=$12,updated_at=now() WHERE id=$1 AND object_type=$2`, id, objectType, stringValue(in, "title"), stringValue(in, "status"), stringValue(in, "supplierId"), numberValue(in, "amount"), stringValue(in, "startDate"), stringValue(in, "dueDate"), stringValue(in, "endDate"), stringValue(in, "riskLevel"), numberValue(in, "score"), raw(data))
		if err != nil {
			logDB(err)
			writeError(w, 400, "save_failed", "데이터를 저장하지 못했습니다")
			return
		}
		a.audit.record(r, "update", objectType, id, before, in)
		o, _ := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1`, id))
		writeJSON(w, 200, redactObject(p, o))
	}
}

func (a *App) submitObject(objectType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		current, scopeErr := scanObject(a.db.QueryRow(r.Context(), objectSelect+` WHERE o.id=$1 AND o.object_type=$2 AND o.deleted_at IS NULL`, id, objectType))
		if scopeErr != nil {
			writeError(w, 404, "not_found", "대상을 찾을 수 없습니다")
			return
		}
		p, _ := principalFrom(r.Context())
		if !a.canAccessObject(r.Context(), p, current) && !grantAuthorized(r.Context()) {
			writeError(w, 403, "data_scope", "데이터 접근 범위를 벗어났습니다")
			return
		}
		enabled, err := a.boolSetting(r.Context(), `SELECT `+jsonBoolSetting("value", false)+` FROM settings WHERE key='workflow.approval_enabled'`, false)
		if err != nil {
			logDB(err)
			writeControlUnavailable(w)
			return
		}
		if !enabled {
			_, err := a.db.Exec(r.Context(), `UPDATE business_objects SET status='approved',updated_at=now() WHERE id=$1 AND object_type=$2`, id, objectType)
			if err != nil {
				writeError(w, 500, "save_failed", "처리하지 못했습니다")
				return
			}
			a.audit.record(r, "auto_approve", objectType, id, nil, map[string]any{"workflowEnabled": false})
			writeJSON(w, 200, map[string]any{"status": "approved", "workflowApplied": false})
			return
		}
		// Submitting again while an approval is already running used to open a
		// second one: each showed separately in approvers' inboxes, and clearing
		// one left the others pending against a request that had already moved.
		// This read is only the fast path — a partial unique index on
		// workflow_instances is what actually guarantees one, because concurrent
		// submits can all pass a check that happens before the insert.
		if openInstance, found := a.pendingApproval(r.Context(), id); found {
			writeAlreadySubmitted(w, openInstance)
			return
		}
		definitionID, steps, err := a.matchingWorkflow(r, objectType, current)
		if errors.Is(err, pgx.ErrNoRows) {
			if _, err := a.db.Exec(r.Context(), `UPDATE business_objects SET status='approved',updated_at=now() WHERE id=$1`, id); err != nil {
				logDB(err)
				writeError(w, 500, "save_failed", "처리하지 못했습니다")
				return
			}
			writeJSON(w, 200, map[string]any{"status": "approved", "workflowApplied": false, "reason": "no_matching_workflow"})
			return
		}
		// Any other failure left definitionID empty, which would go on to
		// violate the foreign key and be reported as a workflow problem.
		if err != nil {
			logDB(err)
			writeError(w, 500, "workflow_failed", "승인 절차를 시작하지 못했습니다")
			return
		}
		var instanceID string
		err = a.db.QueryRow(r.Context(), `INSERT INTO workflow_instances(definition_id,object_type,object_id,requested_by,context) VALUES($1,$2,$3,$4,$5) RETURNING id`, definitionID, objectType, id, p.ID, raw(map[string]any{"steps": json.RawMessage(steps)})).Scan(&instanceID)
		if err != nil {
			// Losing the race to another submit is the expected outcome, not a
			// failure: report the approval that won.
			if openInstance, found := a.pendingApproval(r.Context(), id); found {
				writeAlreadySubmitted(w, openInstance)
				return
			}
			logDB(err)
			writeError(w, 500, "workflow_failed", "승인 절차를 시작하지 못했습니다")
			return
		}
		// The instance already exists and the partial unique index will refuse a
		// second one, so leaving the object in draft would strand it: approvers
		// see a request the object does not know about, and the author cannot
		// resubmit.
		if _, err := a.db.Exec(r.Context(), `UPDATE business_objects SET status='pending_approval',updated_at=now() WHERE id=$1`, id); err != nil {
			logDB(err)
			if _, cleanup := a.db.Exec(r.Context(), `DELETE FROM workflow_instances WHERE id=$1 AND status='pending'`, instanceID); cleanup != nil {
				logDB(cleanup)
			}
			writeError(w, 500, "workflow_failed", "승인 절차를 시작하지 못했습니다")
			return
		}
		a.audit.record(r, "submit", objectType, id, nil, map[string]any{"workflowInstanceId": instanceID})
		writeJSON(w, 200, map[string]any{"status": "pending_approval", "workflowApplied": true, "instanceId": instanceID})
	}
}

type workflowConditions struct {
	MinAmount      *float64 `json:"minAmount"`
	MaxAmount      *float64 `json:"maxAmount"`
	OrganizationID string   `json:"organizationId"`
	RiskLevel      string   `json:"riskLevel"`
	RiskLevels     []string `json:"riskLevels"`
	ContractType   string   `json:"contractType"`
	Category       string   `json:"category"`
	Project        string   `json:"project"`
	SecurityLevel  string   `json:"securityLevel"`
}

// validWorkflowConditions checks the rule an approval is routed by, on the way
// in rather than at submit time.
//
// Two things could be stored here that only ever fail later. A grade outside
// riskGrades matches no object, so the rule it was written for never fires and
// the submission it was meant to hold falls through to the next definition or
// to none at all — approved on the spot, with `no_matching_workflow` in the
// response and nothing in the inbox. And a condition that is not the shape this
// struct reads — minAmount as "1000", conditions as a string — fails
// json.Unmarshal in matchingWorkflow, which every submit of that object type
// then answers 500 to, permanently, for everyone.
func validWorkflowConditions(w http.ResponseWriter, conditions any) bool {
	if conditions == nil {
		return true
	}
	encoded, err := json.Marshal(conditions)
	if err == nil {
		var parsed workflowConditions
		err = json.Unmarshal(encoded, &parsed)
		if err == nil {
			field := riskGradeField("riskLevel", "리스크 조건")
			if !validEnum(w, parsed.RiskLevel, field) {
				return false
			}
			for _, level := range parsed.RiskLevels {
				if !validEnum(w, level, field) {
					return false
				}
			}
			return true
		}
	}
	writeError(w, 400, "validation_error", "승인 조건 형식이 올바르지 않습니다")
	return false
}

func (a *App) matchingWorkflow(r *http.Request, objectType string, object businessObject) (string, []byte, error) {
	rows, err := a.db.Query(r.Context(), `SELECT id,steps,conditions FROM workflow_definitions WHERE object_type=$1 AND enabled=true ORDER BY version DESC,updated_at DESC`, objectType)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var steps, rawConditions []byte
		if err := rows.Scan(&id, &steps, &rawConditions); err != nil {
			return "", nil, err
		}
		var conditions workflowConditions
		if err := json.Unmarshal(rawConditions, &conditions); err != nil {
			return "", nil, err
		}
		if workflowMatches(conditions, object) {
			return id, steps, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return "", nil, pgx.ErrNoRows
}

func workflowMatches(c workflowConditions, o businessObject) bool {
	amount := 0.0
	if o.Amount != nil {
		amount = *o.Amount
	}
	if c.MinAmount != nil && amount < *c.MinAmount {
		return false
	}
	if c.MaxAmount != nil && amount > *c.MaxAmount {
		return false
	}
	if c.OrganizationID != "" && (o.OrganizationID == nil || *o.OrganizationID != c.OrganizationID) {
		return false
	}
	risk := ""
	if o.RiskLevel != nil {
		risk = *o.RiskLevel
	}
	if c.RiskLevel != "" && risk != c.RiskLevel {
		return false
	}
	if len(c.RiskLevels) > 0 && !containsString(c.RiskLevels, risk) {
		return false
	}
	for key, wanted := range map[string]string{"contractType": c.ContractType, "category": c.Category, "project": c.Project, "securityLevel": c.SecurityLevel} {
		if wanted != "" {
			got, ok := o.Data[key].(string)
			if !ok || got != wanted {
				return false
			}
		}
	}
	return true
}

func containsString(values []string, wanted string) bool {
	for _, v := range values {
		if v == wanted {
			return true
		}
	}
	return false
}

func parseLimit(r *http.Request, def int) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 {
		return def
	}
	if n > 500 {
		return 500
	}
	return n
}

// objectScoreField is the inspection or quality score carried on a business
// object. It is the same 0~100 scale the scorecard and the bid evaluation are
// rated on, and the supplier's qualityPerformance is the average of these, so
// one object scored 99,999 reads on the Supplier 360 as a register-wide
// quality figure no supplier could earn. numeric(7,2) refused 1,000,000 and
// nothing below it.
var objectScoreField = numberField{key: "score", label: "점수", min: 0, max: 100}

// objectTextFields are the labels a business object is read by. The title is
// what every list, work inbox card, notification and audit line renders the
// record as, and the number is what the portal and every export quote it by —
// both are caller-supplied and both were unbounded, so a 20,000-character title
// pushed every other row off the screen on pages nobody had opened yet. The
// three short codes behind them drive badges and filters.
var objectTextFields = []textField{
	{"title", "제목"}, {"number", "번호"}, {"currency", "통화"},
	{"status", "상태"}, {"riskLevel", "리스크 등급"},
}

// objectUUIDFields are the four records a business object is filed against: the
// supplier it is with, the object it hangs off, the person answerable for it
// and the organisation it belongs to. Every one of them is a `$n::uuid` in the
// insert, so a typo failed the whole statement.
var objectUUIDFields = []uuidField{
	{"supplierId", "공급업체 ID"}, {"parentId", "상위 업무 ID"},
	{"ownerId", "담당자 ID"}, {"organizationId", "조직 ID"},
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
func numberValue(m map[string]any, key string) any {
	switch v := m[key].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return nil
	}
}

// objectNumberPrefix must name every type in objectRoutes; a type missing from
// it is numbered OBJ- and reads as something the application does not know
// about. TestEveryObjectTypeHasItsOwnNumberPrefix keeps the two in step.
var objectNumberPrefix = map[string]string{
	"contract": "CTR", "purchase_request": "PR", "rfq": "RFQ", "rfp": "RFP",
	"purchase_order": "PO", "delivery": "DLV", "inspection": "INS", "quality": "QLT",
	"issue": "ISS", "document_record": "DOC", "invoice": "INV", "payment": "PAY",
}

func objectNumber(t string) string {
	prefix := objectNumberPrefix[t]
	if prefix == "" {
		prefix = "OBJ"
	}
	return fmt.Sprintf("%s-%s", prefix, strings.ToUpper(strings.ReplaceAll(timeNowID(), "-", "")))
}
func timeNowID() string { token, _ := randomToken(6); return token }

// readableObjectTypes lists the business object types the principal may read,
// for queries that must filter by type in SQL rather than after a LIMIT.
func readableObjectTypes(p Principal) []string {
	types := make([]string, 0, len(objectRoutes))
	for _, route := range objectRoutes {
		if hasPermission(p, route.objectType+".read") {
			types = append(types, route.objectType)
		}
	}
	return types
}
