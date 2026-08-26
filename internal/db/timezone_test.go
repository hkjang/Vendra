package db

import (
	"os"
	"testing"
	"time"
)

func TestLocalTimezoneName(t *testing.T) {
	// PostgreSQL does not read TZ, so the pool has to tell it. Go names the
	// zone only when TZ does; with TZ unset it answers "Local", which
	// PostgreSQL would reject, and the session is left on the server default.
	original, had := os.LookupEnv("TZ")
	t.Cleanup(func() {
		if had {
			os.Setenv("TZ", original)
		} else {
			os.Unsetenv("TZ")
		}
		// time.Local is read once at start-up from the environment; reset it
		// so a later test does not inherit this one's zone.
		time.Local = time.FixedZone("Local", 0)
	})

	for _, tc := range []struct{ tz, want string }{
		{"Asia/Seoul", "Asia/Seoul"},
		{"UTC", "UTC"},
		{"America/New_York", "America/New_York"},
	} {
		loc, err := time.LoadLocation(tc.tz)
		if err != nil {
			t.Skipf("tzdata unavailable: %v", err)
		}
		time.Local = loc
		if got := localTimezoneName(); got != tc.want {
			t.Errorf("with TZ=%s, localTimezoneName() = %q, want %q", tc.tz, got, tc.want)
		}
	}

	time.Local = time.FixedZone("Local", 0)
	if got := localTimezoneName(); got != "" {
		t.Errorf("with an unnamed zone, localTimezoneName() = %q, want \"\"", got)
	}
}
