package httpapi

import (
	"errors"
	"strings"
	"testing"
)

func TestSupplierArgAcceptsEitherName(t *testing.T) {
	// Nine of eleven tools spell it supplierId; get_supplier spelled it id.
	// A model reading all eleven at once guesses, and guessing wrong used to
	// answer "supplier not found" — which it relays as "that supplier does not
	// exist" rather than as its own mistake.
	const want = "25950e2d-5cc9-41ef-88f7-1621d2d71b2b"
	for _, args := range []map[string]any{
		{"supplierId": want},
		{"id": want},
		{"supplierId": want, "id": "ignored"},
		{"supplierId": "   ", "id": want},
	} {
		if got := supplierArg(args, "supplierId", "id"); got != want {
			t.Errorf("supplierArg(%v) = %q, want %q", args, got, want)
		}
	}
	for _, args := range []map[string]any{
		{},
		{"supplierId": ""},
		{"other": want},
		{"supplierId": 42},
	} {
		if got := supplierArg(args, "supplierId", "id"); got != "" {
			t.Errorf("supplierArg(%v) = %q, want \"\"", args, got)
		}
	}
}

func TestSupplierListArgAcceptsEitherName(t *testing.T) {
	a, b := "aaaa", "bbbb"
	for _, args := range []map[string]any{
		{"supplierIds": []any{a, b}},
		{"ids": []any{a, b}},
		{"supplierIds": []any{}, "ids": []any{a, b}},
	} {
		got := supplierListArg(args, "supplierIds", "ids")
		if len(got) != 2 || got[0] != a || got[1] != b {
			t.Errorf("supplierListArg(%v) = %v, want [%s %s]", args, got, a, b)
		}
	}
	for _, args := range []map[string]any{
		{},
		{"supplierIds": []any{}},
		{"supplierIds": "not a list"},
	} {
		if got := supplierListArg(args, "supplierIds", "ids"); len(got) != 0 {
			t.Errorf("supplierListArg(%v) = %v, want empty", args, got)
		}
	}
}

func TestMCPToolSchemasNameTheSupplierConsistently(t *testing.T) {
	// The schema is what the model reads. Every tool that takes a supplier has
	// to call it the same thing, or the guessing starts again.
	for _, tool := range mcpTools {
		name, _ := tool["name"].(string)
		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		for _, banned := range []string{"id", "ids"} {
			if _, found := props[banned]; found {
				t.Errorf("%s advertises %q; use supplierId/supplierIds so the surface reads the same way throughout", name, banned)
			}
		}
	}
}

func TestStringArgPassesTextAndRefusesTheRest(t *testing.T) {
	// "" has to keep meaning "no filter". Three test files already call the
	// search tools with {"query":""} to mean 전부, and a model filling in a
	// template writes "" for what it has no value for — refusing it would be a
	// change to the tools' contract, not a fix to the defect this helper is
	// for.
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, ""},
		{map[string]any{"query": nil}, ""},
		{map[string]any{"query": ""}, ""},
		{map[string]any{"query": "   "}, ""},
		{map[string]any{"query": "  1001  "}, "1001"},
		{map[string]any{"other": 1001}, ""},
	} {
		got, err := stringArg("search_suppliers", tc.args, "query")
		if err != nil {
			t.Errorf("stringArg(%v) refused: %v", tc.args, err)
		} else if got != tc.want {
			t.Errorf("stringArg(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
	// A value that is not text used to arrive as "", which these tools read as
	// the filter being left out — so the answer widened to everything in scope
	// with nothing saying so. The refusal has to name the argument and the
	// value, because that is what the model needs to correct the call.
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"query": float64(1001)}, "search_suppliers query must be text: 1001"},
		{map[string]any{"query": true}, "search_suppliers query must be text: true"},
		{map[string]any{"query": []any{"a"}}, `search_suppliers query must be text: ["a"]`},
		{map[string]any{"query": map[string]any{"name": "a"}}, `search_suppliers query must be text: {"name":"a"}`},
	} {
		got, err := stringArg("search_suppliers", tc.args, "query")
		if got != "" {
			t.Errorf("stringArg(%v) = %q, want \"\" alongside the refusal", tc.args, got)
		}
		if err == nil {
			t.Fatalf("stringArg(%v) was accepted", tc.args)
		}
		if !errors.Is(err, errMCPTool) {
			t.Errorf("stringArg(%v) refused with %v, which is not relayed to the caller", tc.args, err)
		}
		if !strings.HasSuffix(err.Error(), tc.want) {
			t.Errorf("stringArg(%v) said %q, want it to end with %q", tc.args, err, tc.want)
		}
	}
}
