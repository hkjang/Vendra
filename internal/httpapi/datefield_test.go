package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func errorCodeAndMessage(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var out struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if rec.Body.Len() == 0 {
		return "", ""
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response was not the usual error envelope: %s", rec.Body.String())
	}
	return out.Error.Code, out.Error.Message
}

func TestValidDateNamesTheField(t *testing.T) {
	// Left to PostgreSQL these came back as a failed cast, which the handler
	// reported as "데이터를 저장하지 못했습니다" — no clue which of three date
	// fields on the form was wrong — and logged as a database error.
	// 2024 is a leap year and 2026 is not, so both branches of that rule
	// are exercised. A field of only spaces is an empty field.
	for _, value := range []string{"", "2026-01-15", "2024-02-29", "  "} {
		rec := httptest.NewRecorder()
		if !validDate(rec, value, "시작일") {
			_, msg := errorCodeAndMessage(t, rec)
			t.Errorf("validDate(%q) rejected a usable value: %s", value, msg)
		}
	}
	for _, value := range []string{"2026-13-45", "2026-02-31", "2026-02-29", "쓰레기", "2026-1-5", "2026-01-15T00:00:00Z"} {
		rec := httptest.NewRecorder()
		if validDate(rec, value, "시작일") {
			t.Errorf("validDate(%q) accepted it", value)
			continue
		}
		code, msg := errorCodeAndMessage(t, rec)
		if code != "validation_error" {
			t.Errorf("validDate(%q) answered %q, want validation_error", value, code)
		}
		if msg == "" || msg[:len("시작일은")] != "시작일은" {
			t.Errorf("validDate(%q) said %q; it has to name the field, with the right particle", value, msg)
		}
	}
}

func TestValidInstantTakesWhatClientsSend(t *testing.T) {
	for _, value := range []string{
		"",
		"2026-12-31",
		"2026-12-31T00:00:00Z",
		"2026-12-31T09:00:00+09:00",
		"2026-12-31T09:00:00",
		"2026-12-31 09:00:00",
		"2026-12-31 09:00:00+09:00",
	} {
		rec := httptest.NewRecorder()
		if !validInstant(rec, value, "종료 시각") {
			_, msg := errorCodeAndMessage(t, rec)
			t.Errorf("validInstant(%q) rejected a usable value: %s", value, msg)
		}
	}
	for _, value := range []string{"2026-13-45", "쓰레기", "yesterday", "2026-12-31T99:99:99Z"} {
		rec := httptest.NewRecorder()
		if validInstant(rec, value, "종료 시각") {
			t.Errorf("validInstant(%q) accepted it", value)
			continue
		}
		if code, _ := errorCodeAndMessage(t, rec); code != "validation_error" {
			t.Errorf("validInstant(%q) answered %q, want validation_error", value, code)
		}
	}
}

func TestValidDateFieldsReportsTheFirstBadOne(t *testing.T) {
	in := map[string]any{"startDate": "2026-01-01", "dueDate": "2026-13-45", "endDate": "also wrong"}
	rec := httptest.NewRecorder()
	if validDateFields(rec, in, dateField{"startDate", "시작일"}, dateField{"dueDate", "마감일"}, dateField{"endDate", "종료일"}) {
		t.Fatal("validDateFields accepted a malformed dueDate")
	}
	_, msg := errorCodeAndMessage(t, rec)
	if msg[:len("마감일은")] != "마감일은" {
		t.Errorf("reported %q; the first bad field is dueDate", msg)
	}

	ok := map[string]any{"startDate": "2026-01-01", "endDate": ""}
	if !validDateFields(httptest.NewRecorder(), ok, dateField{"startDate", "시작일"}, dateField{"endDate", "종료일"}) {
		t.Error("validDateFields rejected a usable pair")
	}
}
