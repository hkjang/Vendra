package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

const testAccountNumber = "110-1234-567890"

func TestAuditPayloadNeverCarriesTheBankAccount(t *testing.T) {
	in := map[string]any{
		"name":        "가나다 주식회사",
		"bankAccount": testAccountNumber,
		"phone":       "02-0000-0000",
	}
	got := auditableSupplierInput(in)

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	// The account number is stored as AES-256-GCM ciphertext; writing the
	// submitted plaintext into audit_logs.new_value would undo that for every
	// holder of audit.read.
	if strings.Contains(string(encoded), testAccountNumber) {
		t.Fatalf("the account number reached the audit payload: %s", encoded)
	}
	if _, present := got["bankAccount"]; present {
		t.Error("the bankAccount key survived redaction")
	}
	if got["bankAccountChanged"] != true {
		t.Error("the audit no longer records that the account changed")
	}
	// Everything else must still be auditable.
	if got["name"] != "가나다 주식회사" || got["phone"] != "02-0000-0000" {
		t.Errorf("unrelated fields were altered: %#v", got)
	}
	// The caller's map must not be mutated; it is used for the update itself.
	if in["bankAccount"] != testAccountNumber {
		t.Error("redaction mutated the request map the update depends on")
	}
}

func TestAuditPayloadDistinguishesAnEmptyAccountFromAChange(t *testing.T) {
	blank := auditableSupplierInput(map[string]any{"name": "x", "bankAccount": "   "})
	if blank["bankAccountChanged"] != false {
		t.Error("a blank account was recorded as a change")
	}
	// A request that never mentions the account is passed through untouched.
	untouched := map[string]any{"name": "x"}
	got := auditableSupplierInput(untouched)
	if _, present := got["bankAccountChanged"]; present {
		t.Error("an unrelated update gained a bank account marker")
	}
}
