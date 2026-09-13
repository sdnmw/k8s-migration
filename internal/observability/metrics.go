package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is a dependency-free Prometheus collector shared by the API and
// worker processes. Labels are deliberately bounded to route patterns, status
// codes, migration step types and fixed outcomes.
type Metrics struct {
	mu sync.RWMutex

	httpRequests map[httpKey]uint64
	httpDuration map[httpKey]float64
	jobRuns      map[jobKey]uint64
	jobDuration  map[jobKey]float64
	inFlight     atomic.Int64
}

type httpKey struct {
	Method string
	Route  string
	Status int
}

type jobKey struct {
	Step    string
	Outcome string
}

func New() *Metrics {
	return &Metrics{
		httpRequests: make(map[httpKey]uint64),
		httpDuration: make(map[httpKey]float64),
		jobRuns:      make(map[jobKey]uint64),
		jobDuration:  make(map[jobKey]float64),
	}
}

func (m *Metrics) ObserveHTTP(method, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	key := httpKey{Method: method, Route: route, Status: status}
	m.mu.Lock()
	m.httpRequests[key]++
	m.httpDuration[key] += duration.Seconds()
	m.mu.Unlock()
}

func (m *Metrics) ObserveJob(step, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	key := jobKey{Step: step, Outcome: outcome}
	m.mu.Lock()
	m.jobRuns[key]++
	m.jobDuration[key] += duration.Seconds()
	m.mu.Unlock()
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Add(1)
		defer m.inFlight.Add(-1)
		started := time.Now()
		capture := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(capture, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.ObserveHTTP(r.Method, route, capture.status, time.Since(started))
	})
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(m.Render()))
	})
}

func (m *Metrics) Render() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var output strings.Builder
	output.WriteString("# HELP sks_migration_http_requests_total HTTP requests handled by the API.\n")
	output.WriteString("# TYPE sks_migration_http_requests_total counter\n")
	httpKeys := make([]httpKey, 0, len(m.httpRequests))
	for key := range m.httpRequests {
		httpKeys = append(httpKeys, key)
	}
	sort.Slice(httpKeys, func(i, j int) bool {
		return fmt.Sprint(httpKeys[i]) < fmt.Sprint(httpKeys[j])
	})
	for _, key := range httpKeys {
		labels := `method="` + escape(key.Method) + `",route="` + escape(key.Route) + `",status="` + strconv.Itoa(key.Status) + `"`
		fmt.Fprintf(&output, "sks_migration_http_requests_total{%s} %d\n", labels, m.httpRequests[key])
		fmt.Fprintf(&output, "sks_migration_http_request_duration_seconds_sum{%s} %g\n", labels, m.httpDuration[key])
	}
	output.WriteString("# HELP sks_migration_http_requests_in_flight HTTP requests currently being handled.\n")
	output.WriteString("# TYPE sks_migration_http_requests_in_flight gauge\n")
	fmt.Fprintf(&output, "sks_migration_http_requests_in_flight %d\n", m.inFlight.Load())
	output.WriteString("# HELP sks_migration_worker_jobs_total Migration steps handled by workers.\n")
	output.WriteString("# TYPE sks_migration_worker_jobs_total counter\n")
	jobKeys := make([]jobKey, 0, len(m.jobRuns))
	for key := range m.jobRuns {
		jobKeys = append(jobKeys, key)
	}
	sort.Slice(jobKeys, func(i, j int) bool {
		return fmt.Sprint(jobKeys[i]) < fmt.Sprint(jobKeys[j])
	})
	for _, key := range jobKeys {
		labels := `step="` + escape(key.Step) + `",outcome="` + escape(key.Outcome) + `"`
		fmt.Fprintf(&output, "sks_migration_worker_jobs_total{%s} %d\n", labels, m.jobRuns[key])
		fmt.Fprintf(&output, "sks_migration_worker_job_duration_seconds_sum{%s} %g\n", labels, m.jobDuration[key])
	}
	return output.String()
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Flush() {
	if value, ok := w.ResponseWriter.(http.Flusher); ok {
		value.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func escape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
