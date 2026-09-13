package tracking

import (
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxViolations bounds the recorder. A blocked request repeats on every page
// view, so the interesting information is which origins are blocked, not how
// many times — a small ring of distinct origins is enough to fix a snippet.
const MaxViolations = 100

// Violation is one origin the content security policy refused, kept with the
// directive that refused it so the screen can say what to allow.
type Violation struct {
	Origin    string    `json:"origin"`
	Directive string    `json:"directive"`
	Page      string    `json:"page"`
	Count     int       `json:"count"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	// Allowed marks an origin the current configuration already permits, so a
	// fixed snippet stops nagging without anybody clearing the list.
	Allowed bool `json:"allowed"`
}

// Recorder collects the policy violations browsers report. It is deliberately
// in memory: the reports are a live troubleshooting aid for the person pasting
// a snippet, not an audit record, and keeping them out of the database means
// an unauthenticated endpoint can accept them without growing storage.
type Recorder struct {
	mutex      sync.Mutex
	violations map[string]*Violation
	now        func() time.Time
}

func NewRecorder() *Recorder {
	return &Recorder{violations: make(map[string]*Violation), now: time.Now}
}

// Record notes one blocked request. Anything that is not an http origin — a
// browser extension, a data: URL, an inline script — is ignored, because
// allowing it is neither possible nor what an allow list is for.
func (r *Recorder) Record(blockedURI, directive, page string) {
	if r == nil {
		return
	}
	origin := originOf(blockedURI)
	if origin == "" || !strings.HasPrefix(origin, "http") {
		return
	}
	directive = strings.TrimSpace(strings.ToLower(directive))
	if index := strings.IndexByte(directive, ' '); index > 0 {
		directive = directive[:index]
	}
	if directive == "" {
		directive = "connect-src"
	}
	if parsed, err := url.Parse(page); err == nil && parsed.Path != "" {
		// The page is kept as a path: the host is ours and the query may carry
		// a search term or a record id that has no business in this list.
		page = parsed.Path
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	key := directive + " " + origin
	if existing, found := r.violations[key]; found {
		existing.Count++
		existing.LastSeen = r.now()
		existing.Page = page
		return
	}
	if len(r.violations) >= MaxViolations {
		r.evictOldest()
	}
	moment := r.now()
	r.violations[key] = &Violation{Origin: origin, Directive: directive, Page: page, Count: 1, FirstSeen: moment, LastSeen: moment}
}

func (r *Recorder) evictOldest() {
	var oldestKey string
	var oldest time.Time
	for key, violation := range r.violations {
		if oldestKey == "" || violation.LastSeen.Before(oldest) {
			oldestKey, oldest = key, violation.LastSeen
		}
	}
	delete(r.violations, oldestKey)
}

// List returns the blocked origins, most recent first, marking the ones the
// configuration already allows.
func (r *Recorder) List(config Config) []Violation {
	if r == nil {
		return []Violation{}
	}
	allowed := make(map[string]struct{})
	scripts, connects, images := config.PolicySources()
	for _, group := range [][]string{scripts, connects, images} {
		for _, origin := range group {
			allowed[strings.ToLower(strings.TrimSuffix(origin, "/"))] = struct{}{}
		}
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	items := make([]Violation, 0, len(r.violations))
	for _, violation := range r.violations {
		copied := *violation
		_, known := allowed[strings.ToLower(copied.Origin)]
		copied.Allowed = known || matchesWildcard(copied.Origin, allowed)
		items = append(items, copied)
	}
	sort.Slice(items, func(first, second int) bool {
		if items[first].LastSeen.Equal(items[second].LastSeen) {
			return items[first].Origin < items[second].Origin
		}
		return items[first].LastSeen.After(items[second].LastSeen)
	})
	return items
}

// Forget drops the recorded violations, which is what an administrator does
// after fixing a snippet to see whether anything is still blocked.
func (r *Recorder) Forget() {
	if r == nil {
		return
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.violations = make(map[string]*Violation)
}

// matchesWildcard covers policy entries such as https://*.google-analytics.com.
func matchesWildcard(origin string, allowed map[string]struct{}) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	lowered := strings.ToLower(origin)
	host := strings.ToLower(parsed.Host)
	for pattern := range allowed {
		star := strings.Index(pattern, "*.")
		if star < 0 {
			continue
		}
		if strings.HasPrefix(lowered, pattern[:star]) && strings.HasSuffix(host, pattern[star+1:]) {
			return true
		}
	}
	return false
}
