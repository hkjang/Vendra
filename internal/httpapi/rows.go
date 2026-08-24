package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// rowScanner is the part of pgx.Rows a read path actually uses. Err is the
// method that matters: pgx reports a failure raised partway through a result
// set there, not from Query.
type rowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
}

// scanJSONRows collects rows made of a single jsonb column.
//
// pgx reports a failed query while the rows are being read rather than when
// Query returns, so a loop that ignores rows.Err turns a failure into an empty
// result: the spend report says nothing was spent, the approval list says
// nothing is waiting for you, the audit trail says nobody did anything. Those
// answers are indistinguishable from the truth and far more damaging than an
// error, so every read path has to tell "there are no rows" apart from "we
// could not read them".
func scanJSONRows(rows rowScanner) ([]any, error) {
	items := []any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// searchSource runs one leg of a multi-source search, keeping a failed leg
// distinguishable from a leg that matched nothing.
func searchSource(rows pgx.Rows, err error, scan func(pgx.Rows) (map[string]any, error)) ([]map[string]any, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// dateParam reads an optional YYYY-MM-DD filter. A malformed date is the
// caller's mistake, so it is answered as one rather than reaching PostgreSQL
// and failing there as a server error.
func dateParam(w http.ResponseWriter, r *http.Request, name, label string) (string, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", label+"은(는) YYYY-MM-DD 형식이어야 합니다")
		return "", false
	}
	return value, true
}

// boolSetting reads a boolean control out of the settings table.
//
// A control that cannot be read must not quietly take its permissive value.
// These lookups used to ignore their error and leave the zero value in place,
// so one failed read approved the very request the control exists to hold: a
// purchase order was stored as approved with no approval, and a supplier's
// bank account was replaced without the change ever reaching a reviewer. Both
// were recorded in the audit trail as if approvals had been switched off.
//
// A missing row is different from a failed read: the setting has simply never
// been configured, and missing is the documented default for that key.
func (a *App) boolSetting(ctx context.Context, query string, missing bool, args ...any) (bool, error) {
	var value bool
	switch err := a.db.QueryRow(ctx, query, args...).Scan(&value); {
	case err == nil:
		return value, nil
	case errors.Is(err, pgx.ErrNoRows):
		return missing, nil
	default:
		return false, err
	}
}

// writeControlUnavailable refuses a request whose approval requirements could
// not be established. Proceeding would decide the question the wrong way.
func writeControlUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "결재 설정을 확인할 수 없어 처리를 중단했습니다. 잠시 후 다시 시도하세요")
}
