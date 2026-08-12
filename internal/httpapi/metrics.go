package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

type httpMetrics struct {
	requests            atomic.Uint64
	errors              atomic.Uint64
	durationNanoseconds atomic.Uint64
	inFlight            atomic.Int64
}

var runtimeHTTPMetrics httpMetrics

type responseObserver struct {
	http.ResponseWriter
	status int
}

func (w *responseObserver) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseObserver) Write(body []byte) (int, error) {
	return w.ResponseWriter.Write(body)
}

func (w *responseObserver) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (a *App) metrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	requests := runtimeHTTPMetrics.requests.Load()
	duration := float64(runtimeHTTPMetrics.durationNanoseconds.Load()) / 1e9
	_, _ = fmt.Fprintf(w, `# HELP vendra_build_info Vendra build metadata.
# TYPE vendra_build_info gauge
vendra_build_info{version="%s",commit="%s"} 1
# HELP vendra_http_requests_total Total HTTP requests completed.
# TYPE vendra_http_requests_total counter
vendra_http_requests_total %d
# HELP vendra_http_request_errors_total Total HTTP responses with status 400 or greater.
# TYPE vendra_http_request_errors_total counter
vendra_http_request_errors_total %d
# HELP vendra_http_requests_in_flight Current HTTP requests in flight.
# TYPE vendra_http_requests_in_flight gauge
vendra_http_requests_in_flight %d
# HELP vendra_http_request_duration_seconds Total HTTP request duration in seconds.
# TYPE vendra_http_request_duration_seconds summary
vendra_http_request_duration_seconds_sum %.9f
vendra_http_request_duration_seconds_count %d
`, metricLabel(Version), metricLabel(Commit), requests, runtimeHTTPMetrics.errors.Load(), runtimeHTTPMetrics.inFlight.Load(), duration, requests)
	if a.db != nil {
		stats := a.db.Stat()
		_, _ = fmt.Fprintf(w, `# HELP vendra_postgres_connections PostgreSQL pool connections by state.
# TYPE vendra_postgres_connections gauge
vendra_postgres_connections{state="acquired"} %d
vendra_postgres_connections{state="idle"} %d
vendra_postgres_connections{state="total"} %d
`, stats.AcquiredConns(), stats.IdleConns(), stats.TotalConns())
	}
}

func metricLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}
