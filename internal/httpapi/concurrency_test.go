package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentSubmitsOpenOneApproval races several submits of the same request.
// The guard added in v0.6.17 reads before it writes, so a race could still open
// more than one approval.
func TestConcurrentSubmitsOpenOneApproval(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var roleID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system) VALUES('race_submitter','경합 검증','["contract.read","contract.create","contract.update","workflow.read","workflow.approve"]','company',false)
		ON CONFLICT(code) DO UPDATE SET permissions=excluded.permissions RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	const email = "race-submitter@vendra.test"
	hash, err := app.hashPassword(ctx, "RacePassphrase!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,status) VALUES($1,'경합 검증자',$2,'internal','active')
		ON CONFLICT(email) DO UPDATE SET password_hash=excluded.password_hash,status='active' RETURNING id`, email, hash).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, roleID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	var definitionID string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_definitions(name,object_type,enabled,conditions,steps,created_by) VALUES('경합 검증','contract',true,'{}','[{"name":"승인","role":"","order":0}]',$1) RETURNING id`, userID).Scan(&definitionID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO settings(key,value,category) VALUES('workflow.approval_enabled','true','workflow')
		ON CONFLICT(key) DO UPDATE SET value='true'`); err != nil {
		t.Fatalf("enable approvals: %v", err)
	}
	t.Cleanup(func() {
		clean := context.Background()
		_, _ = pool.Exec(clean, `DELETE FROM workflow_instances WHERE definition_id=$1`, definitionID)
		_, _ = pool.Exec(clean, `DELETE FROM business_objects WHERE number LIKE 'RACE-SUB-%'`)
		_, _ = pool.Exec(clean, `DELETE FROM workflow_definitions WHERE id=$1`, definitionID)
		_, _ = pool.Exec(clean, `DELETE FROM users WHERE email=$1`, email)
		_, _ = pool.Exec(clean, `DELETE FROM roles WHERE code='race_submitter'`)
	})

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, email)
	token := sessionCookieFrom(t, postLogin(t, handler, email, "RacePassphrase!2026", "203.0.113.240:5000"))
	send := func(method, path string, payload any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := send(http.MethodPost, "/api/v1/contracts", map[string]any{"title": "경합 상신", "number": "RACE-SUB-1", "amount": 100})
	var object struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &object); err != nil || object.ID == "" {
		t.Fatalf("create: %s", created.Body.String())
	}

	// Fire the submits together, the way a double-click or a retrying client does.
	const racers = 24
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < racers; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			send(http.MethodPost, "/api/v1/contracts/"+object.ID+"/submit", map[string]any{})
		}()
	}
	start.Done()
	done.Wait()

	var pending int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_instances WHERE object_id=$1 AND status='pending'`, object.ID).Scan(&pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if pending != 1 {
		t.Errorf("%d approvals opened from %d concurrent submits, want 1", pending, racers)
	}
}

// TestConcurrentApprovalsActOnce races several approvers on the same pending
// approval. workflowAction takes a row lock, and this checks that the lock
// actually keeps a single-step approval from being decided twice.
func TestConcurrentApprovalsActOnce(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	var roleID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system) VALUES('race_approver','승인 경합','["contract.read","contract.create","contract.update","workflow.read","workflow.approve"]','company',false)
		ON CONFLICT(code) DO UPDATE SET permissions=excluded.permissions RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	hash, err := app.hashPassword(ctx, "ApproveRace!2026")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	tokens := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		email := fmt.Sprintf("race-approver-%d@vendra.test", i)
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,status) VALUES($1,$2,$3,'internal','active')
			ON CONFLICT(email) DO UPDATE SET password_hash=excluded.password_hash,status='active' RETURNING id`, email, fmt.Sprintf("승인자 %d", i), hash).Scan(&id); err != nil {
			t.Fatalf("seed approver: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, roleID); err != nil {
			t.Fatalf("assign role: %v", err)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, email)
		tokens = append(tokens, sessionCookieFrom(t, postLogin(t, handler, email, "ApproveRace!2026", fmt.Sprintf("203.0.113.%d:5000", 240+i))))
	}
	var ownerID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email='race-approver-0@vendra.test'`).Scan(&ownerID); err != nil {
		t.Fatalf("read owner: %v", err)
	}
	var definitionID, objectID, instanceID string
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_definitions(name,object_type,enabled,conditions,steps,created_by) VALUES('승인 경합','contract',true,'{}','[{"name":"승인","role":"","order":0}]',$1) RETURNING id`, ownerID).Scan(&definitionID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO business_objects(object_type,number,title,status,owner_id,created_by) VALUES('contract','RACE-APR-1','승인 경합','pending_approval',$1,$1) RETURNING id`, ownerID).Scan(&objectID); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workflow_instances(definition_id,object_type,object_id,requested_by,context) VALUES($1,'contract',$2,$3,'{"steps":[{"name":"승인","role":"","order":0}]}') RETURNING id`, definitionID, objectID, ownerID).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	t.Cleanup(func() {
		clean := context.Background()
		_, _ = pool.Exec(clean, `DELETE FROM workflow_instances WHERE definition_id=$1`, definitionID)
		_, _ = pool.Exec(clean, `DELETE FROM business_objects WHERE number='RACE-APR-1'`)
		_, _ = pool.Exec(clean, `DELETE FROM workflow_definitions WHERE id=$1`, definitionID)
		_, _ = pool.Exec(clean, `DELETE FROM users WHERE email LIKE 'race-approver-%@vendra.test'`)
		_, _ = pool.Exec(clean, `DELETE FROM roles WHERE code='race_approver'`)
	})

	var accepted, conflicted int
	var mu sync.Mutex
	var start, done sync.WaitGroup
	start.Add(1)
	for _, token := range tokens {
		done.Add(1)
		go func(token string) {
			defer done.Done()
			start.Wait()
			body, _ := json.Marshal(map[string]string{"action": "approve"})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+instanceID+"/actions", strings.NewReader(string(body)))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			mu.Lock()
			defer mu.Unlock()
			switch w.Code {
			case http.StatusOK:
				accepted++
			case http.StatusConflict:
				conflicted++
			}
		}(token)
	}
	start.Done()
	done.Wait()

	if accepted != 1 {
		t.Errorf("%d approvers each recorded a decision on one approval, want 1", accepted)
	}
	if conflicted != len(tokens)-1 {
		t.Errorf("%d approvers were told the decision was already made, want %d", conflicted, len(tokens)-1)
	}
	var actions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_actions WHERE instance_id=$1`, instanceID).Scan(&actions); err != nil {
		t.Fatalf("count actions: %v", err)
	}
	if actions != 1 {
		t.Errorf("%d decisions recorded in the audit trail for one approval", actions)
	}
}

// TestConcurrentUploadsGetDistinctVersions races uploads of the same document
// name. The version is computed as max(version)+1 inside the insert, which two
// concurrent statements can evaluate against the same snapshot.
func TestConcurrentUploadsGetDistinctVersions(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()

	storage := t.TempDir()
	if _, err := pool.Exec(ctx, `INSERT INTO settings(key,value,category) VALUES('storage',$1,'document')
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprintf(`{"driver":"filesystem","path":%q}`, storage)); err != nil {
		t.Fatalf("point storage at the test directory: %v", err)
	}
	var supplierID string
	if err := pool.QueryRow(ctx, `INSERT INTO suppliers(supplier_number,name,business_number,status) VALUES('SUP-VER-RACE','버전 경합','444-44-44444','active')
		ON CONFLICT(supplier_number) DO UPDATE SET name=excluded.name RETURNING id`).Scan(&supplierID); err != nil {
		t.Fatalf("seed supplier: %v", err)
	}
	t.Cleanup(func() {
		clean := context.Background()
		_, _ = pool.Exec(clean, `DELETE FROM documents WHERE supplier_id=$1`, supplierID)
		_, _ = pool.Exec(clean, `DELETE FROM suppliers WHERE supplier_number='SUP-VER-RACE'`)
		_, _ = pool.Exec(clean, `UPDATE settings SET value='{"driver":"filesystem","path":"/var/lib/vendra/documents"}' WHERE key='storage'`)
	})

	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.250:5000"))

	const uploaders = 12
	var start, done sync.WaitGroup
	start.Add(1)
	for i := 0; i < uploaders; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreateFormFile("file", "VERRACE 계약서.pdf")
			if err != nil {
				return
			}
			_, _ = part.Write([]byte(fmt.Sprintf("본문 %d", i)))
			_ = form.WriteField("documentType", "contract")
			_ = form.WriteField("supplierId", supplierID)
			_ = form.Close()
			start.Wait()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/documents/upload", &body)
			r.Header.Set("Content-Type", form.FormDataContentType())
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: admin})
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}(i)
	}
	start.Done()
	done.Wait()

	rows, err := pool.Query(ctx, `SELECT version, count(*) FROM documents WHERE supplier_id=$1 GROUP BY version HAVING count(*)>1 ORDER BY version`, supplierID)
	if err != nil {
		t.Fatalf("query versions: %v", err)
	}
	defer rows.Close()
	collisions := map[int]int{}
	for rows.Next() {
		var version, count int
		if rows.Scan(&version, &count) == nil {
			collisions[version] = count
		}
	}
	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE supplier_id=$1`, supplierID).Scan(&stored); err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(collisions) > 0 {
		t.Errorf("%d of %d uploads share a version number with another: %v — the version history cannot say which file is which", len(collisions), stored, collisions)
	}
}

// TestConcurrentDraftSavesKeepTheirOwnDraft races autosave. The cap added in
// v0.6.5 deletes everything outside the newest fifty after each save, so a
// concurrent save could evict a draft that was just written.
func TestConcurrentDraftSavesKeepTheirOwnDraft(t *testing.T) {
	app, pool := newTestApp(t)
	ctx := context.Background()
	handler := app.Handler()
	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM user_form_drafts WHERE user_id=$1`, adminID)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_form_drafts WHERE user_id=$1`, adminID)
	})
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
	token := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.251:5000"))

	// Sit right at the cap so every save triggers an eviction.
	for i := 0; i < maxFormDraftsPerUser; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO user_form_drafts(user_id,draft_key,payload) VALUES($1,$2,'{}')
			ON CONFLICT DO NOTHING`, adminID, fmt.Sprintf("filler-%03d", i)); err != nil {
			t.Fatalf("seed filler: %v", err)
		}
	}

	const savers = 16
	keys := make([]string, savers)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := 0; i < savers; i++ {
		keys[i] = fmt.Sprintf("racer-%03d", i)
		done.Add(1)
		go func(key string) {
			defer done.Done()
			body, _ := json.Marshal(map[string]any{"payload": map[string]any{"title": key}})
			start.Wait()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/me/drafts/"+key, strings.NewReader(string(body)))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}(keys[i])
	}
	start.Done()
	done.Wait()

	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_form_drafts WHERE user_id=$1 AND draft_key=ANY($2)`, adminID, keys).Scan(&kept); err != nil {
		t.Fatalf("count racers: %v", err)
	}
	if kept != savers {
		t.Errorf("%d of %d concurrently saved drafts survived; a save reported success but the content was evicted", kept, savers)
	}
	var total int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_form_drafts WHERE user_id=$1`, adminID).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if total > maxFormDraftsPerUser {
		t.Errorf("%d drafts stored, over the %d cap", total, maxFormDraftsPerUser)
	}
}
