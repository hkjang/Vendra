package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
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
