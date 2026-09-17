package graph

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapOperationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, query, operation string
		valid                  bool
	}{
		{"simple", `mutation { beginSession { csrfToken } }`, "", true},
		{"alias", `mutation { start: beginSession { csrfToken } }`, "", true},
		{"fragment", `mutation { ...B } fragment B on Mutation { beginSession { csrfToken } }`, "", true},
		{"inline", `mutation { ... on Mutation { beginSession { csrfToken } } }`, "", true},
		{"selected", `query Q { serviceInfo { name } } mutation B { beginSession { csrfToken } }`, "B", true},
		{"wrong selected", `query Q { serviceInfo { name } } mutation B { beginSession { csrfToken } }`, "Q", false},
		{"ambiguous", `query Q { serviceInfo { name } } mutation B { beginSession { csrfToken } }`, "", false},
		{"disguised query", `query beginSession { serviceInfo { name } }`, "beginSession", false},
		{"mixed roots", `mutation { beginSession { csrfToken } __typename }`, "", false},
		{"duplicate roots", `mutation { beginSession { csrfToken } beginSession { csrfToken } }`, "", false},
		{"aliased duplicates", `mutation { a:beginSession { csrfToken } b:beginSession { csrfToken } }`, "", false},
		{"directive", `mutation { beginSession @skip(if:false) { csrfToken } }`, "", false},
		{"cycle", `mutation { ...B } fragment B on Mutation { ...B }`, "", false},
		{"unknown field", `mutation { beginSession { token } }`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"query": tc.query, "operationName": tc.operation})
			req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			if err := validateBootstrapRequest(req); (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
	for _, body := range []string{`[]`, `null`, `{`, strings.Repeat(" ", int(MaxRequestBytes)+1)} {
		req := httptest.NewRequest("POST", "/graphql", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if validateBootstrapRequest(req) == nil {
			t.Fatal("invalid body accepted")
		}
	}
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(`{"query":"mutation {beginSession{csrfToken}}"}`))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if validateBootstrapRequest(req) == nil {
		t.Fatal("multipart bootstrap accepted")
	}
}
