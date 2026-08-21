package httpapi

import "testing"

func TestScreeningThresholdsRejectUnusableRules(t *testing.T) {
	usable := []screeningThresholds{
		{PassMin: 80, ConditionalMin: 70, ReviewMin: 60},
		{PassMin: 1, ConditionalMin: 1, ReviewMin: 0},
	}
	for _, th := range usable {
		if !th.usable() {
			t.Errorf("a valid rule set was rejected: %#v", th)
		}
	}
	unusable := []screeningThresholds{
		{}, // the template omitted result_rules entirely
		{PassMin: 0, ConditionalMin: 0, ReviewMin: 0}, // explicit zeroes decide nothing
		{PassMin: 60, ConditionalMin: 70, ReviewMin: 80},
		{PassMin: 80, ConditionalMin: 70, ReviewMin: -5},
	}
	for _, th := range unusable {
		if th.usable() {
			t.Errorf("an undecidable rule set was accepted: %#v", th)
		}
	}
}

// A template with no thresholds used to make "total >= passMin" true for every
// score, so the screening returned PASS and the supplier was approved.
func TestScreeningWithoutThresholdsNeverPasses(t *testing.T) {
	none := screeningThresholds{}
	for _, total := range []float64{0, 1, 50, 99.9, 100} {
		if got := none.decide(total, false); got != "REVIEW_REQUIRED" {
			t.Errorf("score %v with no thresholds decided %q, want REVIEW_REQUIRED", total, got)
		}
	}
	if got := none.decide(100, true); got != "REVIEW_REQUIRED" {
		t.Errorf("missing required items decided %q", got)
	}
}

func TestScreeningDecidesAcrossTheConfiguredBands(t *testing.T) {
	th := screeningThresholds{PassMin: 80, ConditionalMin: 70, ReviewMin: 60, RequiredFailureResult: "REJECT"}
	for _, test := range []struct {
		total float64
		want  string
	}{
		{95, "PASS"}, {80, "PASS"},
		{79.9, "CONDITIONAL_PASS"}, {70, "CONDITIONAL_PASS"},
		{69.9, "REVIEW_REQUIRED"}, {60, "REVIEW_REQUIRED"},
		{59.9, "REJECT"}, {0, "REJECT"},
	} {
		if got := th.decide(test.total, false); got != test.want {
			t.Errorf("score %v decided %q, want %q", test.total, got, test.want)
		}
	}
	// A missing required item overrides the score entirely.
	if got := th.decide(100, true); got != "REJECT" {
		t.Errorf("missing required item with a perfect score decided %q, want the template's REJECT", got)
	}
	// With no explicit failure result the safe default applies.
	bare := screeningThresholds{PassMin: 80, ConditionalMin: 70, ReviewMin: 60}
	if got := bare.decide(100, true); got != "REVIEW_REQUIRED" {
		t.Errorf("missing required item decided %q, want REVIEW_REQUIRED", got)
	}
}
