package httpapi

import (
	"encoding/json"
	"testing"
)

func stepsJSON(t *testing.T, names ...string) []byte {
	t.Helper()
	steps := make([]map[string]any, 0, len(names))
	for i, name := range names {
		steps = append(steps, map[string]any{"name": name, "role": name, "order": i})
	}
	encoded, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func snapshotJSON(t *testing.T, names ...string) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"steps": json.RawMessage(stepsJSON(t, names...))})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestInstanceStepsPrefersTheSubmissionSnapshot(t *testing.T) {
	snapshot := snapshotJSON(t, "team_lead", "finance", "cfo")
	// The administrator has since cut the workflow down to a single step.
	definition := stepsJSON(t, "cfo")

	got := instanceSteps(snapshot, definition)
	if len(got) != 3 {
		t.Fatalf("in-flight approval follows %d steps, want the 3 it was submitted under", len(got))
	}
	if got[0]["role"] != "team_lead" {
		t.Errorf("first approver is %v, want team_lead", got[0]["role"])
	}
	// Reading the live definition would put current_step=1 past the end of a
	// one-step list, and workflowAction rejects that with 409 — the request
	// could then never be approved, rejected or returned.
	const currentStep = 1
	if currentStep >= len(instanceSteps(snapshot, definition)) {
		t.Error("the approval would be stranded with no action accepted")
	}
	if live := instanceSteps(nil, definition); currentStep < len(live) {
		t.Error("expected the live definition alone to strand this approval; the test no longer reproduces the bug")
	}
}

func TestInstanceStepsFallsBackToTheDefinition(t *testing.T) {
	definition := stepsJSON(t, "team_lead", "finance")
	for _, snapshot := range [][]byte{nil, []byte(`{}`), []byte(`{"steps":[]}`), []byte(`not json`)} {
		got := instanceSteps(snapshot, definition)
		if len(got) != 2 {
			t.Errorf("snapshot %q produced %d steps, want the definition's 2", snapshot, len(got))
		}
	}
}

func TestInstanceStepsKeepsStepOrderAndRoles(t *testing.T) {
	got := instanceSteps(snapshotJSON(t, "a", "b", "c"), nil)
	for i, want := range []string{"a", "b", "c"} {
		if got[i]["role"] != want {
			t.Errorf("step %d role = %v, want %s", i, got[i]["role"], want)
		}
	}
}
