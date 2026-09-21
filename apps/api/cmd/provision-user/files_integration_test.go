//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/graph"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
)

func testFiles(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, directory)
	ownerCtx, owner, token, state := f.user(1000)
	otherCtx, other, _, _ := f.user(1000)
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	publisher := f.publisher(f.pool, f.local)
	published, err := publisher.Publish(ownerCtx, []*upload.Staged{f.staged("alpha"), f.staged("beta12"), f.staged("note"), f.staged("")})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := publisher.Publish(otherCtx, []*upload.Staged{f.staged("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"Alpha%_!.TXT", "BETA.txt", "alpha-note.txt", "empty.txt"}
	instant := time.Date(2026, 1, 1, 12, 0, 0, 123456000, time.UTC)
	for i, file := range published {
		at := instant
		if i == 3 {
			at = instant.Add(-time.Hour)
		}
		if _, err := admin.Exec(ctx, "UPDATE vault.files SET original_name=$2,created_at=$3 WHERE id=$1", file.ID, names[i], at); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("stable ties and new insertion between pages", func(t *testing.T) {
		expected := []string{published[0].ID, published[1].ID, published[2].ID}
		sort.Sort(sort.Reverse(sort.StringSlice(expected)))
		page, err := reader.List(ownerCtx, files.ListOptions{First: 2})
		if err != nil || len(page.Nodes) != 2 || !page.HasNextPage || page.EndCursor == nil || page.Nodes[0].ID != expected[0] || page.Nodes[1].ID != expected[1] {
			t.Fatalf("first page: %+v %v", page, err)
		}
		if _, err := publisher.Publish(ownerCtx, []*upload.Staged{f.staged("newer-query-file")}); err != nil {
			t.Fatal(err)
		}
		next, err := reader.List(ownerCtx, files.ListOptions{First: 2, After: page.EndCursor})
		if err != nil || len(next.Nodes) != 2 || next.HasNextPage || next.Nodes[0].ID != expected[2] || next.Nodes[1].ID != published[3].ID {
			t.Fatalf("next page: %+v %v", next, err)
		}
		empty, err := reader.List(ownerCtx, files.ListOptions{First: 2, After: next.EndCursor})
		if err != nil || len(empty.Nodes) != 0 || empty.EndCursor != nil || empty.HasNextPage {
			t.Fatal("terminal page incorrect", err)
		}
		if _, err := reader.List(otherCtx, files.ListOptions{First: 2, After: page.EndCursor}); !errors.Is(err, files.ErrInvalidInput) {
			t.Fatal("foreign cursor accepted", err)
		}
	})
	pointer := func(value string) *string { return &value }
	t.Run("combined literal MIME size and date filters", func(t *testing.T) {
		before := instant.Add(time.Second)
		page, err := reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{NameContains: pointer("ALPHA"), MIMEType: pointer("text/plain"), MinSizeBytes: pointer("4"), MaxSizeBytes: pointer("5"), CreatedFrom: &instant, CreatedBefore: &before}})
		if err != nil || len(page.Nodes) != 2 {
			t.Fatalf("combined filters: %+v %v", page, err)
		}
		literal, err := reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{NameContains: pointer("%_!")}})
		if err != nil || len(literal.Nodes) != 1 || literal.Nodes[0].ID != published[0].ID {
			t.Fatal("LIKE wildcard expansion", err)
		}
		for _, filter := range []files.Filter{{MIMEType: pointer("image/png")}, {NameContains: pointer("' OR 1=1 --")}, {CreatedBefore: &instant, MinSizeBytes: pointer("1")}} {
			page, err := reader.List(ownerCtx, files.ListOptions{First: 20, Filter: filter})
			if err != nil || len(page.Nodes) != 0 {
				t.Fatal("unexpected filter matches", err)
			}
		}
		empty, err := reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{MinSizeBytes: pointer("0"), MaxSizeBytes: pointer("0")}})
		if err != nil || len(empty.Nodes) != 1 || empty.Nodes[0].ID != published[3].ID {
			t.Fatal("zero size filter failed", err)
		}
	})
	t.Run("ownership and fresh authorization", func(t *testing.T) {
		if _, err := reader.Get(ownerCtx, published[0].ID); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{foreign[0].ID, "00000000-0000-4000-8000-000000000000", "invalid"} {
			if _, err := reader.Get(ownerCtx, id); !errors.Is(err, files.ErrNotFound) {
				t.Fatal("foreign/missing ID exposed", err)
			}
		}
		// Admin role does not turn this endpoint into a global file browser.
		if _, err := admin.Exec(ctx, "UPDATE vault.users SET role='ADMIN' WHERE id=$1", other); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Get(otherCtx, published[0].ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("admin bypassed owned metadata API", err)
		}
		if _, err := reader.List(ctx, files.ListOptions{First: 20}); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("anonymous query accepted", err)
		}
		expired, _, staleToken, _ := f.user(100)
		if err := f.sessions.Revoke(ctx, staleToken); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.List(expired, files.ListOptions{First: 20}); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatal("stale context accepted", err)
		}
	})
	t.Run("TLS GraphQL contract and bounds", func(t *testing.T) {
		limiter, err := auth.NewUserLimiter(f.pool, 2)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := graph.NewApplicationHandler(f.logger, f.browser, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
			return f.sessions.BeginSession(ctx, token, 60)
		}, f.sessions.LoginBrowser, f.sessions.Revoke, publisher, reader, limiter, graph.MultipartConfig{Directory: t.TempDir(), MaxFileBytes: 100, MaxRequestBytes: 4096, MaxFiles: 3, MaxConcurrentRequests: 2, Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewTLSServer(handler)
		defer server.Close()
		request := func(query string, variables map[string]any, cookie, csrf string) (int, string) {
			t.Helper()
			// Reset only this test user's admission window between independent assertions.
			if _, err := admin.Exec(ctx, "UPDATE vault.user_request_windows SET accepted_at='{}' WHERE user_id=$1", owner); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
			req, err := http.NewRequestWithContext(ctx, "POST", server.URL, bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "https://vault.example.com")
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
			}
			if csrf != "" {
				req.Header.Set("X-CSRF-Token", csrf)
			}
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return response.StatusCode, string(body)
		}
		tagMutation := "mutation($id:ID!,$tags:[String!]!){setFileTags(fileId:$id,tags:$tags)}"
		_, tagBody := request(tagMutation, map[string]any{"id": published[0].ID, "tags": []string{"Private"}}, token, state.CSRFToken)
		if strings.Contains(tagBody, "\"errors\"") || !strings.Contains(tagBody, "private") {
			t.Fatal("tag mutation failed", tagBody)
		}
		_, tagBody = request(tagMutation, map[string]any{"id": foreign[0].ID, "tags": []string{"stolen"}}, token, state.CSRFToken)
		if !strings.Contains(tagBody, "NOT_FOUND") {
			t.Fatal("foreign tag edit allowed", tagBody)
		}
		tagStatus, _ := request(tagMutation, map[string]any{"id": published[0].ID, "tags": []string{}}, token, "")
		if tagStatus != 403 {
			t.Fatal("tag CSRF bypass", tagStatus)
		}
		_, tagBody = request(tagMutation, map[string]any{"id": published[0].ID, "tags": []string{}}, "", "")
		if !strings.Contains(tagBody, "UNAUTHENTICATED") {
			t.Fatal("anonymous tag mutation allowed", tagBody)
		}
		query := "query($first:Int!,$filter:FileFilter){files(first:$first,filter:$filter){nodes{id name sizeBytes detectedMIME createdAt} pageInfo{endCursor hasNextPage}}}"
		status, body := request(query, map[string]any{"first": 50, "filter": map[string]any{"nameContains": "%_!"}}, token, state.CSRFToken)
		if status != 200 || strings.Contains(body, "\"errors\"") || !strings.Contains(body, published[0].ID) {
			t.Fatal("GraphQL list failed", status, body)
		}
		for _, value := range []string{"storage_key", "sha256", "blob-", other, foreign[0].ID} {
			if strings.Contains(body, value) {
				t.Fatal("private metadata leaked")
			}
		}
		for _, count := range []int{0, 51, -1} {
			_, body = request(query, map[string]any{"first": count}, token, state.CSRFToken)
			if !strings.Contains(body, "INVALID_INPUT") {
				t.Fatal("unbounded page accepted", body)
			}
		}
		for _, filter := range []map[string]any{{"minSizeBytes": "-1"}, {"minSizeBytes": "8", "maxSizeBytes": "1"}, {"mimeType": "image/*"}, {"createdFrom": "not-a-date"}} {
			_, body = request(query, map[string]any{"first": 20, "filter": filter}, token, state.CSRFToken)
			if !strings.Contains(body, "\"errors\"") {
				t.Fatal("invalid filter accepted", body)
			}
		}
		lookup := "query($id:ID!){file(id:$id){id name sizeBytes}}"
		_, own := request(lookup, map[string]any{"id": published[0].ID}, token, state.CSRFToken)
		if strings.Contains(own, "\"errors\"") || !strings.Contains(own, published[0].ID) {
			t.Fatal("owned metadata rejected", own)
		}
		_, hidden := request(lookup, map[string]any{"id": foreign[0].ID}, token, state.CSRFToken)
		_, absent := request(lookup, map[string]any{"id": "00000000-0000-4000-8000-000000000000"}, token, state.CSRFToken)
		if hidden != absent || !strings.Contains(hidden, "NOT_FOUND") {
			t.Fatal("IDOR existence oracle", hidden, absent)
		}
		_, body = request("{files{nodes{id}}}", nil, "", "")
		if !strings.Contains(body, "UNAUTHENTICATED") {
			t.Fatal("anonymous files exposed", body)
		}
		status, _ = request("{files{nodes{id}}}", nil, token, "")
		if status != 403 {
			t.Fatal("CSRF bypass", status)
		}
	})
	t.Run("legacy missing MIME has searchable fallback", func(t *testing.T) {
		if _, err := admin.Exec(ctx, "UPDATE vault.blobs SET detected_mime=NULL WHERE id=(SELECT blob_id FROM vault.files WHERE id=$1)", published[1].ID); err != nil {
			t.Fatal(err)
		}
		page, err := reader.List(ownerCtx, files.ListOptions{First: 20, Filter: files.Filter{MIMEType: pointer("application/octet-stream")}})
		if err != nil || len(page.Nodes) != 1 || page.Nodes[0].ID != published[1].ID || page.Nodes[0].DetectedMIME != "application/octet-stream" {
			t.Fatal("fallback MIME cannot be searched", err)
		}
	})
	t.Run("database errors fail closed", func(t *testing.T) {
		f.pool.Close()
		if _, err := reader.List(ownerCtx, files.ListOptions{First: 20}); !errors.Is(err, files.ErrUnavailable) {
			t.Fatal("database failure not closed", err)
		}
	})
}
