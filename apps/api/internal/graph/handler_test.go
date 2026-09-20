package graph

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGraphQLBoundary(t *testing.T) {
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)))
	cases := []struct {
		name, method, contentType, body string
		status                          int
		wantError                       bool
	}{
		{"query", "POST", "application/json", `{"query":"{ serviceInfo { name } }"}`, 200, false},
		{"unknown field", "POST", "application/json", `{"query":"{ users { id } }"}`, 422, true},
		{"syntax", "POST", "application/json", `{"query":"{"}`, 422, true},
		{"malformed JSON", "POST", "application/json", `{`, 400, true},
		{"batch", "POST", "application/json", `[{"query":"{serviceInfo{name}}"}]`, 400, true},
		{"GET", "GET", "", "", 405, true},
		{"multipart", "POST", "multipart/form-data; boundary=x", "", 415, true},
		{"oversized", "POST", "application/json", strings.Repeat(" ", int(MaxRequestBytes)+1), 413, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/graphql", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			// Exercise the size guard independently of a Content-Length declaration.
			req.ContentLength = -1
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			var result struct {
				Data   struct{ ServiceInfo struct{ Name string } }
				Errors []json.RawMessage
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if (len(result.Errors) > 0) != tc.wantError {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
			if !tc.wantError && result.Data.ServiceInfo.Name != "File Vault" {
				t.Fatal("unexpected service name")
			}
		})
	}
}

func TestComplexityLimit(t *testing.T) {
	var query strings.Builder
	query.WriteString("{")
	for i := 0; i < 251; i++ {
		query.WriteString("a" + strconv.Itoa(i) + ":serviceInfo{name} ")
	}
	query.WriteString("}")
	body, _ := json.Marshal(map[string]string{"query": query.String()})
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "COMPLEXITY_LIMIT_EXCEEDED") {
		t.Fatalf("expected complexity rejection: %s", w.Body.String())
	}
}

func TestFileListComplexityIncludesPageSize(t *testing.T) {
	query := `{a:files(first:50){nodes{id name sizeBytes detectedMIME createdAt} pageInfo{endCursor hasNextPage}} b:files(first:50){nodes{id name sizeBytes detectedMIME createdAt} pageInfo{endCursor hasNextPage}}}`
	body, _ := json.Marshal(map[string]string{"query": query})
	request := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "COMPLEXITY_LIMIT_EXCEEDED") {
		t.Fatal("aliased lists bypassed complexity budget", response.Body.String())
	}
}
