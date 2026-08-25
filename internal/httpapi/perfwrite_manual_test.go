package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestMeasureWriteLatency reports how long the write paths take against
// whatever data the target database holds. Like the read harness it asserts
// nothing; run it with VENDRA_PERF=1 against a seeded database.
func TestMeasureWriteLatency(t *testing.T) {
	if os.Getenv("VENDRA_PERF") == "" {
		t.Skip("set VENDRA_PERF=1 to measure write latency")
	}
	app, pool := newTestApp(t)
	handler := app.Handler()
	ctx := context.Background()
	var roleID string
	if err := pool.QueryRow(ctx, `INSERT INTO roles(code,name,permissions,data_scope,system) VALUES('perf_writer','성능 쓰기','["*"]','company',false)
		ON CONFLICT(code) DO UPDATE SET permissions='["*"]',data_scope='company' RETURNING id`).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, testAdminEmail).Scan(&adminID); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, adminID, roleID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE settings SET value=jsonb_build_object('driver','filesystem','path',$1::text) WHERE key='storage'`, t.TempDir()); err != nil {
		t.Fatalf("storage: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM login_attempts`)
	token := sessionCookieFrom(t, postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.1:5000"))

	var supplierID string
	if err := pool.QueryRow(ctx, `SELECT id FROM suppliers WHERE deleted_at IS NULL LIMIT 1`).Scan(&supplierID); err != nil {
		t.Skipf("no supplier: %v", err)
	}

	send := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}
	upload := func(n int) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", fmt.Sprintf("perf-%d.txt", n))
		_, _ = fw.Write(bytes.Repeat([]byte("x"), 4096))
		_ = mw.WriteField("documentType", "other")
		_ = mw.WriteField("supplierId", supplierID)
		_ = mw.Close()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/documents/upload", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	type step struct {
		name string
		call func(n int) *httptest.ResponseRecorder
	}
	steps := []step{
		{"POST /suppliers", func(n int) *httptest.ResponseRecorder {
			// Names have to differ from each other, or the duplicate check
			// answers 409 and the measurement covers the wrong path.
			return send("POST", "/api/v1/suppliers", fmt.Sprintf(`{"name":"%s%s측정%d","businessNumber":"900-99-%05d","supplierNumber":"PW-%06d"}`,
				[]string{"가", "나", "다", "라", "마", "바", "사", "아"}[n%8],
				[]string{"철강", "제지", "의약", "항공", "조선", "섬유", "식품", "통신"}[(n/8)%8], n, n%100000, n))
		}},
		{"POST /contracts", func(n int) *httptest.ResponseRecorder {
			return send("POST", "/api/v1/contracts", fmt.Sprintf(`{"number":"PWC-%06d","title":"쓰기측정 계약 %d","supplierId":%q,"amount":1000}`, n, n, supplierID))
		}},
		{"PATCH /contracts/{id}", nil},
		{"POST /contracts/{id}/submit", nil},
		{"POST /suppliers/{id}/risks", func(n int) *httptest.ResponseRecorder {
			return send("POST", "/api/v1/suppliers/"+supplierID+"/risks", `{"riskType":"재무","severity":"HIGH","probability":3,"impact":3}`)
		}},
		{"POST /spend/transactions", func(n int) *httptest.ResponseRecorder {
			return send("POST", "/api/v1/spend/transactions", fmt.Sprintf(`{"transactionNumber":"PWT-%06d","supplierId":%q,"itemName":"품목","amount":1000,"transactionDate":"2026-01-15"}`, n, supplierID))
		}},
		{"POST /documents/upload", func(n int) *httptest.ResponseRecorder { return upload(n) }},
		{"POST /auth/login", func(n int) *httptest.ResponseRecorder {
			_, _ = pool.Exec(context.Background(), `DELETE FROM login_attempts`)
			return postLogin(t, handler, testAdminEmail, testAdminPassword, "203.0.113.2:5000")
		}},
	}

	// The two that need a record of their own are wired after creation.
	created := make([]string, 0, 16)
	const runs = 8
	type result struct {
		name   string
		median time.Duration
		worst  time.Duration
		status int
	}
	results := make([]result, 0, len(steps))
	for _, s := range steps {
		call := s.call
		switch s.name {
		case "PATCH /contracts/{id}":
			call = func(n int) *httptest.ResponseRecorder {
				return send("PATCH", "/api/v1/contracts/"+created[n%len(created)], fmt.Sprintf(`{"title":"수정 %d"}`, n))
			}
		case "POST /contracts/{id}/submit":
			call = func(n int) *httptest.ResponseRecorder {
				return send("POST", "/api/v1/contracts/"+created[n%len(created)]+"/submit", `{}`)
			}
		}
		if call == nil {
			continue
		}
		samples := make([]time.Duration, 0, runs)
		status := 0
		for i := 0; i < runs; i++ {
			began := time.Now()
			rec := call(i + int(time.Now().UnixNano()%1000000))
			samples = append(samples, time.Since(began))
			status = rec.Code
			if s.name == "POST /contracts" && rec.Code < 300 {
				var out struct {
					Object struct {
						ID string `json:"id"`
					} `json:"object"`
					ID string `json:"id"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &out)
				if id := out.Object.ID; id != "" {
					created = append(created, id)
				} else if out.ID != "" {
					created = append(created, out.ID)
				}
			}
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		results = append(results, result{s.name, samples[len(samples)/2], samples[len(samples)-1], status})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].median > results[j].median })
	for _, r := range results {
		marker := "   "
		if r.median > 300*time.Millisecond {
			marker = "!! "
		} else if r.median > 100*time.Millisecond {
			marker = " ! "
		}
		fmt.Printf("%s%-30s median=%-9s worst=%-9s status=%d\n", marker, r.name, r.median.Round(time.Millisecond), r.worst.Round(time.Millisecond), r.status)
	}
}
