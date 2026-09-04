package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// topicParticle picks 은 or 는 for word. Korean chooses between them on
// whether the last syllable closes on a consonant, and a message that ducks
// the choice with "업체명은(는)" reads like a form letter rather than a
// sentence. A word ending outside the Hangul syllable block — a code, a
// number — takes 는.
func topicParticle(word string) string {
	runes := []rune(strings.TrimSpace(word))
	if len(runes) == 0 {
		return "는"
	}
	last := runes[len(runes)-1]
	if last < 0xAC00 || last > 0xD7A3 || (last-0xAC00)%28 == 0 {
		return "는"
	}
	return "은"
}

// jsonDate reads a date out of a JSON field that clients can write.
//
// Casting such a value directly is a defect waiting to happen. "2026-13-45"
// satisfies any reasonable regex and then fails the cast, and the failure
// takes the whole statement with it — one stored value left the dashboard
// answering 500 to everyone in scope, permanently, and another stopped a
// notification pass from generating anything. PostgreSQL 16 has no cast that
// can decline to fail, but the silent flag on jsonb_path_query_first answers
// NULL instead, which the caller can COALESCE or simply let fall out of a
// comparison.
//
// A Z suffix is rewritten to +00:00 first: jsonpath's datetime() follows ISO
// 8601 strictly and will not take the military form. Going through timestamptz
// means the answer is the date where the business is, which is what these
// comparisons are measured against.
func jsonDate(column, key string) string {
	return "(jsonb_path_query_first(jsonb_build_object('d',replace(" + column + "->>'" + key + "','Z','+00:00'))," +
		"'$.d.datetime()','{}',true) #>> '{}')::timestamptz::date"
}

// jsonBool reads a boolean out of a JSON field, answering fallback for
// anything it cannot read. Same hazard as jsonDate: ('maybe')::boolean errors,
// and the failure takes the statement with it. Every spelling PostgreSQL
// accepts is listed, so nothing that used to read one way now reads the other.
func jsonBool(column, key string, fallback bool) string {
	return jsonBoolValue(column+"->'"+key+"'", column+"->>'"+key+"'", fallback)
}

// jsonBoolSetting is jsonBool for a settings row that holds the boolean as its
// whole value rather than under a key. The earlier sweep of these casts looked
// for ->>'key' and so walked straight past this shape: setting
// workflow.approval_enabled to something that is not a boolean — which the
// settings endpoint accepts — made every submission answer 503.
func jsonBoolSetting(column string, fallback bool) string {
	return jsonBoolValue(column, column+" #>> '{}'", fallback)
}

func jsonBoolValue(jsonExpr, textExpr string, fallback bool) string {
	reads := func(literal string, spellings ...string) string {
		quoted := make([]string, len(spellings))
		for i, s := range spellings {
			quoted[i] = "'" + s + "'"
		}
		return jsonExpr + " = '" + literal + "'::jsonb OR lower(trim(" + textExpr + ")) IN (" + strings.Join(quoted, ",") + ")"
	}
	if fallback {
		return "(CASE WHEN " + reads("false", "false", "f", "no", "n", "off", "0") + " THEN false ELSE true END)"
	}
	return "(CASE WHEN " + reads("true", "true", "t", "yes", "y", "on", "1") + " THEN true ELSE false END)"
}

// dateField names a request-body field holding a calendar date, with the label
// to use when telling the caller it is malformed.
type dateField struct{ key, label string }

// validDateFields checks optional YYYY-MM-DD values taken from a request body.
//
// Left to PostgreSQL, "2026-13-45" comes back as a failed cast, which the
// handler can only report as "could not save the data" — with no clue which of
// three date fields on the form was wrong — while writing a "database error"
// line to the log for what is plainly the caller's typo. Same reasoning as
// dateParam, applied on the way in.
func validDateFields(w http.ResponseWriter, in map[string]any, fields ...dateField) bool {
	for _, f := range fields {
		if !validDate(w, stringValue(in, f.key), f.label) {
			return false
		}
	}
	return true
}

func validDate(w http.ResponseWriter, value, label string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", label+topicParticle(label)+" YYYY-MM-DD 형식이어야 합니다")
		return false
	}
	return true
}

// maxIdentifierLen bounds the short free-text fields that name a record —
// 업체명, 대표자, 코드 — as opposed to notes and descriptions, which the 2 MB
// body limit covers. A text column would take a megabyte happily; the screen
// will not. One 5,000-character supplier name renders a 1,900-pixel-tall table
// row and follows the record into every export, dropdown and audit line.
const maxIdentifierLen = 200

// textField names a request-body field holding one of those labels, with the
// label to use when telling the caller it is too long.
type textField struct{ key, label string }

// validTextFields bounds the optional labels taken from a request body. Only
// the fields a caller actually sent are measured, the same way the date and
// number checks beside it leave an absent field to the statement's own default.
func validTextFields(w http.ResponseWriter, in map[string]any, fields ...textField) bool {
	for _, f := range fields {
		if !validText(w, stringValue(in, f.key), f.label) {
			return false
		}
	}
	return true
}

// validText bounds one label a typed handler has already decoded. Runes, not
// bytes: every Hangul syllable is three bytes, so a byte-length check would cut
// Korean names at a third of the limit it advertises.
func validText(w http.ResponseWriter, value, label string) bool {
	if utf8.RuneCountInString(strings.TrimSpace(value)) <= maxIdentifierLen {
		return true
	}
	writeError(w, http.StatusBadRequest, "validation_error",
		fmt.Sprintf("%s%s %d자를 넘을 수 없습니다", label, topicParticle(label), maxIdentifierLen))
	return false
}

// maxEmailLen bounds an address at the length the mail system itself does. RFC
// 5321 caps a forward path at 254 characters, so anything longer is not a long
// address but no address: nothing could be delivered to it however it is spelt.
const maxEmailLen = 254

// emailField names a request-body field holding an email address, with the
// label to use when telling the caller it is not one.
type emailField struct{ key, label string }

// validEmailFields checks the addresses a request body carries and rewrites
// each one into the form the rest of the application reads it in.
//
// An address is not free text the way a title is. It is a destination — the
// invitation link, the tax invoice, the verification mail all go where it says
// — and on users.email it is the account's identity: login selects
// `WHERE email=$1` on the trimmed, lower-cased value the sign-in form sends.
// Nothing between the request and the column agreed with that. The insert
// lower-cases but does not trim, so an admin who pastes "  gu@acme.co.kr " from
// a spreadsheet creates an account that no password will ever open, and whose
// only symptom is 자격 증명이 올바르지 않습니다 forever; the same stored space
// defeats the ON CONFLICT(email) that is supposed to attach the OIDC identity
// to the existing account, so signing in through Keycloak silently opens a
// second one. And nothing checked the shape at all, so "김구매" — the name, in
// the box above — saved without complaint on every one of these surfaces. Only
// the forms objected, through type="email", which is no help to the portal,
// the API client or the paste that skips the keystroke.
//
// So both halves are the caller's answer: the value is normalised to what the
// lookups use, and anything that is not an address is refused by the field the
// form shows it in. Only a field the caller actually sent is measured, the same
// way the date, number and label checks beside it leave an absent one to the
// statement's own default.
func validEmailFields(w http.ResponseWriter, in map[string]any, fields ...emailField) bool {
	for _, f := range fields {
		address, ok := validEmail(w, stringValue(in, f.key), f.label)
		if !ok {
			return false
		}
		if _, sent := in[f.key].(string); sent {
			in[f.key] = address
		}
	}
	return true
}

// validEmail checks one address a typed handler has already decoded and answers
// the form to store, which is the form login looks it up in.
func validEmail(w http.ResponseWriter, value, label string) (string, bool) {
	address := strings.ToLower(strings.TrimSpace(value))
	if address == "" {
		return "", true
	}
	if len(address) > maxEmailLen {
		writeError(w, http.StatusBadRequest, "validation_error",
			fmt.Sprintf("%s%s %d자를 넘을 수 없습니다", label, topicParticle(label), maxEmailLen))
		return "", false
	}
	if !isEmailAddress(address) {
		writeError(w, http.StatusBadRequest, "validation_error",
			label+topicParticle(label)+" 올바른 이메일 형식이 아닙니다")
		return "", false
	}
	return address, true
}

// isEmailAddress reports whether address is one, by the rules that matter to a
// procurement system rather than by RFC 5322's grammar — which also admits
// quoted local parts, nested comments and bracketed IP literals that no buyer
// has ever typed and no mail client would show back to them. Accepting those
// would buy nothing and cost the thing this check exists for.
//
// The local part is held to ASCII on purpose. An address in Hangul is not a
// thing anyone here has; a Hangul local part is what it looks like — the
// person's name, entered in the box below the one it belongs in.
func isEmailAddress(address string) bool {
	local, domain, ok := strings.Cut(address, "@")
	if !ok || strings.Contains(domain, "@") {
		return false
	}
	if local == "" || strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return false
	}
	for _, c := range local {
		if c <= ' ' || c >= 0x7f || strings.ContainsRune(`"(),:;<>[\]`, c) {
			return false
		}
	}
	// A domain has to carry a dot with something either side of it. "kim@사내"
	// and "kim@localhost" are hostnames on somebody's machine, not somewhere a
	// supplier's invitation can arrive.
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			alphanumeric := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
			if !alphanumeric && c != '-' {
				return false
			}
		}
	}
	return true
}

// uuidField names a request-body field holding the id of another record, with
// the label to use when telling the caller it is not one.
type uuidField struct{ key, label string }

// validUUIDFields checks the optional record ids taken from a request body.
//
// Every one of them reaches the statement as `$n::uuid`, and PostgreSQL answers
// a malformed id by failing the whole statement — which the handler can only
// pass on as "저장하지 못했습니다" with no field named, the same dead end the
// dates, numbers and labels were. The path ids have been checked at the router
// since app.go was written and the query filters since uuidParam; the ids a
// body carries were the third door and nobody stood at it.
//
// Worse than the message is where the check has to sit. A malformed id is
// currently caught, by accident, by the scope lookups: supplierScopeAllowed
// selects the row and treats a failed query as a denial, so a typo in
// supplierId comes back as 403 "데이터 접근 범위를 벗어났습니다" — the caller is
// told they lack permission for a record that does not exist. The same request
// from a company-scope account skips that lookup entirely and gets a 400
// instead. So these run before the scope checks: a value that is not an id is
// the caller's mistake, not a decision about what they may see.
func validUUIDFields(w http.ResponseWriter, in map[string]any, fields ...uuidField) bool {
	for _, f := range fields {
		if !validRecordID(w, stringValue(in, f.key), f.label) {
			return false
		}
	}
	return true
}

// validRecordID checks one id a typed handler has already decoded. As with the
// checks beside it, an id the caller left out keeps whatever default the
// statement applies and is not measured.
func validRecordID(w http.ResponseWriter, value, label string) bool {
	value = strings.TrimSpace(value)
	if value == "" || validUUID(value) {
		return true
	}
	writeError(w, http.StatusBadRequest, "validation_error", label+topicParticle(label)+" 올바른 형식이 아닙니다")
	return false
}

// riskGrades is the vocabulary every risk grade in the application is written
// in. It is not a display convention: the queries branch on the spelling.
//
// The award calculation scores a bid's supplier with
// `CASE risk_level WHEN 'LOW' THEN 100 WHEN 'MEDIUM' THEN 70 WHEN 'HIGH' THEN
// 30 ELSE 0 END`, so a supplier stored as "high" is not read as high risk but
// as worse than CRITICAL, and loses the tender to that. The dashboard's
// high-risk count is `risk_level IN('HIGH','CRITICAL')` and the recommendation
// tool excludes `risk_level NOT IN('CRITICAL')`, so the same record is missing
// from the warning and present in the shortlist. On a business object the grade
// is what an approval rule routes on, and a rule matching HIGH never sees
// "High": the submission finds no matching workflow and is approved on the
// spot, with no approver and nothing to notice.
//
// The forms offer exactly these four, upper case, everywhere they offer any.
var riskGrades = []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}

// enumField names a request-body field whose value has to be one of a fixed
// set, with the label to use when telling the caller it is not.
type enumField struct {
	key, label string
	allowed    []string
}

// riskGradeField describes one of the fields carrying a risk grade.
func riskGradeField(key, label string) enumField {
	return enumField{key: key, label: label, allowed: riskGrades}
}

// supplierStatuses is the vocabulary a supplier's 거래 상태 is written in. Like
// the risk grades, these are not labels but words the queries branch on: the
// dashboard's 거래 가능 tile counts status='active' and its 심사 대기 tile
// status='screening', the recommendation tool shortlists only
// status IN('active','approved'), and the register's status filter selects on
// the spelling exactly.
//
// Every status the application writes for itself is on this list — 'candidate'
// is what a supplier is created as, 'registration' is what portal self-signup
// inserts, and completing a screening moves the supplier to 'screening',
// 'approved' or 'suspended'. The remaining four are what the edit form offers a
// buyer, and that form is where the vocabulary had already split: its dropdown
// said "registered", a spelling that exists nowhere else in the application.
// Saving it produced a supplier the 등록 filter never returns, with no label and
// the wrong badge colour; and because "registration" was not among the options,
// opening any self-registered supplier and pressing save silently reset it to
// 후보 — the browser selects the first option when the current value is not one.
var supplierStatuses = []string{
	"candidate", "registration", "screening", "approved", "active",
	"preferred", "improvement", "suspended", "terminated",
}

// supplierStatusField describes a field carrying one of those statuses.
func supplierStatusField(key, label string) enumField {
	return enumField{key: key, label: label, allowed: supplierStatuses}
}

// validEnumFields checks the optional controlled-vocabulary fields of a request
// body. As with the date, number and text checks beside it, a field the caller
// left out keeps whatever default the statement applies and is not measured.
func validEnumFields(w http.ResponseWriter, in map[string]any, fields ...enumField) bool {
	for _, f := range fields {
		if !validEnum(w, stringValue(in, f.key), f) {
			return false
		}
	}
	return true
}

// validEnum checks one value a typed handler has already decoded. A value
// outside the set is the caller's mistake and is answered as one: stored, it is
// not a different grade but no grade at all, and every query that branches on
// the spelling silently takes its ELSE.
func validEnum(w http.ResponseWriter, value string, f enumField) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, allowed := range f.allowed {
		if value == allowed {
			return true
		}
	}
	writeError(w, http.StatusBadRequest, "validation_error",
		fmt.Sprintf("%s%s %s 중 하나여야 합니다", f.label, topicParticle(f.label), strings.Join(f.allowed, ", ")))
	return false
}

// maxAmount bounds the money and quantity a caller writes into the ledger.
//
// Two ceilings sit above a request number, and this is under both. The columns
// are numeric(20,2) and numeric(20,4), so 10^16 already overflows the narrower
// one — and PostgreSQL reports that as a failed statement, which the handler
// can only pass on as "저장하지 못했습니다" with no field named, the same dead
// end the dates were. Lower still, a JSON number is decoded into a float64,
// and past 2^53 it stops being the number the caller sent: 9007199254740993
// arrives as ...992, so the ledger would hold an amount the invoice does not.
// Under 10^15 every accepted value is stored as written, and 10^15 KRW is
// three orders of magnitude above the largest transaction anyone books.
const maxAmount = 1e15

// numberField names a request-body field holding a number, with the label and
// the range to use when telling the caller the value is outside it.
type numberField struct {
	key, label string
	min, max   float64
}

// amountField describes a money or quantity field, which runs from zero to
// maxAmount. The forms mark every one of them min="0"; only the API took a
// negative, and a negative amount is not a smaller purchase but a wrong one —
// it subtracts from the spend rollup, the concentration report and the
// supplier's annual spend, and slips under every minAmount an approval rule
// routes on.
func amountField(key, label string) numberField {
	return numberField{key: key, label: label, min: 0, max: maxAmount}
}

// validNumberFields checks optional numbers taken from a request body. A field
// the caller left out keeps whatever default the statement applies, so only a
// value that is actually present is ranged.
func validNumberFields(w http.ResponseWriter, in map[string]any, fields ...numberField) bool {
	for _, f := range fields {
		value, ok := numberValue(in, f.key).(float64)
		if !ok {
			continue
		}
		if !validNumber(w, value, f) {
			return false
		}
	}
	return true
}

// validNumber ranges one number that a typed handler has already decoded.
func validNumber(w http.ResponseWriter, value float64, f numberField) bool {
	if value >= f.min && value <= f.max {
		return true
	}
	writeError(w, http.StatusBadRequest, "validation_error",
		fmt.Sprintf("%s%s %s에서 %s 사이여야 합니다", f.label, topicParticle(f.label), groupDigits(f.min), groupDigits(f.max)))
	return false
}

// groupDigits writes a bound the way the message needs to read it: 100 stays
// 100, and maxAmount is unreadable as 1000000000000000.
func groupDigits(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	var b strings.Builder
	for i := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(whole[i])
	}
	if hasFrac {
		b.WriteString("." + frac)
	}
	return sign + b.String()
}

// validInstant is validDate for a field carrying a point in time. The spellings
// listed are the ones an API client actually sends; anything else was going to
// be stored ambiguously or rejected by the cast anyway.
func validInstant(w http.ResponseWriter, value, label string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	writeError(w, http.StatusBadRequest, "validation_error", label+topicParticle(label)+" YYYY-MM-DD 또는 RFC3339 형식이어야 합니다")
	return false
}

// uuidParam reads an optional record-id filter. A malformed id is the caller's
// mistake in the same way a malformed date is: without this it reaches
// `$1::uuid` and PostgreSQL rejects it, which the handler could only pass on
// as a 500.
func uuidParam(w http.ResponseWriter, r *http.Request, name, label string) (string, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if !validRecordID(w, value, label) {
		return "", false
	}
	return value, true
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
		writeError(w, http.StatusBadRequest, "validation_error", label+topicParticle(label)+" YYYY-MM-DD 형식이어야 합니다")
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

// orgInScope renders the data-scope predicate as inline SQL instead of calling
// vendra_org_in_scope, which PostgreSQL evaluates once per row.
//
// A SQL function is only inlined when its body has no subquery, and the
// division branch needs one to walk the organisation tree — so every scoped
// scan paid a function call per row. Written out, the descendant lookup is
// uncorrelated, and the planner hoists it into a single hashed subplan.
//
// Measured over 100,000 business objects:
//
//	scope        function   inline
//	company        127 ms    7.4 ms
//	department     238 ms    7.0 ms
//	division       463 ms    8.9 ms
//
// The function itself is kept: the single-row checks call it once, where the
// per-row cost does not arise.
func orgInScope(column, scopeParam, orgParam string) string {
	return "(" + scopeParam + "='company'" +
		" OR (" + scopeParam + "='department' AND " + column + "=NULLIF(" + orgParam + ",'')::uuid)" +
		" OR (" + scopeParam + "='division' AND " + column + " IN (SELECT vo.id FROM organizations vo, organizations vp" +
		" WHERE vp.id=NULLIF(" + orgParam + ",'')::uuid AND (vo.path||vo.id||'/') LIKE (vp.path||vp.id||'/')||'%')))"
}
