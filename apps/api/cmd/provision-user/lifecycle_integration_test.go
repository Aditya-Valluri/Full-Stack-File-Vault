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
	"strings"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/graph"
	"full-stack-file-vault.local/api/internal/server"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
)

func testLifecycle(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, directory)
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	publisher := f.publisher(f.pool, f.local)
	ownerCtx, owner, _, _ := f.user(1000)
	otherCtx, _, otherToken, _ := f.user(1000)
	published, err := publisher.Publish(ownerCtx, []*upload.Staged{f.staged("lifecycle-bytes"), f.staged("lifecycle-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := publisher.Publish(otherCtx, []*upload.Staged{f.staged("lifecycle-bytes")})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("owner stats exclude cross-user savings", func(t *testing.T) {
		stats, err := reader.Statistics(ownerCtx)
		if err != nil || stats.FileCount != 2 || stats.LogicalBytes != 30 || stats.UniqueContentBytes != 15 || stats.SavedBytes != 15 || stats.SavingsPercent != "50.00" {
			t.Fatal(stats, err)
		}
		other, err := reader.Statistics(otherCtx)
		if err != nil || other.FileCount != 1 || other.SavedBytes != 0 {
			t.Fatal("global reuse leaked", other, err)
		}
	})
	t.Run("grants and logical delete authorization", func(t *testing.T) {
		if _, err := reader.CreateAccess(otherCtx, published[0].ID, "DOWNLOAD"); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("foreign grant issued", err)
		}
		if err := reader.Delete(otherCtx, published[0].ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("foreign file deleted", err)
		}
		if _, err := reader.CreateAccess(ownerCtx, published[0].ID, "PREVIEW"); err != nil {
			t.Fatal("plain text preview rejected", err)
		}
		grant, err := reader.CreateAccess(ownerCtx, published[0].ID, "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		opaque := strings.TrimPrefix(grant.URL, "/content/")
		var databaseNow time.Time
		if err = admin.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
			t.Fatal(err)
		}
		if len(opaque) != 43 || !grant.ExpiresAt.After(databaseNow) || grant.ExpiresAt.After(databaseNow.Add(time.Minute)) {
			t.Fatal("grant format/expiry")
		}
		opened, err := reader.OpenAccess(ownerCtx, opaque, f.local)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(opened.Body)
		_ = opened.Body.Close()
		if err != nil || string(data) != "lifecycle-bytes" {
			t.Fatal("wrong content", err)
		}
		if _, err := reader.OpenAccess(otherCtx, opaque, f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("foreign grant redemption", err)
		}
		// Quota corruption fails closed: deletion must roll back without revoking grants.
		if _, err := admin.Exec(ctx, "UPDATE vault.users SET used_bytes=0 WHERE id=$1", owner); err != nil {
			t.Fatal(err)
		}
		if err := reader.Delete(ownerCtx, published[0].ID); !errors.Is(err, files.ErrInvariant) {
			t.Fatal("drift hidden", err)
		}
		if _, err := reader.Get(ownerCtx, published[0].ID); err != nil {
			t.Fatal("failed deletion removed file", err)
		}
		if _, err := admin.Exec(ctx, "UPDATE vault.users SET used_bytes=30 WHERE id=$1", owner); err != nil {
			t.Fatal(err)
		}
		if err := reader.Delete(ownerCtx, published[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.OpenAccess(ownerCtx, opaque, f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("deleted grant usable", err)
		}
		if err := reader.Delete(ownerCtx, published[0].ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("repeat deletion charged quota", err)
		}
		f.usage(publisher, ownerCtx, 15)
		if err := reader.Delete(ownerCtx, published[1].ID); err != nil {
			t.Fatal(err)
		}
		f.usage(publisher, ownerCtx, 0)
		var blobState string
		if err := admin.QueryRow(ctx, "SELECT b.state FROM vault.blobs b JOIN vault.files f ON f.blob_id=b.id WHERE f.id=$1", shared[0].ID).Scan(&blobState); err != nil || blobState != "ACTIVE" {
			t.Fatal("shared content scheduled prematurely")
		}
		if err := reader.Delete(otherCtx, shared[0].ID); err != nil {
			t.Fatal(err)
		}
		f.usage(publisher, otherCtx, 0)
	})
	t.Run("grant expiry and per-user bound", func(t *testing.T) {
		request, id, _, _ := f.user(100)
		items, err := publisher.Publish(request, []*upload.Staged{f.staged("grant-cap")})
		if err != nil {
			t.Fatal(err)
		}
		var first string
		for i := 0; i < 64; i++ {
			grant, err := reader.CreateAccess(request, items[0].ID, "DOWNLOAD")
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first = strings.TrimPrefix(grant.URL, "/content/")
			}
		}
		if _, err := reader.CreateAccess(request, items[0].ID, "DOWNLOAD"); !errors.Is(err, files.ErrAccessLimit) {
			t.Fatal("grant cap bypass", err)
		}
		if _, err := admin.Exec(ctx, "UPDATE vault.file_access SET created_at=now()-interval '2 minutes',expires_at=now()-interval '90 seconds' WHERE owner_id=$1", id); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.OpenAccess(request, first, f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("expired grant accepted", err)
		}
		if _, err := reader.CreateAccess(request, items[0].ID, "DOWNLOAD"); err != nil {
			t.Fatal("expired grants not reclaimed", err)
		}
	})
	t.Run("GraphQL to authenticated HTTP content", func(t *testing.T) {
		// Independent fresh files ensure statistics/deletion tests cannot affect transport.
		request, id, cookie, session := f.user(1000)
		data := "<script>never execute this</script>"
		input, err := publisher.Publish(request, []*upload.Staged{f.staged(data)})
		if err != nil {
			t.Fatal(err)
		}
		png := string([]byte{137, 80, 78, 71, 13, 10, 26, 10}) + "preview-signature"
		images, err := publisher.Publish(request, []*upload.Staged{f.staged(png)})
		if err != nil {
			t.Fatal(err)
		}
		limiter, err := auth.NewUserLimiter(f.pool, 2)
		if err != nil {
			t.Fatal(err)
		}
		app, err := graph.NewApplicationHandler(f.logger, f.browser, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
			return f.sessions.BeginSession(ctx, token, 60)
		}, f.sessions.LoginBrowser, f.sessions.Revoke, publisher, reader, limiter, graph.MultipartConfig{Directory: t.TempDir(), MaxFileBytes: 100, MaxRequestBytes: 4096, MaxFiles: 3, MaxConcurrentRequests: 2, Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		content := f.browser.WrapContent(limiter.Wrap(files.NewContentHandler(reader, f.local, f.logger)))
		router := server.New("", func(context.Context) error { return nil }, f.logger, app, content)
		srv := httptest.NewTLSServer(router.Handler)
		defer srv.Close()
		reset := func() {
			t.Helper()
			if _, err := admin.Exec(ctx, "UPDATE vault.user_request_windows SET accepted_at='{}' WHERE user_id IN ($1,$2)", id, owner); err != nil {
				t.Fatal(err)
			}
		}
		gql := func(query string, variables map[string]any) string {
			t.Helper()
			reset()
			payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
			req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/graphql", bytes.NewReader(payload))
			req.Header.Set("Origin", "https://vault.example.com")
			req.Header.Set("X-CSRF-Token", session.CSRFToken)
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
			response, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return string(body)
		}
		issue := func(fileID, mode string) string {
			t.Helper()
			body := gql("mutation($id:ID!,$mode:FileAccessMode!){createFileAccess(fileId:$id,mode:$mode){url expiresAt}}", map[string]any{"id": fileID, "mode": mode})
			var response struct {
				Data struct{ CreateFileAccess struct{ URL string } }
			}
			if err := json.Unmarshal([]byte(body), &response); err != nil || response.Data.CreateFileAccess.URL == "" {
				t.Fatal("grant mutation failed", body)
			}
			return response.Data.CreateFileAccess.URL
		}
		fetch := func(path, method, cookie, byteRange, site string) (int, string, http.Header) {
			t.Helper()
			reset()
			req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, nil)
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
			}
			if byteRange != "" {
				req.Header.Set("Range", byteRange)
			}
			if site != "" {
				req.Header.Set("Sec-Fetch-Site", site)
			}
			response, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return response.StatusCode, string(body), response.Header
		}
		path := issue(input[0].ID, "DOWNLOAD")
		status, body, headers := fetch(path, "GET", cookie, "", "same-origin")
		if status != 200 || body != data || headers.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") || headers.Get("Referrer-Policy") != "no-referrer" || headers.Get("ETag") != "" {
			t.Fatal("unsafe download", status, body, headers)
		}
		status, body, headers = fetch(path, "GET", cookie, "bytes=0-6", "")
		if status != 206 || body != data[:7] || headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal("range transfer failed", status, body)
		}
		status, body, _ = fetch(path, "HEAD", cookie, "", "")
		if status != 200 || body != "" {
			t.Fatal("HEAD failed")
		}
		status, _, _ = fetch(path, "GET", cookie, "bytes=0-1,3-4", "")
		if status != 416 {
			t.Fatal("multipart ranges accepted")
		}
		status, _, _ = fetch(path, "GET", cookie, "", "cross-site")
		if status != 403 {
			t.Fatal("cross-site transport accepted")
		}
		status, _, _ = fetch(path, "GET", "", "", "")
		if status != 401 {
			t.Fatal("cookie-less transport accepted")
		}
		status, _, _ = fetch(path, "GET", otherToken, "", "")
		if status != 404 {
			t.Fatal("other owner redeemed URL", status)
		}
		sameUserToken, _, err := f.sessions.Create(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		status, _, _ = fetch(path, "GET", sameUserToken, "", "")
		if status != 404 {
			t.Fatal("another session redeemed URL", status)
		}
		rejected := gql("mutation($id:ID!){createFileAccess(fileId:$id,mode:PREVIEW){url}}", map[string]any{"id": input[0].ID})
		if !strings.Contains(rejected, "UNSUPPORTED_MEDIA_TYPE") {
			t.Fatal("active content preview allowed", rejected)
		}
		imagePath := issue(images[0].ID, "PREVIEW")
		status, body, headers = fetch(imagePath, "GET", cookie, "", "")
		if status != 200 || body != png || headers.Get("Content-Type") != "image/png" || !strings.HasPrefix(headers.Get("Content-Disposition"), "inline;") || !strings.Contains(headers.Get("Content-Security-Policy"), "sandbox") {
			t.Fatal("preview policy failed", status, headers)
		}
		stats := gql("{storageStats{fileCount logicalBytes uniqueContentBytes savedBytes savingsPercent}}", nil)
		if !strings.Contains(stats, "\"fileCount\":\"2\"") || strings.Contains(stats, "\"errors\"") {
			t.Fatal(stats)
		}
		deleted := gql("mutation($id:ID!){deleteFile(id:$id)}", map[string]any{"id": input[0].ID})
		if !strings.Contains(deleted, "\"deleteFile\":true") {
			t.Fatal("delete mutation failed", deleted)
		}
		status, _, _ = fetch(path, "GET", cookie, "", "")
		if status != 404 {
			t.Fatal("deleted grant survived", status)
		}
		if err := f.sessions.Revoke(ctx, cookie); err != nil {
			t.Fatal(err)
		}
		status, _, _ = fetch(imagePath, "GET", cookie, "", "")
		if status != 401 {
			t.Fatal("revoked session opened bytes", status)
		}
	})

}
