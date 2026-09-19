package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"file-vault.local/api/internal/graph/model"
	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type failingResolver struct {
	err        error
	panicValue any
}

func (r *failingResolver) Query() QueryResolver       { return r }
func (r *failingResolver) Mutation() MutationResolver { return (&Resolver{}).Mutation() }
func (r *failingResolver) ServiceInfo(context.Context) (*model.ServiceInfo, error) {
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return nil, r.err
}

func TestResolverErrorsArePrivate(t *testing.T) {
	const secret = "password=private-value /private/storage SQL failure"
	for _, tc := range []struct {
		name     string
		resolver *failingResolver
	}{
		{"ordinary", &failingResolver{err: errors.New(secret)}},
		{"wrapped", &failingResolver{err: fmt.Errorf("database: %w", errors.New(secret))}},
		{"forged protocol code", &failingResolver{err: &gqlerror.Error{Message: secret, Extensions: map[string]any{"code": "GRAPHQL_VALIDATION_FAILED", "debug": secret}}}},
		{"panic", &failingResolver{panicValue: secret}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := newHandler(slog.New(slog.NewJSONHandler(&logs, nil)), tc.resolver)
			req := httptest.NewRequest("POST", "/graphql", strings.NewReader(`{"query":"{serviceInfo{name}}"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			var result graphql.Response
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Errors) != 1 {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
			e := result.Errors[0]
			if e.Message != "internal server error" || e.Extensions["code"] != codeInternal {
				t.Fatalf("unsafe error: %s", w.Body.String())
			}
			if len(e.Path) != 1 || fmt.Sprint(e.Path[0]) != "serviceInfo" {
				t.Fatalf("missing resolver path: %v", e.Path)
			}
			if strings.Contains(w.Body.String(), secret) || strings.Contains(logs.String(), secret) {
				t.Fatal("secret exposed")
			}
		})
	}
}

func TestProtocolErrorsHaveSafeCodes(t *testing.T) {
	cases := []struct {
		name, method, contentType, body, code string
		status                                int
	}{
		{"method", "GET", "", "", codeMethodNotAllowed, 405},
		{"media", "POST", "text/plain", "", codeUnsupportedMedia, 415},
		{"size", "POST", "application/json", strings.Repeat(" ", int(MaxRequestBytes)+1), codeRequestTooLarge, 413},
		{"malformed body", "POST", "application/json", `{"password":"private-value",`, codeInvalidInput, 400},
		{"invalid field", "POST", "application/json", `{"query":"{ unknownField }"}`, "GRAPHQL_VALIDATION_FAILED", 422},
		{"syntax", "POST", "application/json", `{"query":"{"}`, "GRAPHQL_PARSE_FAILED", 422},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := NewHandler(slog.New(slog.NewJSONHandler(&logs, nil)))
			req := httptest.NewRequest(tc.method, "/graphql", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			var result graphql.Response
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || len(result.Errors) == 0 || result.Errors[0].Extensions["code"] != tc.code {
				t.Fatalf("status %d response %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "private-value") || strings.Contains(logs.String(), "private-value") {
				t.Fatal("request body leaked")
			}
			if tc.name == "invalid field" && !strings.Contains(result.Errors[0].Message, "unknownField") {
				t.Fatal("useful validation detail lost")
			}
		})
	}
}
