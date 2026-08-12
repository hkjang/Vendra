package observability

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestCaptureQueryAndRedaction(t *testing.T) {
	store := NewStore(100)
	logger := slog.New(NewCaptureHandler(slog.NewJSONHandler(io.Discard, nil), store))
	logger.InfoContext(context.Background(), "service ready", "request_id", "req-1", "api_key", "do-not-store")
	logger.Error("database failed", "component", "postgres")

	items, stats := store.Query("ERROR", "postgres", 20)
	if len(items) != 1 || items[0].Message != "database failed" || stats.Error != 1 || stats.Info != 1 {
		t.Fatalf("unexpected query result: items=%v stats=%+v", items, stats)
	}
	all, _ := store.Query("ALL", "", 20)
	if all[1].Attributes["api_key"] != "[REDACTED]" {
		t.Fatalf("sensitive attribute was not redacted: %v", all[1].Attributes)
	}
}

func TestStoreKeepsNewestEntries(t *testing.T) {
	store := NewStore(100)
	for index := 0; index < 105; index++ {
		store.Add(Entry{Level: "INFO", Message: "entry"})
	}
	items, stats := store.Query("", "", 500)
	if len(items) != 100 || stats.Retained != 100 || items[0].ID != 105 {
		t.Fatalf("ring buffer mismatch: newest=%d retained=%d", items[0].ID, stats.Retained)
	}
}

func TestHandlerPreservesAttributeGroups(t *testing.T) {
	store := NewStore(100)
	handler := NewCaptureHandler(slog.NewJSONHandler(io.Discard, nil), store)
	logger := slog.New(handler).With("outside", "value").WithGroup("request").With("token", "hidden")
	logger.Info("grouped", "path", "/admin")

	items, _ := store.Query("", "", 10)
	if len(items) != 1 {
		t.Fatalf("expected one entry, got %d", len(items))
	}
	attributes := items[0].Attributes
	if attributes["outside"] != "value" || attributes["request.path"] != "/admin" {
		t.Fatalf("group semantics were not preserved: %v", attributes)
	}
	if attributes["request.token"] != "[REDACTED]" {
		t.Fatalf("grouped secret was not redacted: %v", attributes)
	}
}

func TestHandlerConvertsNonJSONAttribute(t *testing.T) {
	store := NewStore(100)
	logger := slog.New(NewCaptureHandler(slog.NewJSONHandler(io.Discard, nil), store))
	logger.Info("non-json", "value", make(chan int))

	items, _ := store.Query("", "", 10)
	if len(items) != 1 || items[0].Attributes["value"] == nil {
		t.Fatalf("non-JSON value was not captured safely: %v", items)
	}
}
