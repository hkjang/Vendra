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

func TestCalculateScreeningScoresWhatWasAnswered(t *testing.T) {
	items := []byte(`[
		{"code":"credit","name":"신용","weight":50,"required":true},
		{"code":"safety","name":"안전","weight":30,"required":false},
		{"code":"esg","name":"ESG","weight":20,"required":false}
	]`)

	// An unanswered item scores zero rather than being left out of the total,
	// so a half-filled screening cannot score as well as a complete one. The
	// supplier evaluation next door had the opposite arrangement and graded an
	// empty submission a D on 0 points; this one drags the total down instead,
	// which lands on REVIEW_REQUIRED rather than a verdict.
	_, total, missing := calculateScreening(items, map[string]any{})
	if total != 0 {
		t.Errorf("an empty screening scored %v, want 0", total)
	}
	if !missing {
		t.Error("an empty screening did not report its required item as missing")
	}

	_, total, missing = calculateScreening(items, map[string]any{"credit": 100.0, "safety": 100.0, "esg": 100.0})
	if total != 100 {
		t.Errorf("full marks scored %v, want 100", total)
	}
	if missing {
		t.Error("a complete screening reported something missing")
	}

	// Answering only the optional items leaves the required one missing.
	_, total, missing = calculateScreening(items, map[string]any{"safety": 100.0, "esg": 100.0})
	if total != 50 {
		t.Errorf("the optional half scored %v, want 50", total)
	}
	if !missing {
		t.Error("the unanswered required item was not reported")
	}

	// Out-of-range answers are clamped, so a single item cannot carry the
	// total past what the weights allow.
	_, total, _ = calculateScreening(items, map[string]any{"credit": 1000.0, "safety": -50.0, "esg": 0.0})
	if total != 50 {
		t.Errorf("a 1000 on a 50-weight item scored %v, want 50", total)
	}

	// A value that is not a number is not an answer.
	_, total, missing = calculateScreening(items, map[string]any{"credit": "100", "safety": nil})
	if total != 0 {
		t.Errorf("non-numeric answers scored %v, want 0", total)
	}
	if !missing {
		t.Error("a non-numeric answer to a required item was counted as answered")
	}
}
