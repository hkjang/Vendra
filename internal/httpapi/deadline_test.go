package httpapi

import (
	"testing"
	"time"
)

func TestDeadlinePassedUsesTheBusinessDay(t *testing.T) {
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// 08:30 on the 26th in Seoul is still the 25th in UTC. An RFQ that closed
	// on the 25th must be closed; the old check compared instants against
	// time.Now().Truncate(24*time.Hour), which is always midnight UTC, and
	// kept taking bids for another half hour — up to nine, earlier in the
	// morning.
	seoulMorning := time.Date(2026, 8, 25, 23, 30, 0, 0, time.UTC).In(seoul)
	if !deadlinePassed("2026-08-25", seoulMorning) {
		t.Error("a deadline of the 25th was still open at 08:30 on the 26th in Seoul")
	}
	if deadlinePassed("2026-08-26", seoulMorning) {
		t.Error("a deadline of the 26th was closed on the morning of the 26th in Seoul")
	}

	// The other direction: 21:00 on the 25th in New York is already the 26th
	// in UTC, and a deadline of the 25th must still be open.
	newYorkEvening := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC).In(newYork)
	if deadlinePassed("2026-08-25", newYorkEvening) {
		t.Error("a deadline of the 25th was closed at 21:00 on the 25th in New York")
	}
	if !deadlinePassed("2026-08-24", newYorkEvening) {
		t.Error("a deadline of the 24th was still open on the 25th in New York")
	}

	if deadlinePassed("", seoulMorning) {
		t.Error("an absent deadline was treated as passed")
	}
}

func TestDeadlinePassedBeatsTruncate(t *testing.T) {
	// Guards the specific mistake: Truncate(24h) rounds to a multiple of the
	// duration since the zero instant, so it lands on midnight UTC whatever
	// the local zone is.
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	now := time.Date(2026, 8, 25, 23, 30, 0, 0, time.UTC).In(seoul)
	due := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	if due.Before(now.Truncate(24 * time.Hour)) {
		t.Fatal("the old comparison closed the deadline; this test no longer proves anything")
	}
	if !deadlinePassed(due.Format("2006-01-02"), now) {
		t.Error("deadlinePassed did not close a deadline the old comparison also left open")
	}
}
