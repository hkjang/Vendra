package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliverNotificationAdapters(t *testing.T) {
	t.Run("log", func(t *testing.T) {
		if err := deliverNotification(context.Background(), notificationAdapter{Name: "operations", Type: "log", Enabled: true}, "title", "body", "info"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("webhook", func(t *testing.T) {
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("unexpected request %s %s", r.Method, r.Header.Get("Content-Type"))
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		if err := deliverNotification(context.Background(), notificationAdapter{Name: "chat", Type: "mattermost", URL: server.URL, Enabled: true}, "title", "body", "warning"); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("webhook was not called")
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		if err := deliverNotification(context.Background(), notificationAdapter{Type: "carrier-pigeon"}, "title", "body", "info"); err == nil {
			t.Fatal("unsupported adapter was accepted")
		}
	})
}
