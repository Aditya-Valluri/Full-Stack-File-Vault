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
	"sync"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/graph"
	"full-stack-file-vault.local/api/internal/server"
	"full-stack-file-vault.local/api/internal/sharing"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
)

type gatedSharedRead struct {
	base    files.ContentStore
	entered chan struct{}
	release <-chan struct{}
}

func (g gatedSharedRead) Open(ctx context.Context, key string, size int64) (io.ReadSeekCloser, error) {
	close(g.entered)
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.base.Open(ctx, key, size)
}

func testSharing(t *testing.T, ctx context.Context, admin *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, admin, dsn, directory)
	service, err := sharing.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sharing.NewStore(f.other)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	publisher := f.publisher(f.pool, f.local)
	ownerCtx, ownerID, ownerCookie, ownerSession := f.user(1000)
	recipientCtx, recipientID, recipientCookie, recipientSession := f.user(1000)
	strangerCtx, _, _, _ := f.user(1000)
	body := "shared-secret-content"
	published, err := publisher.Publish(ownerCtx, []*upload.Staged{f.staged(body)})
	if err != nil {
		t.Fatal(err)
	}
	fileID := published[0].ID
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	tokenOf := func(sh sharing.Created) string { return strings.TrimPrefix(sh.URL, "/share#") }
	grantToken := func(grant files.AccessGrant) string { return strings.TrimPrefix(grant.URL, "/shared-content/") }
	create := func(recipient *string, permission string) sharing.Created {
		t.Helper()
		sh, err := service.Create(ownerCtx, fileID, permission, 3600, recipient)
		if err != nil {
			t.Fatal(err)
		}
		return sh
	}
	anonymous := func() (context.Context, string, auth.Session) {
		t.Helper()
		cookie, state, err := f.sessions.CreateAnonymous(ctx)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "https://vault.example.com/graphql", nil).WithContext(ctx)
		req.Header.Set("Origin", "https://vault.example.com")
		req.Header.Set("X-CSRF-Token", state.CSRFToken)
		req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
		var request context.Context
		w := httptest.NewRecorder()
		f.browser.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { request = r.Context() })).ServeHTTP(w, req)
		if request == nil {
			t.Fatal("anonymous boundary failed", w.Code)
		}
		return request, cookie, state
	}
	anonCtx, anonCookie, anonSession := anonymous()
	resetAnon := func(request context.Context) {
		t.Helper()
		_, binding, err := auth.BrowserBinding(request)
		if err != nil {
			t.Fatal(err)
		}
		exec("UPDATE vault.shared_request_windows SET accepted_at='{}' WHERE session_hash=$1", binding)
	}

	t.Run("bounded private activity", func(t *testing.T) {
		ownCtx, _, _, _ := f.user(1000)
		items, e := publisher.Publish(ownCtx, []*upload.Staged{f.staged("activity-data")})
		if e != nil {
			t.Fatal(e)
		}
		id := items[0].ID
		public, e := service.Create(ownCtx, id, "DOWNLOAD", 3600, nil)
		if e != nil {
			t.Fatal(e)
		}
		restricted, e := service.Create(ownCtx, id, "DOWNLOAD", 3600, &recipientID)
		if e != nil {
			t.Fatal(e)
		}
		if public.URL == restricted.URL {
			t.Fatal("share tokens reused")
		}
		resetAnon(anonCtx)
		if _, e = service.Inspect(anonCtx, tokenOf(public)); e != nil {
			t.Fatal(e)
		}
		if _, e = service.Inspect(recipientCtx, tokenOf(restricted)); e != nil {
			t.Fatal(e)
		}
		overview, e := service.List(ownCtx, id)
		if e != nil || len(overview.Activity) != 2 {
			t.Fatal("missing activity")
		}
		if overview.Activity[0].RecipientID == nil || *overview.Activity[0].RecipientID != recipientID || overview.Activity[1].RecipientID != nil {
			t.Fatal("incorrect attribution")
		}
		if _, e = service.List(strangerCtx, id); !errors.Is(e, files.ErrNotFound) {
			t.Fatal("private activity leaked")
		}
		grant, e := service.CreateAccess(recipientCtx, tokenOf(restricted), "DOWNLOAD")
		if e != nil {
			t.Fatal(e)
		}
		// A real download GET is counted once even if its transport grant is reused.
		transport := files.NewContentTransport("/shared-content/", service.OpenAccess, f.local, f.logger)
		for i := 0; i < 2; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", grant.URL, nil).WithContext(recipientCtx)
			transport.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal("download rejected")
			}
		}
		overview, e = service.List(ownCtx, id)
		if e != nil || len(overview.Activity) != 3 || overview.Activity[0].Kind != "DOWNLOAD_STARTED" {
			t.Fatal("download event missing or duplicated")
		}
		if e = service.Revoke(ownCtx, restricted.Share.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = service.Inspect(recipientCtx, tokenOf(restricted)); !errors.Is(e, files.ErrNotFound) {
			t.Fatal("revoked share admitted")
		}
		overview, e = service.List(ownCtx, id)
		if e != nil || len(overview.Activity) != 3 || overview.Activity[0].Status != "REVOKED" {
			t.Fatal("revoked activity lost or changed")
		}
		for i := 0; i < 105; i++ {
			if _, e = service.Inspect(recipientCtx, tokenOf(public)); e != nil {
				t.Fatal(e)
			}
		}
		overview, e = service.List(ownCtx, id)
		if e != nil || len(overview.Activity) != 100 {
			t.Fatal("unbounded history")
		}
		for _, event := range overview.Activity {
			if event.RecipientID != nil {
				t.Fatal("public visitor identity exposed")
			}
		}
		exec("UPDATE vault.file_shares SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", public.Share.ID)
		if _, e = service.Inspect(recipientCtx, tokenOf(public)); !errors.Is(e, files.ErrNotFound) {
			t.Fatal("expired share admitted")
		}
	})
	t.Run("owner control and recipient permissions", func(t *testing.T) {
		if _, err := service.Create(strangerCtx, fileID, "DOWNLOAD", 3600, nil); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("foreign share creation", err)
		}
		if _, err := service.List(strangerCtx, fileID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("foreign share listing", err)
		}
		sh := create(&recipientID, "DOWNLOAD")
		if len(tokenOf(sh)) != 43 {
			t.Fatal("invalid share token")
		}
		if _, err := service.Inspect(recipientCtx, tokenOf(sh)); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Inspect(strangerCtx, tokenOf(sh)); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("recipient bypass", err)
		}
		resetAnon(anonCtx)
		if _, err := service.Inspect(anonCtx, tokenOf(sh)); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("anonymous recipient bypass", err)
		}
		if _, err := service.CreateAccess(recipientCtx, tokenOf(sh), "PREVIEW"); !errors.Is(err, files.ErrPreviewUnsupported) {
			t.Fatal("permission bypass", err)
		}
		grant, err := service.CreateAccess(recipientCtx, tokenOf(sh), "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.OpenAccess(strangerCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("session binding bypass", err)
		}
		opened, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(opened.Body)
		_ = opened.Body.Close()
		if string(data) != body {
			t.Fatal("wrong shared bytes")
		}
		// Sharing never grants ownership, even to an allowed recipient.
		if err := reader.Delete(recipientCtx, fileID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("recipient deleted source", err)
		}
		if err := service.Revoke(recipientCtx, sh.Share.ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("recipient revoked owner's share", err)
		}
		f.usage(publisher, ownerCtx, int64(len(body)))
		f.usage(publisher, recipientCtx, 0)
		if err := service.Revoke(ownerCtx, sh.Share.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("revoked byte grant survived", err)
		}
	})
	t.Run("expiration owner status and deleted source", func(t *testing.T) {
		sh := create(nil, "DOWNLOAD")
		grant, err := service.CreateAccess(recipientCtx, tokenOf(sh), "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		exec("UPDATE vault.users SET disabled_at=clock_timestamp() WHERE id=$1", ownerID)
		if _, err := service.Inspect(recipientCtx, tokenOf(sh)); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("disabled owner metadata", err)
		}
		if _, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("disabled owner bytes", err)
		}
		exec("UPDATE vault.users SET disabled_at=NULL WHERE id=$1", ownerID)
		exec("UPDATE vault.shared_access SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '90 seconds' WHERE share_id=$1", sh.Share.ID)
		if _, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("expired byte grant", err)
		}
		grant, err = service.CreateAccess(recipientCtx, tokenOf(sh), "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		exec("UPDATE vault.file_shares SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1", sh.Share.ID)
		if _, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("share expiry did not invalidate grant", err)
		}
		other, err := publisher.Publish(ownerCtx, []*upload.Staged{f.staged("shared-deleted-source")})
		if err != nil {
			t.Fatal(err)
		}
		gone, err := service.Create(ownerCtx, other[0].ID, "DOWNLOAD", 3600, nil)
		if err != nil {
			t.Fatal(err)
		}
		grant, err = service.CreateAccess(recipientCtx, tokenOf(gone), "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Delete(ownerCtx, other[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("deleted source accessible", err)
		}
	})
	t.Run("anonymous admission persists failures across pools", func(t *testing.T) {
		resetAnon(anonCtx)
		for _, store := range []*sharing.Store{service, second} {
			if _, err := store.Inspect(anonCtx, strings.Repeat("A", 43)); !errors.Is(err, files.ErrNotFound) {
				t.Fatal("unexpected invalid-link response", err)
			}
		}
		var limited *auth.RateLimitError
		if _, err := second.Inspect(anonCtx, strings.Repeat("A", 43)); !errors.As(err, &limited) {
			t.Fatal("anonymous budget bypass", err)
		}
		resetAnon(anonCtx)
	})
	t.Run("share capacity under concurrent creation", func(t *testing.T) {
		request, _, _, _ := f.user(100)
		input, err := publisher.Publish(request, []*upload.Staged{f.staged("sharing-capacity")})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 19; i++ {
			if _, err := service.Create(request, input[0].ID, "DOWNLOAD", 3600, nil); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error, 2)
		for _, store := range []*sharing.Store{service, second} {
			go func(store *sharing.Store) {
				_, err := store.Create(request, input[0].ID, "DOWNLOAD", 3600, nil)
				done <- err
			}(store)
		}
		successes, limited := 0, 0
		for i := 0; i < 2; i++ {
			err := <-done
			if err == nil {
				successes++
			} else if errors.Is(err, sharing.ErrLimit) {
				limited++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || limited != 1 {
			t.Fatal("share cap raced", successes, limited)
		}
	})
	t.Run("TLS GraphQL and shared byte transport", func(t *testing.T) {
		limiter, err := auth.NewUserLimiter(f.pool, 2)
		if err != nil {
			t.Fatal(err)
		}
		app, err := graph.NewApplicationHandler(f.logger, f.browser, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
			return f.sessions.BeginSession(ctx, token, 60)
		}, f.sessions.LoginBrowser, f.sessions.Revoke, publisher, reader, limiter, graph.MultipartConfig{Directory: t.TempDir(), MaxFileBytes: 100, MaxRequestBytes: 4096, MaxFiles: 3, MaxConcurrentRequests: 2, Timeout: 5 * time.Second}, graph.Services{Sharing: service})
		if err != nil {
			t.Fatal(err)
		}
		content := f.browser.WrapContent(limiter.Wrap(files.NewContentHandler(reader, f.local, f.logger)))
		shared := f.browser.WrapSharedContent(limiter.Wrap(files.NewContentTransport("/shared-content/", service.OpenAccess, f.local, f.logger)))
		router := server.New("", func(context.Context) error { return nil }, f.logger, app, content, shared)
		srv := httptest.NewTLSServer(router.Handler)
		defer srv.Close()
		reset := func() {
			t.Helper()
			resetAnon(anonCtx)
			exec("UPDATE vault.user_request_windows SET accepted_at='{}' WHERE user_id IN ($1,$2)", ownerID, recipientID)
		}
		gql := func(query string, variables map[string]any, cookie, csrf string) (int, string) {
			t.Helper()
			reset()
			payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
			req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/graphql", bytes.NewReader(payload))
			req.Header.Set("Origin", "https://vault.example.com")
			req.Header.Set("Content-Type", "application/json")
			if csrf != "" {
				req.Header.Set("X-CSRF-Token", csrf)
			}
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
			}
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			return res.StatusCode, string(data)
		}
		query := "mutation($input:CreateShareInput!){createShare(input:$input){url share{id permission expiresAt}}}"
		_, response := gql(query, map[string]any{"input": map[string]any{"fileId": fileID, "expiresInSeconds": 3600}}, ownerCookie, ownerSession.CSRFToken)
		var result struct {
			Data struct {
				CreateShare struct {
					URL   string
					Share struct{ ID string }
				}
			}
		}
		if err := json.Unmarshal([]byte(response), &result); err != nil || result.Data.CreateShare.URL == "" {
			t.Fatal("GraphQL create failed", response)
		}
		link := strings.TrimPrefix(result.Data.CreateShare.URL, "/share#")
		inspect := "query($token:String!){sharedFile(token:$token){name sizeBytes detectedMIME previewAllowed}}"
		_, response = gql(inspect, map[string]any{"token": link}, "", "")
		if !strings.Contains(response, "UNAUTHENTICATED") {
			t.Fatal("cookie-less share query accepted", response)
		}
		status, response := gql(inspect, map[string]any{"token": link}, anonCookie, "")
		if status != 403 {
			t.Fatal("CSRF bypass", status, response)
		}
		_, response = gql(inspect, map[string]any{"token": link}, anonCookie, anonSession.CSRFToken)
		if strings.Contains(response, "errors") || !strings.Contains(response, "example.txt") || strings.Contains(response, fileID) {
			t.Fatal("metadata response", response)
		}
		issue := func(token, mode string) string {
			t.Helper()
			_, payload := gql("mutation($token:String!,$mode:FileAccessMode!){createSharedAccess(token:$token,mode:$mode){url expiresAt}}", map[string]any{"token": token, "mode": mode}, anonCookie, anonSession.CSRFToken)
			var reply struct {
				Data struct{ CreateSharedAccess struct{ URL string } }
			}
			if err := json.Unmarshal([]byte(payload), &reply); err != nil || reply.Data.CreateSharedAccess.URL == "" {
				t.Fatal("shared grant failed", payload)
			}
			return reply.Data.CreateSharedAccess.URL
		}
		fetch := func(path, method, cookie, byteRange string) (int, string, http.Header) {
			t.Helper()
			reset()
			req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, nil)
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
			}
			if byteRange != "" {
				req.Header.Set("Range", byteRange)
			}
			res, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			data, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			return res.StatusCode, string(data), res.Header
		}
		path := issue(link, "DOWNLOAD")
		status, _, _ = fetch(path, "GET", recipientCookie, "")
		if status != 404 {
			t.Fatal("another session redeemed public byte grant", status)
		}
		status, _, _ = fetch(path, "HEAD", anonCookie, "")
		if status != 200 {
			t.Fatal("shared HEAD", status)
		}
		overview, err := service.List(ownerCtx, fileID)
		if err != nil || overview.DownloadStarts != 0 {
			t.Fatal("HEAD counted", overview, err)
		}
		status, payload, headers := fetch(path, "GET", anonCookie, "bytes=0-5")
		if status != 206 || payload != body[:6] || !strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") || headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal("shared range", status, payload, headers)
		}
		status, payload, _ = fetch(path, "GET", anonCookie, "")
		if status != 200 || payload != body {
			t.Fatal("shared GET", status, payload)
		}
		overview, err = service.List(ownerCtx, fileID)
		if err != nil || overview.DownloadStarts != 1 {
			t.Fatal("range retry overcounted", overview, err)
		}
		_, response = gql("query($id:ID!){fileShares(fileId:$id){downloadStarts shares{id}}}", map[string]any{"id": fileID}, ownerCookie, ownerSession.CSRFToken)
		if !strings.Contains(response, "\"downloadStarts\":\"1\"") || strings.Contains(response, link) {
			t.Fatal("owner tracking query", response)
		}
		// Mutation aliases must be rejected before creating either share.
		_, response = gql("mutation($input:CreateShareInput!){a:createShare(input:$input){url} b:createShare(input:$input){url}}", map[string]any{"input": map[string]any{"fileId": fileID, "expiresInSeconds": 3600}}, ownerCookie, ownerSession.CSRFToken)
		if !strings.Contains(response, "INVALID_INPUT") {
			t.Fatal("mixed share mutations accepted", response)
		}
		_, response = gql("mutation($id:ID!){revokeShare(id:$id)}", map[string]any{"id": result.Data.CreateShare.Share.ID}, ownerCookie, ownerSession.CSRFToken)
		if !strings.Contains(response, "\"revokeShare\":true") {
			t.Fatal("GraphQL revoke failed", response)
		}
		status, _, _ = fetch(path, "GET", anonCookie, "")
		if status != 404 {
			t.Fatal("revoked URL served", status)
		}
		png := string([]byte{137, 80, 78, 71, 13, 10, 26, 10}) + "shared-preview"
		image, err := publisher.Publish(ownerCtx, []*upload.Staged{f.staged(png)})
		if err != nil {
			t.Fatal(err)
		}
		preview, err := service.Create(ownerCtx, image[0].ID, "PREVIEW_AND_DOWNLOAD", 3600, nil)
		if err != nil {
			t.Fatal(err)
		}
		path = issue(tokenOf(preview), "PREVIEW")
		status, payload, headers = fetch(path, "GET", anonCookie, "")
		if status != 200 || payload != png || headers.Get("Content-Type") != "image/png" || !strings.HasPrefix(headers.Get("Content-Disposition"), "inline;") {
			t.Fatal("shared preview", status, headers)
		}
		overview, err = service.List(ownerCtx, image[0].ID)
		if err != nil || overview.DownloadStarts != 0 {
			t.Fatal("preview counted as download", overview, err)
		}
		_, binding, _ := auth.BrowserBinding(anonCtx)
		exec("UPDATE vault.sessions SET revoked_at=clock_timestamp() WHERE token_hash=$1", binding)
		status, _, _ = fetch(path, "GET", anonCookie, "")
		if status != 401 {
			t.Fatal("revoked anonymous session served", status)
		}
		_ = recipientSession
	})
	t.Run("delete waits for admitted open then revokes future opens", func(t *testing.T) {
		request, _, _, _ := f.user(100)
		input, err := publisher.Publish(request, []*upload.Staged{f.staged("sharing-delete-race")})
		if err != nil {
			t.Fatal(err)
		}
		sh, err := service.Create(request, input[0].ID, "DOWNLOAD", 3600, nil)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := service.CreateAccess(recipientCtx, tokenOf(sh), "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		type result struct {
			content files.OpenContent
			err     error
		}
		opened := make(chan result, 1)
		go func() {
			content, err := second.OpenAccess(recipientCtx, grantToken(grant), gatedSharedRead{f.local, entered, release})
			opened <- result{content, err}
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("open did not acquire its authorization")
		}
		deleted := make(chan error, 1)
		go func() { deleted <- reader.Delete(request, input[0].ID) }()
		select {
		case err := <-deleted:
			t.Fatal("deletion passed open lock", err)
		case <-time.After(100 * time.Millisecond):
		}
		unblock()
		resultValue := <-opened
		if resultValue.err != nil {
			t.Fatal(resultValue.err)
		}
		data, err := io.ReadAll(resultValue.content.Body)
		_ = resultValue.content.Body.Close()
		if err != nil || string(data) != "sharing-delete-race" {
			t.Fatal("admitted handle lost", err)
		}
		if err := <-deleted; err != nil {
			t.Fatal(err)
		}
		if _, err := service.OpenAccess(recipientCtx, grantToken(grant), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("future open survived deletion", err)
		}
	})
	t.Run("shared grant capacity is session scoped", func(t *testing.T) {
		request, _, _, _ := f.user(100)
		sh := create(nil, "DOWNLOAD")
		for i := 0; i < 64; i++ {
			if _, err := service.CreateAccess(request, tokenOf(sh), "DOWNLOAD"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := second.CreateAccess(request, tokenOf(sh), "DOWNLOAD"); !errors.Is(err, sharing.ErrLimit) {
			t.Fatal("shared grant cap bypass", err)
		}
	})

	t.Run("reciprocal sharing uses consistent lock order", func(t *testing.T) {
		other, err := publisher.Publish(recipientCtx, []*upload.Staged{f.staged("reciprocal-sharing")})
		if err != nil {
			t.Fatal(err)
		}
		outgoing := create(&recipientID, "DOWNLOAD")
		incoming, err := service.Create(recipientCtx, other[0].ID, "DOWNLOAD", 3600, &ownerID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		failures := make(chan error, 2)
		for _, pair := range []struct {
			request context.Context
			token   string
		}{{ownerCtx, tokenOf(incoming)}, {recipientCtx, tokenOf(outgoing)}} {
			wg.Add(1)
			go func(request context.Context, token string) {
				defer wg.Done()
				for i := 0; i < 8; i++ {
					if _, err := second.CreateAccess(request, token, "DOWNLOAD"); err != nil {
						failures <- err
						return
					}
				}
			}(pair.request, pair.token)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Fatal("reciprocal sharing failed", err)
		}
	})
}
