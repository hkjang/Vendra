package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise submission, definition edits, both inboxes and approval through the
// real handler. Neither reader has wildcard permissions or the other's role.
func TestWorkInboxUsesSubmissionSnapshot(t *testing.T) {
	for _, scenario := range []string{"removed_step", "changed_role", "legacy_fallback"} {
		t.Run(scenario, func(t *testing.T) {
			app, pool := newTestApp(t)
			ctx := context.Background()
			handler := app.Handler()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			cleanupExec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), query, args...); err != nil {
					t.Error(err)
				}
			}
			var originalSetting []byte
			if err := pool.QueryRow(ctx, `SELECT value FROM settings WHERE key='workflow.approval_enabled'`).Scan(&originalSetting); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := pool.Exec(context.Background(), `UPDATE settings SET value=$1 WHERE key='workflow.approval_enabled'`, originalSetting); err != nil {
					t.Error(err)
				}
			})
			exec(`UPDATE settings SET value='true' WHERE key='workflow.approval_enabled'`)
			exec(`DELETE FROM login_attempts WHERE email=$1`, testAdminEmail)
			admin := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.220:5000"))
			send := func(token, method, path, body string, want int) json.RawMessage {
				t.Helper()
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s %s: %d, want %d: %s", method, path, w.Code, want, w.Body.String())
				}
				return w.Body.Bytes()
			}
			decodeID := func(body []byte, field string) string {
				t.Helper()
				var out map[string]any
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatal(err)
				}
				id, _ := out[field].(string)
				if id == "" {
					t.Fatalf("missing %s: %s", field, body)
				}
				return id
			}
			hash, err := app.hashPassword(ctx, testAdminPassword)
			if err != nil {
				t.Fatal(err)
			}
			var tokens [2]string
			var roles [2]string
			for i := range tokens {
				roles[i] = fmt.Sprintf("snapshot_%s_%d", scenario, i)
				email := roles[i] + "@vendra.test"
				var roleID, userID string
				if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system) VALUES($1,$1,'["workflow.read","workflow.approve"]','company',false) RETURNING id`, roles[i]).Scan(&roleID); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { cleanupExec(`DELETE FROM roles WHERE id=$1`, roleID) })
				if err := pool.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,user_type,status) VALUES($1,$1,$2,'internal','active') RETURNING id`, email, hash).Scan(&userID); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					cleanupExec(`DELETE FROM audit_logs WHERE actor_id=$1`, userID)
					cleanupExec(`DELETE FROM login_attempts WHERE email=$1`, email)
					cleanupExec(`DELETE FROM user_roles WHERE user_id=$1`, userID)
					cleanupExec(`DELETE FROM users WHERE id=$1`, userID)
				})
				exec(`INSERT INTO user_roles(user_id,role_id) VALUES($1,$2)`, userID, roleID)
				tokens[i] = sessionCookieFrom(t, postLogin(t, handler, email, testAdminPassword, fmt.Sprintf("203.0.113.%d:5000", 221+i)))
			}
			steps := fmt.Sprintf(`[{"name":"First review","role":%q},{"name":"Original final review","role":%q}]`, roles[0], roles[0])
			ruleID := decodeID(send(admin, "POST", "/api/v1/workflows", `{"name":"Inbox snapshot test","objectType":"payment","enabled":true,"conditions":{},"steps":`+steps+`}`, 201), "id")
			t.Cleanup(func() {
				cleanupExec(`DELETE FROM audit_logs WHERE object_id=$1`, ruleID)
				cleanupExec(`DELETE FROM workflow_definitions WHERE id=$1`, ruleID)
			})
			objectID := decodeID(send(admin, "POST", "/api/v1/payments", `{"title":"Inbox snapshot test","status":"draft","amount":100}`, 201), "id")
			t.Cleanup(func() {
				cleanupExec(`DELETE FROM audit_logs WHERE object_id=$1`, objectID)
				cleanupExec(`DELETE FROM business_objects WHERE id=$1`, objectID)
			})
			instanceID := decodeID(send(admin, "POST", "/api/v1/payments/"+objectID+"/submit", `{}`, 200), "instanceId")
			t.Cleanup(func() {
				cleanupExec(`DELETE FROM workflow_actions WHERE instance_id=$1`, instanceID)
				cleanupExec(`DELETE FROM workflow_instances WHERE id=$1`, instanceID)
			})
			// Assert the intended rule was selected without manufacturing its snapshot.
			var selected string
			if err := pool.QueryRow(ctx, `SELECT definition_id FROM workflow_instances WHERE id=$1`, instanceID).Scan(&selected); err != nil {
				t.Fatal(err)
			}
			if selected != ruleID {
				t.Fatalf("submission used %s instead of %s", selected, ruleID)
			}
			check := func(token string, present bool, stepName, role string) {
				t.Helper()
				var approvals struct {
					Items []struct {
						ID         string `json:"id"`
						Definition struct {
							Name string `json:"name"`
							Role string `json:"role"`
						} `json:"currentStepDefinition"`
					} `json:"items"`
				}
				if err := json.Unmarshal(send(token, "GET", "/api/v1/approvals", "", 200), &approvals); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, item := range approvals.Items {
					if item.ID == instanceID {
						found = true
						if present && (item.Definition.Name != stepName || item.Definition.Role != role) {
							t.Errorf("approval step = %+v, want %s / %s", item.Definition, stepName, role)
						}
					}
				}
				if found != present {
					t.Errorf("approvals contains %s = %v, want %v", instanceID, found, present)
				}
				var inbox struct {
					Items []workInboxItem `json:"items"`
				}
				if err := json.Unmarshal(send(token, "GET", "/api/v1/me/work-inbox", "", 200), &inbox); err != nil {
					t.Fatal(err)
				}
				found = false
				for _, item := range inbox.Items {
					if item.Key == "approval:"+instanceID {
						found = true
						if present && item.Description != "Inbox snapshot test · "+stepName {
							t.Errorf("work inbox description = %q, want original approval step %q", item.Description, stepName)
						}
					}
				}
				if found != present {
					t.Errorf("work inbox contains approval:%s = %v, want %v", instanceID, found, present)
				}
			}
			check(tokens[0], true, "First review", roles[0])
			check(tokens[1], false, "", "")
			send(tokens[0], "POST", "/api/v1/approvals/"+instanceID+"/actions", `{"action":"approve"}`, 200)
			check(tokens[0], true, "Original final review", roles[0])
			changed := fmt.Sprintf(`[{"name":"Changed first review","role":%q},{"name":"Changed final review","role":%q}]`, roles[1], roles[1])
			if scenario == "removed_step" {
				changed = fmt.Sprintf(`[{"name":"Replacement only step","role":%q}]`, roles[1])
			}
			send(admin, "PATCH", "/api/v1/workflows/"+ruleID, `{"steps":`+changed+`}`, 200)
			var finalAnswer json.RawMessage
			if scenario == "legacy_fallback" {
				// Only legacy rows omit the submission snapshot.
				exec(`UPDATE workflow_instances SET context='{}' WHERE id=$1`, instanceID)
				check(tokens[0], false, "", "")
				check(tokens[1], true, "Changed final review", roles[1])
				finalAnswer = send(tokens[1], "POST", "/api/v1/approvals/"+instanceID+"/actions", `{"action":"approve"}`, 200)
			} else {
				check(tokens[0], true, "Original final review", roles[0])
				check(tokens[1], false, "", "")
				finalAnswer = send(tokens[0], "POST", "/api/v1/approvals/"+instanceID+"/actions", `{"action":"approve"}`, 200)
			}
			if status := decodeID(finalAnswer, "status"); status != "approved" {
				t.Errorf("final approval status = %q, want approved", status)
			}
			check(tokens[0], false, "", "")
			check(tokens[1], false, "", "")
		})
	}
}
