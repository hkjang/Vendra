package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var productivityKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9:_-]{1,120}$`)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// maxFormDraftsPerUser bounds autosave. Draft keys are client-chosen, so without
// a cap one account could keep writing new ones until the table is the database.
const maxFormDraftsPerUser = 50

type workInboxItem struct {
	Key          string  `json:"key"`
	Kind         string  `json:"kind"`
	Category     string  `json:"category"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Urgency      string  `json:"urgency"`
	TimeBucket   string  `json:"timeBucket"`
	ObjectType   string  `json:"objectType,omitempty"`
	ObjectID     string  `json:"objectId,omitempty"`
	Number       string  `json:"number,omitempty"`
	SupplierName string  `json:"supplierName,omitempty"`
	DueDate      *string `json:"dueDate,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	URL          string  `json:"url"`
	Actionable   bool    `json:"actionable"`
}

type workItemState struct {
	State        string
	SnoozedUntil *time.Time
}

func (a *App) workInbox(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.UserType != "internal" {
		writeError(w, http.StatusForbidden, "internal_only", "내부 사용자만 업무 관제탑을 사용할 수 있습니다")
		return
	}
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	items := make([]workInboxItem, 0, 64)
	if hasPermission(p, "workflow.read") {
		roles, err := principalRoleCodes(r.Context(), a, p.ID)
		if err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "승인 업무를 조회하지 못했습니다")
			return
		}
		rows, err := a.db.Query(r.Context(), `SELECT i.id,i.object_type,i.object_id,i.current_step,d.name,d.steps,
		 COALESCE(o.number,''),COALESCE(o.title,'승인 요청'),COALESCE(s.name,''),
		 to_char(COALESCE(o.due_date,(i.created_at + interval '2 days')::date),'YYYY-MM-DD'),
		 to_char(i.created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
		 FROM workflow_instances i
		 JOIN workflow_definitions d ON d.id=i.definition_id
		 LEFT JOIN business_objects o ON o.id=i.object_id
		 LEFT JOIN suppliers s ON s.id=o.supplier_id
		 WHERE i.status='pending' AND (`+orgInScope("o.organization_id", "$1", "$2")+`
		   OR ($1='own' AND (o.owner_id=$3::uuid OR i.requested_by=$3::uuid)))
		 ORDER BY i.created_at LIMIT 200`, p.DataScope, organizationID, p.ID)
		if err != nil {
			writeError(w, 500, "database_error", "승인 업무를 조회하지 못했습니다")
			return
		}
		for rows.Next() {
			var id, objectType, objectID, workflow, number, title, supplier, due, created string
			var step int
			var steps []byte
			if err := rows.Scan(&id, &objectType, &objectID, &step, &workflow, &steps, &number, &title, &supplier, &due, &created); err != nil {
				logDB(err)
				writeError(w, 500, "database_error", "승인 업무를 조회하지 못했습니다")
				return
			}
			var definitions []map[string]any
			_ = json.Unmarshal(steps, &definitions)
			if step >= len(definitions) {
				continue
			}
			role := stringValue(definitions[step], "role")
			if role != "" && !roles[role] && !hasPermission(p, "*") {
				continue
			}
			stepName := stringValue(definitions[step], "name")
			if stepName == "" {
				stepName = "승인 검토"
			}
			items = append(items, newWorkItem("approval:"+id, "approval", "approval", title, workflow+" · "+stepName, objectType, objectID, number, supplier, due, created, "/approvals?selected="+id, true))
		}
		if err := rows.Err(); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "승인 업무를 조회하지 못했습니다")
			return
		}
		rows.Close()
	}

	rows, err := a.db.Query(r.Context(), `SELECT o.id,o.object_type,o.number,o.title,COALESCE(s.name,''),
	 to_char(CASE WHEN o.object_type='contract' THEN o.end_date ELSE o.due_date END,'YYYY-MM-DD'),
	 to_char(o.created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
	 FROM business_objects o LEFT JOIN suppliers s ON s.id=o.supplier_id
	 WHERE o.deleted_at IS NULL AND o.status NOT IN('completed','closed','resolved','ended','terminated','rejected')
	 AND CASE WHEN o.object_type='contract' THEN o.end_date ELSE o.due_date END BETWEEN current_date-365 AND current_date+180
	 AND (`+orgInScope("o.organization_id", "$1", "$2")+` OR ($1='own' AND o.owner_id=$3::uuid))
	 ORDER BY CASE WHEN o.object_type='contract' THEN o.end_date ELSE o.due_date END LIMIT 300`, p.DataScope, organizationID, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "기한 업무를 조회하지 못했습니다")
		return
	}
	for rows.Next() {
		var id, objectType, number, title, supplier, due, created string
		if err := rows.Scan(&id, &objectType, &number, &title, &supplier, &due, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "기한 업무를 조회하지 못했습니다")
			return
		}
		if !hasPermission(p, objectType+".read") {
			continue
		}
		kind, category, description := "due_task", "task", "처리 기한이 다가오는 업무입니다."
		if objectType == "contract" {
			kind, category, description = "contract_expiry", "contract", "계약 종료일을 확인하고 갱신 또는 종료를 준비하세요."
		} else if objectType == "delivery" {
			description = "납품 예정일과 실제 진행 상태를 확인하세요."
		} else if objectType == "issue" || objectType == "quality" {
			category, description = "risk", "조치 기한 전 원인과 개선 계획을 확인하세요."
		}
		items = append(items, newWorkItem(datedWorkKey(kind, id, due), kind, category, title, description, objectType, id, number, supplier, due, created, objectListURL(objectType, number), false))
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "기한 업무를 조회하지 못했습니다")
		return
	}
	rows.Close()

	if hasPermission(p, "risk.read") {
		rows, err = a.db.Query(r.Context(), `SELECT r.id,r.risk_type,r.severity,COALESCE(r.description,''),s.id,s.name,
		 COALESCE(to_char(r.review_date,'YYYY-MM-DD'),''),to_char(r.created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
		 FROM risks r JOIN suppliers s ON s.id=r.supplier_id
		 WHERE r.status NOT IN('closed','resolved') AND (r.review_date BETWEEN current_date-365 AND current_date+30 OR (r.review_date IS NULL AND r.severity IN('HIGH','CRITICAL')))
		 AND s.deleted_at IS NULL AND (`+orgInScope("s.organization_id", "$1", "$2")+` OR ($1='own' AND COALESCE(r.owner_id,s.owner_id)=$3::uuid))
		 ORDER BY r.review_date LIMIT 200`, p.DataScope, organizationID, p.ID)
		if err != nil {
			writeError(w, 500, "database_error", "리스크 검토 업무를 조회하지 못했습니다")
			return
		}
		for rows.Next() {
			var id, riskType, severity, description, supplierID, supplier, due, created string
			if err := rows.Scan(&id, &riskType, &severity, &description, &supplierID, &supplier, &due, &created); err != nil {
				logDB(err)
				writeError(w, 500, "database_error", "리스크 검토 업무를 조회하지 못했습니다")
				return
			}
			if description == "" {
				description = severity + " 리스크의 완화 계획과 현재 상태를 검토하세요."
			}
			item := newWorkItem(datedWorkKey("risk", id, due), "risk_review", "risk", riskType, description, "risk", id, "", supplier, due, created, "/suppliers/"+supplierID+"?tab=Risks", false)
			severityUrgency := notificationUrgency(severity)
			if urgencyRank(severityUrgency) < urgencyRank(item.Urgency) {
				item.Urgency = severityUrgency
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "리스크 검토 업무를 조회하지 못했습니다")
			return
		}
		rows.Close()
	}

	if hasPermission(p, "document.read") {
		rows, err = a.db.Query(r.Context(), `SELECT d.id,d.name,d.document_type,COALESCE(s.id::text,''),COALESCE(s.name,''),
		 to_char(d.expires_at,'YYYY-MM-DD'),to_char(d.created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
		 FROM documents d LEFT JOIN suppliers s ON s.id=d.supplier_id
		 WHERE d.status='active' AND d.expires_at BETWEEN current_date-365 AND current_date+30
		 AND ((d.supplier_id IS NULL AND ($1='company' OR d.uploaded_by=$3::uuid))
		   OR `+orgInScope("s.organization_id", "$1", "$2")+` OR ($1='own' AND COALESCE(d.uploaded_by,s.owner_id)=$3::uuid))
		 ORDER BY d.expires_at LIMIT 200`, p.DataScope, organizationID, p.ID)
		if err != nil {
			writeError(w, 500, "database_error", "문서 만료 업무를 조회하지 못했습니다")
			return
		}
		for rows.Next() {
			var id, name, documentType, supplierID, supplier, due, created string
			if err := rows.Scan(&id, &name, &documentType, &supplierID, &supplier, &due, &created); err != nil {
				logDB(err)
				writeError(w, 500, "database_error", "문서 만료 업무를 조회하지 못했습니다")
				return
			}
			targetURL := "/search?q=" + url.QueryEscape(name)
			if supplierID != "" {
				targetURL = "/suppliers/" + supplierID + "?tab=Documents"
			}
			items = append(items, newWorkItem(datedWorkKey("document", id, due), "document_expiry", "document", name, documentType+" 문서의 갱신 또는 재제출이 필요합니다.", "document", id, documentType, supplier, due, created, targetURL, false))
		}
		if err := rows.Err(); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "문서 만료 업무를 조회하지 못했습니다")
			return
		}
		rows.Close()
	}

	rows, err = a.db.Query(r.Context(), `SELECT id,kind,title,body,severity,COALESCE(object_type,''),COALESCE(object_id::text,''),to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
	 FROM notifications WHERE user_id=$1 AND read_at IS NULL AND kind NOT IN('contract_expiry','document_expiry') ORDER BY created_at DESC LIMIT 100`, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "알림 업무를 조회하지 못했습니다")
		return
	}
	for rows.Next() {
		var id, kind, title, body, severity, objectType, objectID, created string
		if err := rows.Scan(&id, &kind, &title, &body, &severity, &objectType, &objectID, &created); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "알림 업무를 조회하지 못했습니다")
			return
		}
		itemURL := objectListURL(objectType, "")
		if objectType == "supplier" && objectID != "" {
			itemURL = "/suppliers/" + objectID
		}
		item := newWorkItem("notification:"+id, kind, "notification", title, body, objectType, objectID, "", "", "", created, itemURL, true)
		item.Urgency = notificationUrgency(severity)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "알림 업무를 조회하지 못했습니다")
		return
	}
	rows.Close()

	states, err := a.loadWorkItemStates(r, p.ID)
	if err != nil {
		writeError(w, 500, "database_error", "업무 상태를 조회하지 못했습니다")
		return
	}
	now := time.Now()
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	categoryCounts := map[string]int{}
	available := items[:0]
	for _, item := range items {
		state := states[item.Key]
		if state.State == "done" || (state.State == "snoozed" && state.SnoozedUntil != nil && state.SnoozedUntil.After(now)) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(item.Title+" "+item.Description+" "+item.Number+" "+item.SupplierName), query) {
			continue
		}
		categoryCounts[item.Category]++
		available = append(available, item)
	}
	filtered := available[:0]
	for _, item := range available {
		if category == "" || category == "all" || item.Category == category {
			filtered = append(filtered, item)
		}
	}
	items = filtered
	sort.SliceStable(items, func(i, j int) bool {
		left, right := urgencyRank(items[i].Urgency), urgencyRank(items[j].Urgency)
		if left != right {
			return left < right
		}
		if items[i].DueDate == nil {
			return false
		}
		if items[j].DueDate == nil {
			return true
		}
		return *items[i].DueDate < *items[j].DueDate
	})
	summary := map[string]int{"total": len(items), "critical": 0, "overdue": 0, "today": 0, "soon": 0}
	for _, item := range items {
		if item.Urgency == "critical" {
			summary["critical"]++
		}
		if _, ok := summary[item.TimeBucket]; ok {
			summary[item.TimeBucket]++
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "summary": summary, "categories": categoryCounts, "generatedAt": now})
}

func (a *App) loadWorkItemStates(r *http.Request, userID string) (map[string]workItemState, error) {
	rows, err := a.db.Query(r.Context(), `SELECT item_key,state,snoozed_until FROM user_work_item_states WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]workItemState{}
	for rows.Next() {
		var key, state string
		var snoozed *time.Time
		if err := rows.Scan(&key, &state, &snoozed); err != nil {
			return nil, err
		}
		states[key] = workItemState{State: state, SnoozedUntil: snoozed}
	}
	return states, rows.Err()
}

func (a *App) updateWorkItemState(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		ItemKeys     []string `json:"itemKeys"`
		State        string   `json:"state"`
		SnoozedUntil string   `json:"snoozedUntil"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if len(in.ItemKeys) == 0 || len(in.ItemKeys) > 100 || (in.State != "active" && in.State != "done" && in.State != "snoozed") {
		writeError(w, 400, "validation_error", "업무와 active, done, snoozed 상태를 올바르게 선택하세요")
		return
	}
	var snoozedUntil *time.Time
	if in.State == "snoozed" {
		parsed, err := time.Parse(time.RFC3339, in.SnoozedUntil)
		if err != nil || !parsed.After(time.Now()) || parsed.After(time.Now().AddDate(1, 0, 0)) {
			writeError(w, 400, "validation_error", "보류 기한은 현재부터 1년 안의 시각이어야 합니다")
			return
		}
		snoozedUntil = &parsed
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "업무 상태를 저장하지 못했습니다")
		return
	}
	defer tx.Rollback(r.Context())
	for _, key := range in.ItemKeys {
		if !productivityKeyPattern.MatchString(key) {
			writeError(w, 400, "validation_error", "업무 식별자가 올바르지 않습니다")
			return
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO user_work_item_states(user_id,item_key,state,snoozed_until) VALUES($1,$2,$3,$4)
		 ON CONFLICT(user_id,item_key) DO UPDATE SET state=excluded.state,snoozed_until=excluded.snoozed_until,updated_at=now()`, p.ID, key, in.State, snoozedUntil)
		if err != nil {
			writeError(w, 500, "database_error", "업무 상태를 저장하지 못했습니다")
			return
		}
		// The key pattern allows any word after the prefix, but the column is a
		// uuid: a malformed one made the cast fail and rolled back every other
		// item in the batch. Skip what cannot be a notification id.
		if id, ok := strings.CutPrefix(key, "notification:"); ok && in.State == "done" && uuidPattern.MatchString(id) {
			_, err = tx.Exec(r.Context(), `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND user_id=$2`, id, p.ID)
			if err != nil {
				writeError(w, 500, "database_error", "알림 상태를 저장하지 못했습니다")
				return
			}
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 500, "database_error", "업무 상태를 저장하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "updated": len(in.ItemKeys)})
}

func newWorkItem(key, kind, category, title, description, objectType, objectID, number, supplier, due, created, url string, actionable bool) workInboxItem {
	item := workInboxItem{Key: key, Kind: kind, Category: category, Title: title, Description: description, ObjectType: objectType, ObjectID: objectID, Number: number, SupplierName: supplier, CreatedAt: created, URL: url, Actionable: actionable}
	if due != "" {
		item.DueDate = &due
	}
	item.Urgency, item.TimeBucket = dueUrgency(due, time.Now())
	return item
}

func dueUrgency(value string, now time.Time) (string, string) {
	if value == "" {
		return "low", "undated"
	}
	due, err := time.ParseInLocation("2006-01-02", value, now.Location())
	if err != nil {
		return "low", "undated"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := int(due.Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return "critical", "overdue"
	case days == 0:
		return "high", "today"
	case days <= 7:
		return "medium", "soon"
	default:
		return "low", "later"
	}
}

func notificationUrgency(severity string) string {
	switch strings.ToLower(severity) {
	case "critical", "danger", "error":
		return "critical"
	case "warning", "high":
		return "high"
	default:
		return "normal"
	}
}

func urgencyRank(value string) int {
	switch value {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "normal":
		return 3
	default:
		return 4
	}
}

func objectListURL(objectType, number string) string {
	paths := map[string]string{"contract": "/contracts", "purchase_request": "/purchase-requests", "rfq": "/rfq", "rfp": "/rfp", "purchase_order": "/purchase-orders", "delivery": "/deliveries", "inspection": "/inspections", "quality": "/quality", "issue": "/issues", "invoice": "/invoices", "payment": "/payments"}
	path := paths[objectType]
	if path == "" {
		return "/"
	}
	if number != "" {
		return path + "?q=" + url.QueryEscape(number)
	}
	return path
}

func datedWorkKey(kind, id, due string) string {
	return kind + ":" + id + ":" + strings.ReplaceAll(due, "-", "")
}

func (a *App) listSavedViews(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	contextName := strings.TrimSpace(r.URL.Query().Get("context"))
	organizationID := ""
	if p.OrganizationID != nil {
		organizationID = *p.OrganizationID
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,user_id,name,context,filters,columns,shared,created_at,updated_at
	 FROM saved_views WHERE ($2='' OR context=$2) AND (user_id=$1 OR (shared=true AND organization_id=NULLIF($3,'')::uuid))
	 ORDER BY CASE WHEN user_id=$1 THEN 0 ELSE 1 END,name`, p.ID, contextName, organizationID)
	if err != nil {
		writeError(w, 500, "database_error", "저장된 보기를 조회하지 못했습니다")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, ownerID, name, contextName string
		var filters, columns []byte
		var shared bool
		var created, updated any
		if err := rows.Scan(&id, &ownerID, &name, &contextName, &filters, &columns, &shared, &created, &updated); err != nil {
			logDB(err)
			writeError(w, 500, "database_error", "저장된 보기를 조회하지 못했습니다")
			return
		}
		var filterValue, columnValue any
		_ = json.Unmarshal(filters, &filterValue)
		_ = json.Unmarshal(columns, &columnValue)
		items = append(items, map[string]any{"id": id, "name": name, "context": contextName, "filters": filterValue, "columns": columnValue, "shared": shared, "owned": ownerID == p.ID, "createdAt": created, "updatedAt": updated})
	}
	if err := rows.Err(); err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "저장된 보기를 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "canShare": p.OrganizationID != nil})
}

type savedViewInput struct {
	Name    string         `json:"name"`
	Context string         `json:"context"`
	Filters map[string]any `json:"filters"`
	Columns []string       `json:"columns"`
	Shared  bool           `json:"shared"`
}

func validateSavedView(in savedViewInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Context = strings.TrimSpace(in.Context)
	if in.Name == "" || len([]rune(in.Name)) > 60 || !productivityKeyPattern.MatchString(strings.ReplaceAll(in.Context, ".", "_")) {
		return errValidation
	}
	if len(in.Columns) > 30 || len(in.Filters) > 30 {
		return errValidation
	}
	return nil
}

var errValidation = errors.New("invalid productivity input")

func (a *App) createSavedView(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in savedViewInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	in.Name, in.Context = strings.TrimSpace(in.Name), strings.TrimSpace(in.Context)
	if validateSavedView(in) != nil || (in.Shared && p.OrganizationID == nil) {
		writeError(w, 400, "validation_error", "보기 이름, 화면과 필터를 확인하세요")
		return
	}
	var id string
	var organizationID any
	if in.Shared {
		organizationID = *p.OrganizationID
	}
	err := a.db.QueryRow(r.Context(), `INSERT INTO saved_views(user_id,organization_id,name,context,filters,columns,shared)
	 VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, p.ID, organizationID, in.Name, in.Context, raw(in.Filters), raw(in.Columns), in.Shared).Scan(&id)
	if err != nil {
		writeError(w, 409, "save_failed", "같은 이름의 보기가 있거나 저장할 수 없습니다")
		return
	}
	a.audit.record(r, "create", "saved_view", id, nil, in)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *App) updateSavedView(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in savedViewInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	in.Name, in.Context = strings.TrimSpace(in.Name), strings.TrimSpace(in.Context)
	if validateSavedView(in) != nil || (in.Shared && p.OrganizationID == nil) {
		writeError(w, 400, "validation_error", "보기 이름, 화면과 필터를 확인하세요")
		return
	}
	var organizationID any
	if in.Shared {
		organizationID = *p.OrganizationID
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE saved_views SET organization_id=$3,name=$4,context=$5,filters=$6,columns=$7,shared=$8,updated_at=now()
	 WHERE id=$1 AND user_id=$2`, r.PathValue("id"), p.ID, organizationID, in.Name, in.Context, raw(in.Filters), raw(in.Columns), in.Shared)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "수정할 보기를 찾을 수 없습니다")
		return
	}
	a.audit.record(r, "update", "saved_view", r.PathValue("id"), nil, in)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *App) deleteSavedView(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tag, err := a.db.Exec(r.Context(), `DELETE FROM saved_views WHERE id=$1 AND user_id=$2`, r.PathValue("id"), p.ID)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found", "삭제할 보기를 찾을 수 없습니다")
		return
	}
	a.audit.record(r, "delete", "saved_view", r.PathValue("id"), nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) getFormDraft(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	key := r.PathValue("key")
	if !productivityKeyPattern.MatchString(key) {
		writeError(w, 400, "validation_error", "임시저장 식별자가 올바르지 않습니다")
		return
	}
	var payload []byte
	var updated any
	err := a.db.QueryRow(r.Context(), `SELECT payload,updated_at FROM user_form_drafts WHERE user_id=$1 AND draft_key=$2`, p.ID, key).Scan(&payload, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"draft": nil})
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "임시저장 내용을 조회하지 못했습니다")
		return
	}
	var value any
	_ = json.Unmarshal(payload, &value)
	writeJSON(w, 200, map[string]any{"draft": map[string]any{"key": key, "payload": value, "updatedAt": updated}})
}

func (a *App) putFormDraft(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	key := r.PathValue("key")
	if !productivityKeyPattern.MatchString(key) {
		writeError(w, 400, "validation_error", "임시저장 식별자가 올바르지 않습니다")
		return
	}
	var in struct {
		Payload map[string]any `json:"payload"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.Payload) > 100 {
		writeError(w, 400, "invalid_request", "임시저장 내용을 확인하세요")
		return
	}
	_, err := a.db.Exec(r.Context(), `INSERT INTO user_form_drafts(user_id,draft_key,payload) VALUES($1,$2,$3)
	 ON CONFLICT(user_id,draft_key) DO UPDATE SET payload=excluded.payload,updated_at=now()`, p.ID, key, raw(in.Payload))
	if err != nil {
		writeError(w, 500, "database_error", "임시저장에 실패했습니다")
		return
	}
	// Keep only the most recently touched drafts. Autosave is a convenience, so
	// dropping the oldest is preferable to refusing to save the current one.
	if _, err := a.db.Exec(r.Context(), `DELETE FROM user_form_drafts WHERE user_id=$1 AND draft_key NOT IN (
		SELECT draft_key FROM user_form_drafts WHERE user_id=$1 ORDER BY updated_at DESC LIMIT $2)`, p.ID, maxFormDraftsPerUser); err != nil {
		logDB(err)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "updatedAt": time.Now()})
}

func (a *App) deleteFormDraft(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	key := r.PathValue("key")
	if !productivityKeyPattern.MatchString(key) {
		writeError(w, 400, "validation_error", "임시저장 식별자가 올바르지 않습니다")
		return
	}
	_, err := a.db.Exec(r.Context(), `DELETE FROM user_form_drafts WHERE user_id=$1 AND draft_key=$2`, p.ID, key)
	if err != nil {
		writeError(w, 500, "database_error", "임시저장 내용을 삭제하지 못했습니다")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
