package httpapi

import "testing"

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
