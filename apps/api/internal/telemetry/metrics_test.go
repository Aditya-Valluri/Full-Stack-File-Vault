package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsDoNotExposeRequestSecrets(t *testing.T) {
	registry := New()
	handler := registry.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	request := httptest.NewRequest("GET", "/content/private-token?user=private-user", nil)
	request.Header.Set("Authorization", "private-credential")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	registry.GraphQLError("unbounded-private-code")
	registry.Cleanup(1, 2, false)
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, secret := range []string{"private-token", "private-user", "private-credential", "unbounded-private-code"} {
		if strings.Contains(body, secret) {
			t.Fatal("sensitive metric label")
		}
	}
	for _, metric := range []string{`vault_http_requests_total{route="content",status_class="4xx"} 1`, `vault_graphql_errors_total{code="OTHER"} 1`, "vault_cleanup_temporary_removed_total 2"} {
		if !strings.Contains(body, metric) {
			t.Fatal("missing metric", metric)
		}
	}
}
