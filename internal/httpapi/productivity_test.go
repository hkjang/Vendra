package httpapi

import (
	"testing"
	"time"
)

func TestDueUrgency(t *testing.T) {
	now := time.Date(2026, 8, 21, 15, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	tests := []struct {
		date    string
		urgency string
		bucket  string
	}{
		{"2026-08-20", "critical", "overdue"},
		{"2026-08-21", "high", "today"},
		{"2026-08-28", "medium", "soon"},
		{"2026-08-29", "low", "later"},
		{"", "low", "undated"},
	}
	for _, test := range tests {
		urgency, bucket := dueUrgency(test.date, now)
		if urgency != test.urgency || bucket != test.bucket {
			t.Errorf("date=%q got %s/%s, want %s/%s", test.date, urgency, bucket, test.urgency, test.bucket)
		}
	}
}

func TestSavedViewValidation(t *testing.T) {
	valid := savedViewInput{Name: "이번 달 만료", Context: "object:contract", Filters: map[string]any{"status": "active"}}
	if err := validateSavedView(valid); err != nil {
		t.Fatalf("valid saved view rejected: %v", err)
	}
	invalid := []savedViewInput{
		{Name: "", Context: "object:contract"},
		{Name: "보기", Context: "invalid context"},
		{Name: string(make([]rune, 61)), Context: "object:contract"},
	}
	for _, input := range invalid {
		if validateSavedView(input) == nil {
			t.Errorf("invalid saved view accepted: %#v", input)
		}
	}
}

func TestObjectListURL(t *testing.T) {
	if got := objectListURL("contract", "CON 10/20"); got != "/contracts?q=CON+10%2F20" {
		t.Fatalf("unexpected object URL %q", got)
	}
	if got := objectListURL("unknown", "id"); got != "/" {
		t.Fatalf("unknown object URL should fall back to root, got %q", got)
	}
}

func TestDatedWorkKeyChangesWhenDueDateChanges(t *testing.T) {
	first := datedWorkKey("contract_expiry", "object-id", "2026-08-21")
	second := datedWorkKey("contract_expiry", "object-id", "2026-09-21")
	if first == second || first != "contract_expiry:object-id:20260821" {
		t.Fatalf("dated work key does not preserve signal lifecycle: %q / %q", first, second)
	}
}

func TestObjectOrderByProtectsRedactedAmounts(t *testing.T) {
	if got := objectOrderBy("amount_desc", false); got != "o.updated_at DESC" {
		t.Fatalf("redacted amount influenced sorting: %q", got)
	}
	if got := objectOrderBy("amount_desc", true); got != "o.amount DESC NULLS LAST, o.updated_at DESC" {
		t.Fatalf("authorized amount sort not applied: %q", got)
	}
	if got := objectOrderBy("malicious SQL", true); got != "o.updated_at DESC" {
		t.Fatalf("unknown sort was accepted: %q", got)
	}
}
