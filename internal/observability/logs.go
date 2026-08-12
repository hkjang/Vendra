package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultCapacity = 3000

type Entry struct {
	ID         uint64         `json:"id"`
	OccurredAt time.Time      `json:"occurredAt"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Attributes map[string]any `json:"attributes"`
}

type Stats struct {
	Retained int `json:"retained"`
	Debug    int `json:"debug"`
	Info     int `json:"info"`
	Warning  int `json:"warning"`
	Error    int `json:"error"`
}

type Store struct {
	mu       sync.RWMutex
	entries  []Entry
	capacity int
	nextID   atomic.Uint64
	started  time.Time
}

func NewStore(capacity int) *Store {
	if capacity < 100 {
		capacity = defaultCapacity
	}
	return &Store{capacity: capacity, started: time.Now().UTC()}
}

func (s *Store) Add(entry Entry) {
	entry.ID = s.nextID.Add(1)
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == s.capacity {
		copy(s.entries, s.entries[1:])
		s.entries[len(s.entries)-1] = entry
		return
	}
	s.entries = append(s.entries, entry)
}

func (s *Store) Query(level, query string, limit int) ([]Entry, Stats) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	level = strings.ToUpper(strings.TrimSpace(level))
	query = strings.ToLower(strings.TrimSpace(query))
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := Stats{Retained: len(s.entries)}
	for _, entry := range s.entries {
		switch entry.Level {
		case "DEBUG":
			stats.Debug++
		case "WARN":
			stats.Warning++
		case "ERROR":
			stats.Error++
		default:
			stats.Info++
		}
	}
	items := make([]Entry, 0, min(limit, len(s.entries)))
	for index := len(s.entries) - 1; index >= 0 && len(items) < limit; index-- {
		entry := s.entries[index]
		if level != "" && level != "ALL" && entry.Level != level {
			continue
		}
		if query != "" {
			attributes, _ := json.Marshal(entry.Attributes)
			if !strings.Contains(strings.ToLower(entry.Message+" "+string(attributes)), query) {
				continue
			}
		}
		items = append(items, entry)
	}
	return items, stats
}

func (s *Store) Capacity() int        { return s.capacity }
func (s *Store) StartedAt() time.Time { return s.started }

var defaultStore = NewStore(defaultCapacity)

func DefaultStore() *Store { return defaultStore }

type captureHandler struct {
	primary slog.Handler
	store   *Store
	attrs   map[string]any
	groups  []string
}

func NewCaptureHandler(primary slog.Handler, store *Store) slog.Handler {
	if store == nil {
		store = defaultStore
	}
	return &captureHandler{primary: primary, store: store}
}

func (h *captureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.primary.Enabled(ctx, level)
}

func (h *captureHandler) Handle(ctx context.Context, record slog.Record) error {
	attributes := map[string]any{}
	maps.Copy(attributes, h.attrs)
	record.Attrs(func(attr slog.Attr) bool {
		captureAttr(attributes, strings.Join(h.groups, "."), attr)
		return true
	})
	h.store.Add(Entry{OccurredAt: record.Time.UTC(), Level: record.Level.String(), Message: record.Message, Attributes: attributes})
	return h.primary.Handle(ctx, record)
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.primary = h.primary.WithAttrs(attrs)
	clone.attrs = maps.Clone(h.attrs)
	if clone.attrs == nil {
		clone.attrs = map[string]any{}
	}
	for _, attr := range attrs {
		captureAttr(clone.attrs, strings.Join(h.groups, "."), attr)
	}
	return &clone
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.primary = h.primary.WithGroup(name)
	clone.groups = append(append([]string{}, h.groups...), name)
	return &clone
}

func captureAttr(target map[string]any, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	key := attr.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if attr.Value.Kind() == slog.KindGroup {
		for _, child := range attr.Value.Group() {
			captureAttr(target, key, child)
		}
		return
	}
	if sensitiveKey(key) {
		target[key] = "[REDACTED]"
		return
	}
	switch attr.Value.Kind() {
	case slog.KindTime:
		target[key] = attr.Value.Time().UTC().Format(time.RFC3339Nano)
	case slog.KindDuration:
		target[key] = attr.Value.Duration().String()
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok {
			target[key] = err.Error()
		} else {
			target[key] = jsonSafeValue(attr.Value.Any())
		}
	default:
		target[key] = attr.Value.Any()
	}
}

func jsonSafeValue(value any) any {
	if _, err := json.Marshal(value); err != nil {
		return fmt.Sprint(value)
	}
	return value
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, word := range []string{"password", "secret", "token", "authorization", "cookie", "credential", "body", "dsn", "private_key", "access_key", "encryption_key", "api_key"} {
		if strings.Contains(key, word) {
			return true
		}
	}
	return false
}
