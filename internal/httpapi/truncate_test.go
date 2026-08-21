package httpapi

import "testing"

func TestTruncateReportsAndTrims(t *testing.T) {
	// The probe row must never reach the response.
	items, cut := truncate([]int{1, 2, 3, 4}, 3)
	if !cut {
		t.Error("a page with more rows than the limit was reported as complete")
	}
	if len(items) != 3 {
		t.Errorf("returned %d items for a limit of 3", len(items))
	}
	if items[len(items)-1] != 3 {
		t.Errorf("the probe row leaked into the response: %v", items)
	}
}

func TestTruncateLeavesACompletePageAlone(t *testing.T) {
	for _, in := range [][]string{{}, {"a"}, {"a", "b", "c"}} {
		items, cut := truncate(in, 3)
		if cut {
			t.Errorf("a complete page of %d was reported as truncated", len(in))
		}
		if len(items) != len(in) {
			t.Errorf("a complete page was trimmed: %v -> %v", in, items)
		}
	}
}
