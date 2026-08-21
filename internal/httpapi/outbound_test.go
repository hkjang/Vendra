package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdapterTimeoutDefaultsAndClamps(t *testing.T) {
	if got := (notificationAdapter{}).timeout(); got != defaultAdapterTimeoutSeconds*time.Second {
		t.Errorf("unset timeout = %v, want the %ds default", got, defaultAdapterTimeoutSeconds)
	}
	if got := (notificationAdapter{TimeoutSeconds: -5}).timeout(); got != defaultAdapterTimeoutSeconds*time.Second {
		t.Errorf("negative timeout = %v, want the default", got)
	}
	if got := (notificationAdapter{TimeoutSeconds: 9999}).timeout(); got != maxAdapterTimeoutSeconds*time.Second {
		t.Errorf("oversized timeout = %v, want the %ds ceiling", got, maxAdapterTimeoutSeconds)
	}
	if got := (notificationAdapter{TimeoutSeconds: 25}).timeout(); got != 25*time.Second {
		t.Errorf("configured timeout = %v, want 25s", got)
	}
}

// A webhook host that accepts the connection but never responds used to block
// deliverNotification forever, which killed the whole background loop.
func TestDeliverNotificationGivesUpOnAnUnresponsiveAdapter(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	// Release the handler before shutting the server down: Close waits for
	// in-flight requests, so the order matters.
	defer server.Close()
	defer close(release)

	adapter := notificationAdapter{Name: "stuck", Type: "webhook", URL: server.URL, Enabled: true, TimeoutSeconds: 1}
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- deliverNotification(context.Background(), adapter, "제목", "본문", "info")
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an unresponsive adapter reported success")
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Errorf("delivery took %v, far past the 1s adapter budget", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("delivery never returned; the background loop would be stuck here")
	}
}

func TestDeliverNotificationReportsAdapterFailureStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	adapter := notificationAdapter{Name: "broken", Type: "webhook", URL: server.URL, Enabled: true}
	if err := deliverNotification(context.Background(), adapter, "제목", "본문", "info"); err == nil {
		t.Fatal("a 500 from the adapter was treated as delivered")
	}
}

func TestOutboundClientHasATimeout(t *testing.T) {
	if outboundClient.Timeout <= 0 {
		t.Fatal("the shared outbound client has no timeout")
	}
	if outboundClient == http.DefaultClient {
		t.Fatal("integrations fell back to http.DefaultClient, which never times out")
	}
}
