package mail

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Loader reads the stored configuration, password included.
type Loader func(context.Context) (Config, error)

// Directory turns account ids into addresses. The application already knows
// its users; mail borrows that one lookup and keeps no list of its own.
type Directory interface {
	Emails(ctx context.Context, ids []string) (map[string]string, error)
}

// Delivery is one attempt, as the administrator sees it. The body is not in
// it: subject and recipient answer "did it go out", and a log that carries
// bodies is a leak waiting for a reader.
type Delivery struct {
	ID           string    `json:"id"`
	Event        string    `json:"event"`
	Recipient    string    `json:"recipient"`
	Subject      string    `json:"subject"`
	ObjectType   string    `json:"objectType,omitempty"`
	ObjectID     string    `json:"objectId,omitempty"`
	ActorID      string    `json:"actorId,omitempty"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusFailed = "failed"
)

// Page is the delivery log with a status breakdown.
type Page struct {
	Items     []Delivery     `json:"items"`
	Total     int            `json:"total"`
	Status    map[string]int `json:"status"`
	Limit     int            `json:"limit"`
	Truncated bool           `json:"truncated"`
}

// Service sends notifications without holding the request that raised them.
type Service struct {
	pool      *pgxpool.Pool
	load      Loader
	directory Directory
	send      func(context.Context, Config, Message) error
	inFlight  sync.WaitGroup
}

func NewService(pool *pgxpool.Pool, load Loader, directory Directory) *Service {
	return &Service{pool: pool, load: load, directory: directory, send: Deliver}
}

// SetSender swaps the transport, which is how tests drive the service
// without a relay.
func (s *Service) SetSender(send func(context.Context, Config, Message) error) { s.send = send }

// Wait blocks until every background delivery started so far has been
// recorded. Tests use it; the server never needs to.
func (s *Service) Wait() { s.inFlight.Wait() }

func (s *Service) Config(ctx context.Context) (Config, error) {
	if s.load == nil {
		return Default(), nil
	}
	return s.load(ctx)
}

// Notify resolves the recipients and sends in the background. Nothing it does
// can fail the caller: a configuration problem is logged and recorded, and a
// relay that is down is discovered by the goroutine, not the request.
func (s *Service) Notify(ctx context.Context, notification Notification, actorID string, recipientIDs []string) {
	config, err := s.Config(ctx)
	if err != nil {
		slog.Warn("mail configuration could not be read, so nothing was sent", "event", notification.Event, "error", err)
		return
	}
	s.Send(ctx, config, notification, actorID, recipientIDs)
}

// Send is Notify with the configuration already in hand, for a caller that
// raises several notifications in one pass.
func (s *Service) Send(ctx context.Context, config Config, notification Notification, actorID string, recipientIDs []string) {
	if !config.Enabled || !config.Allows(notification.Event) {
		return
	}
	addresses := s.resolve(ctx, recipientIDs, actorID)
	if len(addresses) == 0 {
		return
	}
	// The request that raised the notification may finish before the
	// recording does; its cancellation must not turn the log into a gap.
	ctx = context.WithoutCancel(ctx)
	if err := config.Validate(); err != nil {
		// Switched on but not usable: say so where the administrator looks,
		// once per recipient so the count in the log is honest.
		slog.Warn("mail is enabled but cannot be sent", "event", notification.Event, "error", err)
		for _, address := range addresses {
			delivery := s.record(ctx, notification, actorID, address)
			s.complete(ctx, delivery, err)
		}
		return
	}
	body := notification.Render(config)
	for _, address := range addresses {
		delivery := s.record(ctx, notification, actorID, address)
		s.inFlight.Add(1)
		go s.deliver(delivery, config, Message{To: address, Subject: notification.Subject, Body: body})
	}
}

// SendNow delivers on the caller's clock and reports the outcome, which is
// what the administrator's test button needs. It sends whether or not the
// feature is switched on: the point is to prove the relay before turning it
// on for everyone.
func (s *Service) SendNow(ctx context.Context, notification Notification, actorID, recipient string) error {
	config, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if err := config.Validate(); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	delivery := s.record(ctx, notification, actorID, recipient)
	delivery.Attempts = 1
	sendCtx, cancel := context.WithTimeout(ctx, config.Timeout+5*time.Second)
	defer cancel()
	err = s.send(sendCtx, config, Message{To: recipient, Subject: notification.Subject, Body: notification.Render(config)})
	s.complete(ctx, delivery, err)
	return err
}

// deliver tries twice: a relay that briefly refuses a connection is common,
// and losing the notification is worse than a two-second wait.
func (s *Service) deliver(delivery Delivery, config Config, message Message) {
	defer s.inFlight.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 2*config.Timeout+15*time.Second)
	defer cancel()
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		delivery.Attempts = attempt
		if err = s.send(ctx, config, message); err == nil {
			break
		}
		if attempt == 1 {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
	s.complete(ctx, delivery, err)
}

func (s *Service) record(ctx context.Context, notification Notification, actorID, recipient string) Delivery {
	delivery := Delivery{Event: notification.Event, Recipient: recipient, Subject: notification.Subject,
		ObjectType: notification.ObjectType, ObjectID: notification.ObjectID, ActorID: actorID, Status: StatusQueued}
	if s.pool == nil {
		return delivery
	}
	if err := s.pool.QueryRow(ctx, `INSERT INTO mail_deliveries(event,recipient,subject,object_type,object_id,actor_id) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,'')::uuid,NULLIF($6,'')::uuid) RETURNING id`,
		delivery.Event, delivery.Recipient, trim(delivery.Subject, 300), delivery.ObjectType, delivery.ObjectID, delivery.ActorID).Scan(&delivery.ID); err != nil {
		slog.Warn("mail delivery was not recorded", "event", delivery.Event, "error", err)
	}
	return delivery
}

func (s *Service) complete(ctx context.Context, delivery Delivery, cause error) {
	status, message := StatusSent, ""
	if cause != nil {
		status, message = StatusFailed, cause.Error()
		// The recipient is an address the directory already holds; the
		// password never reaches this line.
		slog.Warn("notification mail failed", "event", delivery.Event, "recipient", delivery.Recipient, "attempts", delivery.Attempts, "error", cause)
	}
	if s.pool == nil || delivery.ID == "" {
		return
	}
	if _, err := s.pool.Exec(ctx, `UPDATE mail_deliveries SET status=$2,attempts=$3,error_message=NULLIF($4,''),updated_at=now() WHERE id=$1`,
		delivery.ID, status, max(delivery.Attempts, 1), trim(message, 1000)); err != nil {
		slog.Warn("mail delivery status was not recorded", "error", err)
	}
}

// resolve turns account ids into unique addresses, dropping the actor so
// nobody is told about their own action.
func (s *Service) resolve(ctx context.Context, recipientIDs []string, actorID string) []string {
	actor := strings.TrimSpace(actorID)
	wanted := make([]string, 0, len(recipientIDs))
	seenID := map[string]bool{}
	for _, id := range recipientIDs {
		id = strings.TrimSpace(id)
		if id == "" || strings.EqualFold(id, actor) || seenID[strings.ToLower(id)] {
			continue
		}
		seenID[strings.ToLower(id)] = true
		wanted = append(wanted, id)
	}
	if len(wanted) == 0 || s.directory == nil {
		return nil
	}
	emails, err := s.directory.Emails(ctx, wanted)
	if err != nil {
		slog.Warn("mail recipients were not resolved", "error", err)
		return nil
	}
	seen := map[string]bool{}
	addresses := make([]string, 0, len(wanted))
	for _, id := range wanted {
		address := strings.TrimSpace(emails[id])
		if address == "" {
			continue
		}
		if key := strings.ToLower(address); !seen[key] {
			seen[key] = true
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// Deliveries lists what went out, newest first, with a status breakdown.
func (s *Service) Deliveries(ctx context.Context, status string, limit int) (Page, error) {
	page := Page{Items: []Delivery{}, Status: map[string]int{}, Limit: limit}
	if s.pool == nil {
		return page, nil
	}
	query := `SELECT id::text,event,recipient,subject,COALESCE(object_type,''),COALESCE(object_id::text,''),COALESCE(actor_id::text,''),status,attempts,COALESCE(error_message,''),created_at,updated_at FROM mail_deliveries`
	args := []any{limit + 1}
	if status = strings.TrimSpace(status); status != "" {
		query += ` WHERE status=$2`
		args = append(args, status)
	}
	rows, err := s.pool.Query(ctx, query+` ORDER BY created_at DESC,id LIMIT $1`, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Delivery
		if err := rows.Scan(&item.ID, &item.Event, &item.Recipient, &item.Subject, &item.ObjectType, &item.ObjectID, &item.ActorID,
			&item.Status, &item.Attempts, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Items) > limit {
		page.Items, page.Truncated = page.Items[:limit], true
	}
	counts, err := s.pool.Query(ctx, `SELECT status,count(*) FROM mail_deliveries GROUP BY 1`)
	if err != nil {
		return Page{}, err
	}
	defer counts.Close()
	for counts.Next() {
		var key string
		var count int
		if err := counts.Scan(&key, &count); err != nil {
			return Page{}, err
		}
		page.Status[key] = count
		page.Total += count
	}
	return page, counts.Err()
}

func trim(value string, limit int) string {
	if runes := []rune(value); len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
