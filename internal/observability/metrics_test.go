package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsUseBoundedRouteAndOutcomeLabels(t *testing.T) {
	metrics := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	request := httptest.NewRequest(http.MethodGet, "/items/secret-id", nil)
	metrics.Middleware(mux).ServeHTTP(httptest.NewRecorder(), request)
	metrics.ObserveJob("RESTORE", "retry", 250*time.Millisecond)

	output := metrics.Render()
	for _, expected := range []string{
		`route="GET /items/{id}"`, `status="202"`, `step="RESTORE",outcome="retry"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics do not contain %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "secret-id") {
		t.Fatal("metrics leaked a high-cardinality path value")
	}
}
