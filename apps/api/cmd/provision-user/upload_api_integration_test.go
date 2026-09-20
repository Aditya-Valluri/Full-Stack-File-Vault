//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"file-vault.local/api/internal/auth"
	"file-vault.local/api/internal/files"
	"file-vault.local/api/internal/graph"
	"github.com/jackc/pgx/v5"
)

type countBody struct{ reads int }

func (b *countBody) Read(_ []byte) (int, error) { b.reads++; return 0, io.EOF }
func (b *countBody) Close() error               { return nil }

func testUploadAPI(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, storageDirectory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, storageDirectory)
	_, user, token, state := f.user(8)
	publisher := f.publisher(f.pool, f.local)
	limiter, err := auth.NewUserLimiter(f.pool, 2)
	if err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := graph.NewApplicationHandler(f.logger, f.browser, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
		return f.sessions.BeginSession(ctx, token, 60)
	}, f.sessions.LoginBrowser, f.sessions.Revoke, publisher, reader, limiter, graph.MultipartConfig{
		Directory: staging, MaxFileBytes: 100, MaxRequestBytes: 4096, MaxFiles: 3, MaxConcurrentRequests: 2, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	reset := func() {
		t.Helper()
		if _, err := admin.Exec(ctx, "UPDATE vault.user_request_windows SET accepted_at='{}' WHERE user_id=$1", user); err != nil {
			t.Fatal(err)
		}
	}
	request := func(body []byte, contentType, cookie, csrf string) (int, string, http.Header) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "POST", server.URL, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", contentType)
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
		output, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(output), response.Header
	}
	multipartBody := func(query string, values []string) ([]byte, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		vars := map[string]any{"files": make([]any, len(values))}
		mapping := map[string][]string{}
		for i := range values {
			mapping[fmt.Sprint(i)] = []string{fmt.Sprintf("variables.files.%d", i)}
		}
		operations, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
		encodedMap, _ := json.Marshal(mapping)
		if err := writer.WriteField("operations", string(operations)); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("map", string(encodedMap)); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			part, err := writer.CreateFormFile(fmt.Sprint(i), fmt.Sprintf("file-%d.txt", i))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(part, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return body.Bytes(), writer.FormDataContentType()
	}
	query := "mutation($files:[Upload!]!){uploadFiles(files:$files){id name sizeBytes detectedMIME createdAt}}"
	body, media := multipartBody(query, []string{"same", "same"})
	for _, credentials := range [][2]string{{"", ""}, {token, ""}, {token, strings.Repeat("A", 43)}} {
		status, output, _ := request(body, media, credentials[0], credentials[1])
		if status != 401 && status != 403 {
			t.Fatalf("upload accepted without auth/CSRF: %d %s", status, output)
		}
	}
	status, output, _ := request(body, media, token, state.CSRFToken)
	if status != 200 || strings.Contains(output, "\"errors\"") || strings.Count(output, "\"sizeBytes\":\"4\"") != 2 {
		t.Fatalf("batch upload failed: %d %s", status, output)
	}
	for _, forbidden := range []string{"sha256", "storage_key", "blob-", "reused", f.dir} {
		if strings.Contains(output, forbidden) {
			t.Fatal("upload leaked internal content identity")
		}
	}
	quota := []byte("{\"query\":\"{quota{usedBytes quotaBytes remainingBytes}}\"}")
	status, output, _ = request(quota, "application/json", token, state.CSRFToken)
	if status != 200 || !strings.Contains(output, "\"usedBytes\":\"8\"") || !strings.Contains(output, "\"remainingBytes\":\"0\"") {
		t.Fatal("quota response incorrect", output)
	}
	// Two admissions already used: rejection must occur before reading any file body.
	unread := &countBody{}
	req := httptest.NewRequest("POST", "https://vault.example.com/graphql", unread).WithContext(ctx)
	req.Header.Set("Origin", "https://vault.example.com")
	req.Header.Set("Content-Type", media)
	req.Header.Set("X-CSRF-Token", state.CSRFToken)
	req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != 429 || unread.reads != 0 || rr.Header().Get("Retry-After") == "" {
		t.Fatal("rate guard failed before body parsing", rr.Code, unread.reads)
	}
	reset()
	status, output, _ = request(body, media, token, state.CSRFToken)
	if status != 200 || !strings.Contains(output, "QUOTA_EXCEEDED") {
		t.Fatal("quota failure not exposed safely", output)
	}
	reset()
	mixed, mixedMedia := multipartBody("mutation($files:[Upload!]!){uploadFiles(files:$files){id} logout}", []string{"x"})
	status, output, _ = request(mixed, mixedMedia, token, state.CSRFToken)
	if status != 400 {
		t.Fatal("mixed mutation accepted", status, output)
	}
	reset()
	tooLarge, largeMedia := multipartBody(query, []string{strings.Repeat("x", 101)})
	status, _, _ = request(tooLarge, largeMedia, token, state.CSRFToken)
	if status != 413 {
		t.Fatal("file byte limit not applied", status)
	}
	// Single-file mutation follows the same publication path, including zero-byte files.
	reset()
	var single bytes.Buffer
	writer := multipart.NewWriter(&single)
	_ = writer.WriteField("operations", `{"query":"mutation($file:Upload!){uploadFile(file:$file){id sizeBytes}}","variables":{"file":null}}`)
	_ = writer.WriteField("map", `{"0":["variables.file"]}`)
	part, err := writer.CreateFormFile("0", "empty.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(nil)
	_ = writer.Close()
	status, output, _ = request(single.Bytes(), writer.FormDataContentType(), token, state.CSRFToken)
	if status != 200 || !strings.Contains(output, "\"sizeBytes\":\"0\"") {
		t.Fatal("single upload failed", output)
	}
	entries, err := os.ReadDir(staging)
	if err != nil || len(entries) != 0 {
		t.Fatal("multipart staging leaked")
	}
	var count int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM vault.files WHERE owner_id=$1", user).Scan(&count); err != nil || count != 3 {
		t.Fatal("rejected operations changed logical files")
	}
	// A second identity cannot read the first user's quota.
	_, _, otherToken, otherState := f.user(20000000)
	status, output, _ = request(quota, "application/json", otherToken, otherState.CSRFToken)
	if status != 200 || !strings.Contains(output, "\"usedBytes\":\"0\"") || !strings.Contains(output, "\"quotaBytes\":\"20000000\"") {
		t.Fatal("quota identity isolation failed", output)
	}
}
