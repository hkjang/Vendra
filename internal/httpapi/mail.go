package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Vendra/internal/mail"
	"github.com/hkjang/Vendra/internal/security"
)

// mailDirectory answers the one question the mail service asks about
// accounts — which address does this id have — from the users table the
// rest of the application already reads. A disabled account gets no mail.
type mailDirectory struct{ db *pgxpool.Pool }

func (d mailDirectory) Emails(ctx context.Context, ids []string) (map[string]string, error) {
	rows, err := d.db.Query(ctx, `SELECT id::text,email FROM users WHERE id=ANY($1::uuid[]) AND status='active'`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	addresses := map[string]string{}
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			return nil, err
		}
		addresses[id] = email
	}
	return addresses, rows.Err()
}

// newMailService wires the relay to the settings rows and the user table.
func newMailService(pool *pgxpool.Pool, vault *security.Vault) *mail.Service {
	load := func(ctx context.Context) (mail.Config, error) {
		return loadMailConfig(ctx, pool, vault)
	}
	return mail.NewService(pool, load, mailDirectory{db: pool})
}

// loadMailConfig reads the mail.* rows. The password is the only secret and
// comes out of secret_value through the vault; it goes into the Config and
// nowhere else — not the settings API, not the log.
func loadMailConfig(ctx context.Context, pool *pgxpool.Pool, vault *security.Vault) (mail.Config, error) {
	rows, err := pool.Query(ctx, `SELECT key,value,secret_value FROM settings WHERE key LIKE 'mail.%'`)
	if err != nil {
		return mail.Config{}, err
	}
	defer rows.Close()
	values := map[string]json.RawMessage{}
	var cipher *string
	for rows.Next() {
		var key string
		var value []byte
		var secret *string
		if err := rows.Scan(&key, &value, &secret); err != nil {
			return mail.Config{}, err
		}
		values[key] = json.RawMessage(value)
		if key == mail.KeyPassword {
			cipher = secret
		}
	}
	if err := rows.Err(); err != nil {
		return mail.Config{}, err
	}
	config := mail.Parse(values)
	if cipher != nil && *cipher != "" && vault != nil {
		password, err := vault.Decrypt(*cipher)
		if err != nil {
			return mail.Config{}, fmt.Errorf("decrypt %s: %w", mail.KeyPassword, err)
		}
		config.Password = password
	}
	return config, nil
}

// notifyMail sends one event mail. Every caller is on a request path or in
// the background loop, and neither waits on the relay.
func (a *App) notifyMail(ctx context.Context, notification mail.Notification, actorID string, recipients []string) {
	if a.mail == nil || len(recipients) == 0 {
		return
	}
	a.mail.Notify(ctx, notification, actorID, recipients)
}

// approvalStepRecipients is who sees a request at its current step: active
// internal accounts holding the step's role whose data scope reaches the
// object — the same people listApprovals would show it to. A step with no
// role goes to whoever holds the approve permission in scope. Administrators
// with `*` can act on any step but are not written to for every one.
func (a *App) approvalStepRecipients(ctx context.Context, instanceID string, step map[string]any) ([]string, error) {
	role, _ := step["role"].(string)
	rows, err := a.db.Query(ctx, `SELECT u.id::text FROM users u
	 JOIN workflow_instances i ON i.id=$1
	 LEFT JOIN business_objects o ON o.id=i.object_id
	 CROSS JOIN LATERAL (SELECT COALESCE((SELECT CASE max(CASE r.data_scope WHEN 'company' THEN 4 WHEN 'division' THEN 3 WHEN 'department' THEN 2 ELSE 1 END) WHEN 4 THEN 'company' WHEN 3 THEN 'division' WHEN 2 THEN 'department' ELSE 'own' END FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id),'own') AS scope) s
	 WHERE u.status='active' AND u.user_type='internal'
	   AND EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND CASE WHEN $2<>'' THEN r.code=$2 ELSE r.permissions ?| ARRAY['workflow.approve','workflow.*','*'] END)
	   AND (vendra_org_in_scope(o.organization_id,s.scope,u.organization_id) OR (s.scope='own' AND (o.owner_id=u.id OR i.requested_by=u.id)))
	 ORDER BY u.id`, instanceID, strings.TrimSpace(role))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// approvalSubject is what the mail says the request is, read from the
// object rather than the request body so the words are the stored ones.
type approvalSubject struct {
	ObjectType, ObjectID, Label, Number, Title, Amount, Path string
	OwnerID, RequestedBy                                     string
}

func (a *App) approvalSubject(ctx context.Context, instanceID string) (approvalSubject, error) {
	var s approvalSubject
	var amount *float64
	var currency string
	var owner, requester *string
	err := a.db.QueryRow(ctx, `SELECT i.object_type,i.object_id::text,COALESCE(o.number,''),COALESCE(o.title,''),o.amount,COALESCE(o.currency,''),o.owner_id::text,i.requested_by::text FROM workflow_instances i LEFT JOIN business_objects o ON o.id=i.object_id WHERE i.id=$1`, instanceID).
		Scan(&s.ObjectType, &s.ObjectID, &s.Number, &s.Title, &amount, &currency, &owner, &requester)
	if err != nil {
		return s, err
	}
	if owner != nil {
		s.OwnerID = *owner
	}
	if requester != nil {
		s.RequestedBy = *requester
	}
	s.Label = objectTypeLabel(s.ObjectType)
	if amount != nil {
		s.Amount = strings.TrimSpace(strconv.FormatFloat(*amount, 'f', -1, 64) + " " + currency)
	}
	for _, route := range objectRoutes {
		if route.objectType == s.ObjectType {
			s.Path = strings.TrimPrefix(route.path, "/api/v1")
		}
	}
	return s, nil
}

// objectTypeLabel is the Korean name a type is shown under. The mail cannot
// say "purchase_order" to somebody who has only ever seen 발주.
func objectTypeLabel(objectType string) string {
	labels := map[string]string{
		"contract": "계약", "purchase_request": "구매요청", "rfq": "RFQ", "rfp": "RFP", "purchase_order": "발주",
		"delivery": "납품", "inspection": "검수", "quality": "품질", "issue": "이슈", "invoice": "Invoice",
		"payment": "지급", "supplier_bank_change": "계좌정보 변경",
	}
	if label, ok := labels[objectType]; ok {
		return label
	}
	return objectType
}

// displayName is what the mail calls a person.
func (a *App) displayName(ctx context.Context, userID string) string {
	var name string
	if err := a.db.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, userID).Scan(&name); err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return "알 수 없는 사용자"
}

// notifyApprovalRequested writes to the people whose turn the request has
// reached. Called when a request opens and each time it advances a step.
func (a *App) notifyApprovalRequested(ctx context.Context, instanceID string, step map[string]any, actorID string) {
	if a.mail == nil {
		return
	}
	recipients, err := a.approvalStepRecipients(ctx, instanceID, step)
	if err != nil {
		logDB(err)
		return
	}
	if len(recipients) == 0 {
		return
	}
	subject, err := a.approvalSubject(ctx, instanceID)
	if err != nil {
		logDB(err)
		return
	}
	requester := subject.RequestedBy
	if requester == "" {
		requester = actorID
	}
	stepName, _ := step["name"].(string)
	a.notifyMail(ctx, mail.ApprovalRequested(subject.ObjectType, subject.ObjectID, subject.Label, subject.Number, subject.Title,
		a.displayName(ctx, requester), stepName, subject.Amount), actorID, recipients)
}

// notifyApprovalDecided tells the requester — and the owner, when that is
// somebody else — how their request ended. The approver is not written to
// for their own decision.
func (a *App) notifyApprovalDecided(ctx context.Context, instanceID, action, comment, actorID string) {
	if a.mail == nil {
		return
	}
	subject, err := a.approvalSubject(ctx, instanceID)
	if err != nil {
		logDB(err)
		return
	}
	a.notifyMail(ctx, mail.ApprovalDecided(subject.ObjectType, subject.ObjectID, subject.Label, subject.Number, subject.Title,
		a.displayName(ctx, actorID), action, comment, subject.Path), actorID, []string{subject.RequestedBy, subject.OwnerID})
}

// digestEvents maps what the background pass raises to the mail switch it is
// under. A kind not listed here is in-app only.
var digestEvents = map[string]string{
	"contract_expiry": mail.EventExpiry, "document_expiry": mail.EventExpiry, "evaluation_due": mail.EventExpiry,
	"sla_breach": mail.EventAlert, "contract_amount_exceeded": mail.EventAlert,
}

// mailDigests bundles the rows one scheduling pass created into one mail per
// person per event: ten expiring contracts are one message. Only rows the
// pass actually inserted are read (ON CONFLICT DO NOTHING RETURNING), so a
// notification is mailed once, on the pass that raised it.
func (a *App) mailDigests(ctx context.Context, raised []raisedNotification) {
	if a.mail == nil || len(raised) == 0 {
		return
	}
	config, err := a.mail.Config(ctx)
	if err != nil || !config.Enabled {
		return
	}
	type key struct{ user, event string }
	groups := map[key][]mail.DigestItem{}
	var order []key
	for _, n := range raised {
		event, ok := digestEvents[n.kind]
		if !ok {
			continue
		}
		k := key{n.userID, event}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], mail.DigestItem{Title: n.title, Body: n.body})
	}
	for _, k := range order {
		a.mail.Send(ctx, config, mail.Digest(k.event, groups[k]), "", []string{k.user})
	}
}

// raisedNotification is one row a scheduling statement inserted.
type raisedNotification struct{ userID, kind, title, body string }

// insertRaised runs one scheduling INSERT and collects the rows it actually
// created. The statement must end in ON CONFLICT … DO NOTHING; RETURNING is
// appended here so every statement reports the same columns.
func (a *App) insertRaised(ctx context.Context, statement string, raised *[]raisedNotification) error {
	rows, err := a.db.Query(ctx, statement+` RETURNING user_id::text,kind,title,body`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n raisedNotification
		if err := rows.Scan(&n.userID, &n.kind, &n.title, &n.body); err != nil {
			return err
		}
		*raised = append(*raised, n)
	}
	return rows.Err()
}

// adminMailDeliveries lists what went out.
func (a *App) adminMailDeliveries(w http.ResponseWriter, r *http.Request) {
	if a.mail == nil {
		writeJSON(w, 200, mail.Page{Items: []mail.Delivery{}, Status: map[string]int{}})
		return
	}
	page, err := a.mail.Deliveries(r.Context(), r.URL.Query().Get("status"), parseLimit(r, 100))
	if err != nil {
		logDB(err)
		writeError(w, 500, "database_error", "발송 기록을 조회하지 못했습니다")
		return
	}
	writeJSON(w, 200, page)
}

// adminSendTestMail sends one real message with the saved settings and
// reports what the relay said, so the configuration can be proven on the
// spot. It works with mail.enabled off — that is the point of a test.
func (a *App) adminSendTestMail(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var in struct {
		Recipient string `json:"recipient"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	recipient := strings.TrimSpace(in.Recipient)
	if recipient == "" {
		recipient = p.Email
	}
	recipient, ok := validEmail(w, recipient, "받는 사람")
	if !ok {
		return
	}
	if recipient == "" {
		writeError(w, 400, "validation_error", "받는 사람 주소가 필요합니다")
		return
	}
	if a.mail == nil {
		writeError(w, 503, "mail_unavailable", "메일 서비스가 구성되지 않았습니다")
		return
	}
	err := a.mail.SendNow(r.Context(), mail.TestMessage(), p.ID, recipient)
	a.audit.record(r, "mail_test", "setting", mail.KeyHost, nil, map[string]any{"recipient": recipient, "sent": err == nil})
	if errors.Is(err, mail.ErrInvalid) {
		writeError(w, 400, "validation_error", strings.TrimPrefix(err.Error(), mail.ErrInvalid.Error()+": "))
		return
	}
	if err != nil {
		writeError(w, 502, "mail_send_failed", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sent": true, "recipient": recipient})
}
