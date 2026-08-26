package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const (
	principalKey          contextKey = "principal"
	requestIDKey          contextKey = "request_id"
	grantAuthorizationKey contextKey = "grant_authorization"
)

type Principal struct {
	ID             string        `json:"id"`
	Email          string        `json:"email"`
	DisplayName    string        `json:"displayName"`
	UserType       string        `json:"userType"`
	SupplierID     *string       `json:"supplierId,omitempty"`
	OrganizationID *string       `json:"organizationId,omitempty"`
	Permissions    []string      `json:"permissions"`
	DataScope      string        `json:"dataScope"`
	SessionID      *string       `json:"-"`
	AccessGrants   []AccessGrant `json:"-"`
}

type AccessGrant struct {
	Permission   string
	ResourceType string
	ResourceID   *string
	Conditions   map[string]any
}

func principalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

func requestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func grantAuthorized(ctx context.Context) bool {
	value, _ := ctx.Value(grantAuthorizationKey).(bool)
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func decodeJSON(r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 2<<20))
	if err != nil {
		return err
	}
	// A text column cannot hold a NUL byte, so a payload carrying one dies
	// inside whatever query the handler runs next and comes back as a 500 with
	// "database error" in the log — for what is plainly bad input. Refuse it
	// here, where it is still recognisable as such.
	if bytes.Contains(body, []byte(`\u0000`)) || bytes.IndexByte(body, 0) >= 0 {
		return errors.New("입력에 저장할 수 없는 문자(NUL)가 있습니다")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func hasPermission(p Principal, wanted string) bool {
	for _, got := range p.Permissions {
		if permissionMatches(got, wanted) {
			return true
		}
	}
	return false
}

func permissionMatches(got, wanted string) bool {
	return got == "*" || got == wanted ||
		(strings.HasSuffix(got, ".*") && strings.HasPrefix(wanted, strings.TrimSuffix(got, "*"))) ||
		(strings.HasPrefix(got, "*.") && strings.HasSuffix(wanted, strings.TrimPrefix(got, "*")))
}

// hasGrantPermission reports whether a delegation lets this request past the
// permission gate, and whether that delegation named the very record being
// acted on.
//
// The distinction is the point. A delegation grants a permission; it does not
// enlarge the data scope the permission runs inside. Only a delegation naming a
// specific record may carry the caller past a scope check, and then only for
// that record. Delegating "contract.read over contracts" used to raise the same
// bypass flag and hand over every contract in the company.
func hasGrantPermission(p Principal, wanted string, r *http.Request) (allowed, namesRecord bool) {
	for _, grant := range p.AccessGrants {
		if !permissionMatches(grant.Permission, wanted) {
			continue
		}
		if grant.ResourceType != "" && grant.ResourceType != "*" {
			domain, _, _ := strings.Cut(wanted, ".")
			if !strings.EqualFold(grant.ResourceType, domain) {
				continue
			}
		}
		if grant.ResourceID != nil {
			resourceID := r.PathValue("id")
			if resourceID == "" || resourceID != *grant.ResourceID {
				continue
			}
		}
		if !grantConditionsMatch(p, r, grant.Conditions) {
			continue
		}
		if grant.ResourceID != nil {
			return true, true
		}
		allowed = true
	}
	return allowed, false
}

func grantConditionsMatch(p Principal, r *http.Request, conditions map[string]any) bool {
	for key, raw := range conditions {
		switch key {
		case "method":
			value, ok := raw.(string)
			if !ok || !strings.EqualFold(value, r.Method) {
				return false
			}
		case "methods":
			values, ok := raw.([]any)
			matched := false
			if !ok {
				return false
			}
			for _, item := range values {
				if value, ok := item.(string); ok && strings.EqualFold(value, r.Method) {
					matched = true
				}
			}
			if !matched {
				return false
			}
		case "pathPrefix":
			value, ok := raw.(string)
			if !ok || !strings.HasPrefix(r.URL.Path, value) {
				return false
			}
		case "userType":
			value, ok := raw.(string)
			if !ok || value != p.UserType {
				return false
			}
		case "dataScope":
			value, ok := raw.(string)
			if !ok || value != p.DataScope {
				return false
			}
		case "organizationId":
			value, ok := raw.(string)
			if !ok || p.OrganizationID == nil || value != *p.OrganizationID {
				return false
			}
		case "supplierId":
			value, ok := raw.(string)
			if !ok || p.SupplierID == nil || value != *p.SupplierID {
				return false
			}
		case "query":
			values, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			for name, expected := range values {
				value, ok := expected.(string)
				if !ok || r.URL.Query().Get(name) != value {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func grantConditionsValid(conditions map[string]any) bool {
	for key, raw := range conditions {
		switch key {
		case "method", "pathPrefix", "userType", "dataScope", "organizationId", "supplierId":
			if _, ok := raw.(string); !ok {
				return false
			}
		case "methods":
			values, ok := raw.([]any)
			if !ok || len(values) == 0 {
				return false
			}
			for _, value := range values {
				if _, ok := value.(string); !ok {
					return false
				}
			}
		case "query":
			values, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			for _, value := range values {
				if _, ok := value.(string); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "error", v, "request_id", requestID(r.Context()))
				writeError(w, http.StatusInternalServerError, "internal_error", "요청을 처리하지 못했습니다")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := randomID()
		if err != nil {
			panic(err)
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'")
		start := time.Now()
		runtimeHTTPMetrics.inFlight.Add(1)
		observer := &responseObserver{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(observer, r.WithContext(ctx))
		runtimeHTTPMetrics.inFlight.Add(-1)
		runtimeHTTPMetrics.requests.Add(1)
		if observer.status >= http.StatusBadRequest {
			runtimeHTTPMetrics.errors.Add(1)
		}
		runtimeHTTPMetrics.durationNanoseconds.Add(uint64(time.Since(start)))
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds(), "request_id", id)
	})
}

func randomID() (string, error) {
	t, err := randomToken(12)
	if err != nil {
		return "", errors.New("random source unavailable")
	}
	return t, nil
}
