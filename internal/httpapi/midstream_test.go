package httpapi

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/hkjang/Vendra/internal/db"
)

// stubRows replays a fixed set of rows and then reports a failure, which is how
// PostgreSQL delivers an error raised partway through a result set.
type stubRows struct {
	values [][]byte
	at     int
	err    error
}

func (s *stubRows) Next() bool {
	if s.at >= len(s.values) {
		return false
	}
	s.at++
	return true
}

func (s *stubRows) Scan(dest ...any) error {
	*(dest[0].(*[]byte)) = s.values[s.at-1]
	return nil
}

func (s *stubRows) Err() error { return s.err }

// A partial result is a failure, not a shorter list: returning the rows that
// did arrive would understate a spend report or an approval queue without any
// sign that something went wrong.
func TestScanJSONRowsReportsLateFailure(t *testing.T) {
	boom := errors.New("connection closed midway through the result set")
	rows := &stubRows{values: [][]byte{[]byte(`{"a":1}`), []byte(`{"a":2}`)}, err: boom}

	items, err := scanJSONRows(rows)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the underlying failure", err)
	}
	if items != nil {
		t.Errorf("items = %v, want nothing — a caller that gets rows may use them", items)
	}
}

// The real thing: PostgreSQL streams rows and can raise an error partway
// through. Query returns successfully, the first rows arrive, and only then
// does the statement fail. Reporting that as an empty result set is the bug
// this guards.
func TestMidStreamFailureIsNotEmptyResult(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VENDRA_TEST_DSN"))
	if dsn == "" {
		t.Skip("set VENDRA_TEST_DSN to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer pool.Close()

	// Rows 1 and 2 build fine; row 3 divides by zero.
	rows, err := pool.Query(ctx, `SELECT jsonb_build_object('n', 100/(3-i)) FROM generate_series(1,5) i`)
	if err != nil {
		t.Fatalf("the query was expected to start successfully, but Query failed: %v", err)
	}
	defer rows.Close()

	items, err := scanJSONRows(rows)
	if err == nil {
		t.Fatalf("a statement that failed partway through was reported as %d rows and no error", len(items))
	}
	if !strings.Contains(err.Error(), "division by zero") {
		t.Errorf("err = %v, want the PostgreSQL failure", err)
	}
}
