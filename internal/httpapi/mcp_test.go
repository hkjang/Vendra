package httpapi

import (
	"errors"
	"testing"
)

func TestMCPToolErrorsStaySeparateFromInternalFaults(t *testing.T) {
	deliberate := mcpToolError("data scope denied")
	if !errors.Is(deliberate, errMCPTool) {
		t.Fatal("a deliberate refusal was not recognised as a tool error")
	}
	if got := deliberate.Error(); got != "mcp tool: data scope denied" {
		t.Errorf("message = %q", got)
	}
	internal := errors.New(`ERROR: invalid input syntax for type uuid: "not-a-uuid" (SQLSTATE 22P02)`)
	if errors.Is(internal, errMCPTool) {
		t.Fatal("a driver error was treated as a safe tool message")
	}
}
