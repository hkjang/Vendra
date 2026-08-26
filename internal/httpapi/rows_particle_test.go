package httpapi

import "testing"

func TestTopicParticle(t *testing.T) {
	// The vowel/consonant ending is the whole rule, so the cases that matter
	// are pairs that differ only in the final consonant.
	for _, tc := range []struct{ word, want string }{
		{"업체명", "은"},   // ㅇ 받침
		{"국가", "는"},    // 받침 없음
		{"시작일", "은"},   // ㄹ 받침
		{"사업자번호", "는"}, // 받침 없음
		{"등급", "은"},    // ㅂ 받침
		{"상태", "는"},
		{"업체 코드", "는"},
		{"SUP-1", "는"}, // 한글 음절이 아닌 끝
		{"", "는"},
		{"  대표자  ", "는"}, // 공백은 무시
	} {
		if got := topicParticle(tc.word); got != tc.want {
			t.Errorf("topicParticle(%q) = %q, want %q", tc.word, got, tc.want)
		}
	}
}
