package httpapi

import "testing"

func TestIdWildcards(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"GET /api/v1/suppliers/{id}", []string{"id"}},
		{"POST /api/v1/suppliers/{id}/risks", []string{"id"}},
		{"GET /api/v1/settings/{key}", nil},
		{"GET /api/v1/lookups/{entityType}", nil},
		{"GET /api/v1/dashboard", nil},
		{"GET /api/v1/a/{supplierId}/b/{contactId}", []string{"supplierId", "contactId"}},
		{"GET /api/v1/files/{path...}", nil},
		{"GET /api/v1/broken/{id", nil},
	} {
		got := idWildcards(tc.pattern)
		if len(got) != len(tc.want) {
			t.Errorf("idWildcards(%q) = %v, want %v", tc.pattern, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("idWildcards(%q) = %v, want %v", tc.pattern, got, tc.want)
				break
			}
		}
	}
}

func TestValidUUID(t *testing.T) {
	// A malformed id used to reach PostgreSQL as $1::uuid and come back as a
	// 500, so a stale bookmark read as a broken server.
	valid := []string{
		"c27b2522-4ca7-4981-a999-cd65582c009a",
		"C27B2522-4CA7-4981-A999-CD65582C009A",
	}
	for _, s := range valid {
		if !validUUID(s) {
			t.Errorf("validUUID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"", "notauuid", "1",
		"' OR 1=1--",
		"c27b25224ca74981a999cd65582c009a",       // 하이픈 없음
		"c27b2522-4ca7-4981-a999-cd65582c009",    // 한 자 짧음
		"c27b2522-4ca7-4981-a999-cd65582c009ab",  // 한 자 김
		"c27b2522-4ca7-4981-a999-cd65582c009g",   // 16진수 아님
		"c27b2522x4ca7-4981-a999-cd65582c009a",   // 하이픈 자리 틀림
		"{c27b2522-4ca7-4981-a999-cd65582c009a}", // 중괄호 형식
	}
	for _, s := range invalid {
		if validUUID(s) {
			t.Errorf("validUUID(%q) = true, want false", s)
		}
	}
}
