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
	"strconv"
	"strings"
	"testing"
	"time"

	administration "full-stack-file-vault.local/api/internal/admin"
	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/graph"
	"full-stack-file-vault.local/api/internal/server"
	"full-stack-file-vault.local/api/internal/sharing"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/jackc/pgx/v5"
)

func testAdministration(t *testing.T, ctx context.Context, db *pgx.Conn, dsn, directory string) {
	f := newPublicationFixture(t, ctx, db, dsn, directory)
	admin, err := administration.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	shares, err := sharing.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := files.NewStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	publisher := f.publisher(f.pool, f.local)
	actorCtx, actorID, actorCookie, actorSession := f.user(1000)
	targetCtx, targetID, targetCookie, _ := f.user(100)
	ordinaryCtx, ordinaryID, ordinaryCookie, ordinarySession := f.user(100)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE vault.users SET role='ADMIN' WHERE id=$1", actorID)
	// Capture a real ADMIN middleware snapshot, then later verify demotion defeats it.
	req := httptest.NewRequest("POST", "https://vault.example.com/graphql", nil).WithContext(ctx)
	req.Header.Set("Origin", "https://vault.example.com")
	req.Header.Set("X-CSRF-Token", actorSession.CSRFToken)
	req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: actorCookie})
	f.browser.Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { actorCtx = r.Context() })).ServeHTTP(httptest.NewRecorder(), req)
	before, err := admin.Statistics(actorCtx)
	if err != nil {
		t.Fatal(err)
	}
	data := "admin-visible-bytes"
	input, err := publisher.Publish(targetCtx, []*upload.Staged{f.staged(data), f.staged(data)})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("resource audit commits and rolls back with its mutation", func(t *testing.T) {
		var uploads, sessions int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM vault.admin_audit WHERE target_user_id=$1 AND action='FILE_UPLOADED'", targetID).Scan(&uploads); err != nil || uploads != 2 {
			t.Fatal("upload audit missing", uploads, err)
		}
		if err := db.QueryRow(ctx, "SELECT count(*) FROM vault.admin_audit WHERE target_user_id=$1 AND action='AUTHENTICATED_SESSION_CREATED'", targetID).Scan(&sessions); err != nil || sessions != 1 {
			t.Fatal("session audit missing", sessions, err)
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, "DELETE FROM vault.files WHERE id=$1", input[0].ID); err != nil {
			t.Fatal(err)
		}
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM vault.admin_audit WHERE file_id=$1 AND action='FILE_DELETED' AND actor_id IS NULL", input[0].ID).Scan(&count); err != nil || count != 1 {
			t.Fatal("deletion audit missing or incorrectly attributed", count, err)
		}
		if err = tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(ctx, "SELECT count(*) FROM vault.admin_audit WHERE file_id=$1 AND action='FILE_DELETED'", input[0].ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("rolled-back deletion left an audit event", count, err)
		}
	})
	t.Run("user and stale admin denied", func(t *testing.T) {
		if _, err := admin.Users(ordinaryCtx, 20, nil); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("user list allowed", err)
		}
		if _, err := admin.Files(ordinaryCtx, 20, nil, nil); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("file list allowed", err)
		}
		if _, err := admin.Statistics(ordinaryCtx); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("global stats allowed", err)
		}
		if _, err := admin.Audit(ordinaryCtx, 20, nil); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("audit allowed", err)
		}
		if _, err := admin.SetQuota(ordinaryCtx, targetID, "200"); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("quota mutation allowed", err)
		}
		if _, err := admin.SetDisabled(ordinaryCtx, targetID, true); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("disable mutation allowed", err)
		}
		exec("UPDATE vault.users SET role='USER' WHERE id=$1", actorID)
		if _, err := admin.Statistics(actorCtx); !errors.Is(err, administration.ErrForbidden) {
			t.Fatal("stale ADMIN snapshot trusted", err)
		}
		exec("UPDATE vault.users SET role='ADMIN' WHERE id=$1", actorID)
	})
	t.Run("bounded lists and global dedup statistics", func(t *testing.T) {
		page, err := admin.Users(actorCtx, 1, nil)
		if err != nil || len(page.Nodes) != 1 || !page.HasNextPage || page.EndCursor == nil {
			t.Fatal("user pagination", page, err)
		}
		next, err := admin.Users(actorCtx, 1, page.EndCursor)
		if err != nil || len(next.Nodes) != 1 || next.Nodes[0].ID <= page.Nodes[0].ID {
			t.Fatal("user cursor", next, err)
		}
		list, err := admin.Files(actorCtx, 1, nil, &targetID)
		if err != nil || len(list.Nodes) != 1 || !list.HasNextPage || list.Nodes[0].OwnerID != targetID {
			t.Fatal("admin files", list, err)
		}
		more, err := admin.Files(actorCtx, 1, list.EndCursor, &targetID)
		if err != nil || len(more.Nodes) != 1 || more.HasNextPage || more.Nodes[0].File.ID == list.Nodes[0].File.ID {
			t.Fatal("file pagination", more, err)
		}
		stats, err := admin.Statistics(actorCtx)
		if err != nil {
			t.Fatal(err)
		}
		number := func(v string) int64 {
			t.Helper()
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
		if number(stats.LogicalBytes)-number(before.LogicalBytes) != int64(2*len(data)) || number(stats.ReferencedBytes)-number(before.ReferencedBytes) != int64(len(data)) || number(stats.FileCount)-number(before.FileCount) != 2 {
			t.Fatal("global storage semantics", before, stats)
		}
		// Administrative visibility is not private byte access or owner authority.
		if _, err := reader.CreateAccess(actorCtx, input[0].ID, "DOWNLOAD"); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("admin private byte bypass", err)
		}
		if err := reader.Delete(actorCtx, input[0].ID); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("admin private deletion bypass", err)
		}
	})

	t.Run("legacy and verified email display", func(t *testing.T) {
		emailCtx, emailID, _, _ := f.user(1000)
		_, legacyID, _, _ := f.user(1000)
		_, emptyID, _, _ := f.user(1000)
		hash, e := auth.HashPassword([]byte("synthetic identity regression password"))
		if e != nil {
			t.Fatal(e)
		}
		exec("UPDATE vault.users SET email_address='Admin.Identity@example.test',email_normalized='admin.identity@example.test',email_verified_at=clock_timestamp() WHERE id=$1", emailID)
		exec("INSERT INTO vault.user_identities(user_id,provider,provider_subject,password_hash) VALUES($1,'password','admin.identity@example.test',$2)", emailID, hash)
		exec("INSERT INTO vault.credentials(user_id,login_name,password_hash) VALUES($1,'identity.legacy',$2)", legacyID, hash)
		// Multiple identity methods must not duplicate rows, and legacy display wins.
		exec("UPDATE vault.users SET email_address='legacy.identity@example.test',email_normalized='legacy.identity@example.test',email_verified_at=clock_timestamp() WHERE id=$1", legacyID)
		exec("INSERT INTO vault.user_identities(user_id,provider,provider_subject,password_hash) VALUES($1,'password','legacy.identity@example.test',$2)", legacyID, hash)
		found := map[string]*string{}
		var cursor *string
		for {
			page, e := admin.Users(actorCtx, 1, cursor)
			if e != nil {
				t.Fatal(e)
			}
			for _, user := range page.Nodes {
				if _, ok := found[user.ID]; ok {
					t.Fatal("duplicate identity row")
				}
				found[user.ID] = user.LoginName
			}
			if !page.HasNextPage {
				break
			}
			cursor = page.EndCursor
		}
		if found[emailID] == nil || *found[emailID] != "Admin.Identity@example.test" {
			t.Fatal("email identity missing")
		}
		if found[legacyID] == nil || *found[legacyID] != "identity.legacy" {
			t.Fatal("legacy display changed")
		}
		if _, ok := found[emptyID]; !ok || found[emptyID] != nil {
			t.Fatal("identity fabricated")
		}
		if _, e = publisher.Publish(emailCtx, []*upload.Staged{f.staged("email identity file")}); e != nil {
			t.Fatal(e)
		}
		for _, check := range []struct {
			name   string
			ctx    context.Context
			filter string
			want   int
		}{
			{"verified email", emailCtx, "ADMIN.IDENTITY", 1},
			{"different identity", emailCtx, "identity.legacy", 0},
			{"literal wildcard", emailCtx, "%", 0},
			{"owner isolation", ordinaryCtx, "admin.identity", 0},
		} {
			got, err := reader.List(check.ctx, files.ListOptions{First: 50, Filter: files.Filter{UploaderNameContains: &check.filter}})
			if err != nil || len(got.Nodes) != check.want {
				t.Fatalf("uploader filter %s: count=%d err=%v", check.name, len(got.Nodes), err)
			}
		}
		page, e := admin.Files(actorCtx, 50, nil, &emailID)
		if e != nil || len(page.Nodes) != 1 || page.Nodes[0].LoginName == nil || *page.Nodes[0].LoginName != "Admin.Identity@example.test" {
			t.Fatal("file uploader identity missing")
		}
		if page.Nodes[0].OwnerID != emailID || len(page.Nodes[0].File.Tags) != 0 {
			t.Fatal("owner or private tags changed")
		}
		changed, e := admin.SetQuota(actorCtx, emailID, "2000")
		if e != nil || changed.LoginName == nil || *changed.LoginName != "Admin.Identity@example.test" {
			t.Fatal("mutation identity missing")
		}
		disabled, e := admin.SetDisabled(actorCtx, emailID, true)
		if e != nil || disabled.LoginName == nil || *disabled.LoginName != "Admin.Identity@example.test" {
			t.Fatal("disabled identity missing")
		}
	})
	t.Run("quota mutation and append-only audit are atomic", func(t *testing.T) {
		if _, err := admin.SetQuota(actorCtx, targetID, strconv.Itoa(2*len(data)-1)); !errors.Is(err, administration.ErrConflict) {
			t.Fatal("quota below usage accepted", err)
		}
		user, err := admin.SetQuota(actorCtx, targetID, "10000001")
		if err != nil || user.QuotaBytes != "10000001" {
			t.Fatal("quota cannot exceed default", user, err)
		}
		events, err := admin.Audit(actorCtx, 1, nil)
		if err != nil || len(events.Nodes) != 1 {
			t.Fatal("audit missing", err)
		}
		event := events.Nodes[0]
		if (event.ActorID == nil || *event.ActorID != actorID) || (event.TargetUserID == nil || *event.TargetUserID != targetID) || event.Action != "USER_QUOTA_CHANGED" || event.PreviousQuota != "100" || event.NewQuota != "10000001" {
			t.Fatal("audit mismatch", event)
		}
		for _, sql := range []string{"UPDATE vault.admin_audit SET action='USER_ENABLED' WHERE false", "DELETE FROM vault.admin_audit WHERE false", "UPDATE vault.users SET role='ADMIN' WHERE false"} {
			if _, err := f.pool.Exec(ctx, sql); err == nil {
				t.Fatal("runtime escalation/tampering permitted")
			}
		}
		exec("REVOKE INSERT ON vault.admin_audit FROM vault_runtime")
		_, changeErr := admin.SetQuota(actorCtx, targetID, "9999999")
		exec("GRANT INSERT ON vault.admin_audit TO vault_runtime")
		if changeErr == nil {
			t.Fatal("mutation succeeded without audit")
		}
		var quota string
		if err := db.QueryRow(ctx, "SELECT quota_bytes::text FROM vault.users WHERE id=$1", targetID).Scan(&quota); err != nil || quota != "10000001" {
			t.Fatal("unaudited quota committed", quota, err)
		}
	})
	t.Run("TLS admin API authorizes each operation", func(t *testing.T) {
		limiter, err := auth.NewUserLimiter(f.pool, 2)
		if err != nil {
			t.Fatal(err)
		}
		app, err := graph.NewApplicationHandler(f.logger, f.browser, func(ctx context.Context, token string) (string, auth.Session, bool, error) {
			return f.sessions.BeginSession(ctx, token, 60)
		}, f.sessions.LoginBrowser, f.sessions.Revoke, publisher, reader, limiter, graph.MultipartConfig{Directory: t.TempDir(), MaxFileBytes: 100, MaxRequestBytes: 4096, MaxFiles: 3, MaxConcurrentRequests: 2, Timeout: 5 * time.Second}, graph.Services{Sharing: shares, Administration: admin})
		if err != nil {
			t.Fatal(err)
		}
		router := server.New("", func(context.Context) error { return nil }, f.logger, app)
		srv := httptest.NewTLSServer(router.Handler)
		defer srv.Close()
		gql := func(query string, variables map[string]any, privileged bool) string {
			t.Helper()
			exec("UPDATE vault.user_request_windows SET accepted_at='{}' WHERE user_id IN ($1,$2)", actorID, ordinaryID)
			payload, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
			req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/graphql", bytes.NewReader(payload))
			cookie, csrf := ordinaryCookie, ordinarySession.CSRFToken
			if privileged {
				cookie, csrf = actorCookie, actorSession.CSRFToken
			}
			req.Header.Set("Origin", "https://vault.example.com")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
			req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: cookie})
			response, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			result, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return string(result)
		}
		operations := []string{
			"{adminUsers{nodes{id role quotaBytes} pageInfo{hasNextPage}}}",
			"{adminFiles{nodes{ownerId file{id name}} pageInfo{hasNextPage}}}",
			"{adminStorageStats{userCount logicalBytes referencedBytes}}",
			"{adminAudit{nodes{id action actorId} pageInfo{hasNextPage}}}",
			"mutation($id:ID!){adminSetQuota(userId:$id,quotaBytes:\"10000002\"){id quotaBytes}}",
			"mutation($id:ID!){adminSetUserDisabled(userId:$id,disabled:false){id disabledAt}}",
		}
		for _, operation := range operations {
			vars := map[string]any{"id": targetID}
			response := gql(operation, vars, false)
			if !strings.Contains(response, "FORBIDDEN") {
				t.Fatal("ordinary user reached admin API", response)
			}
			response = gql(operation, vars, true)
			if strings.Contains(response, "\"errors\"") {
				t.Fatal("authorized admin failed", response)
			}
			if strings.Contains(response, "password_hash") || strings.Contains(response, "storage_key") || strings.Contains(response, "sha256") {
				t.Fatal("sensitive admin response", response)
			}
		}
	})
	t.Run("disable revokes sessions and links without deleting content", func(t *testing.T) {
		sh, err := shares.Create(targetCtx, input[0].ID, "DOWNLOAD", 3600, nil)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := shares.CreateAccess(actorCtx, strings.TrimPrefix(sh.URL, "/share#"), "DOWNLOAD")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.SetDisabled(actorCtx, actorID, true); !errors.Is(err, administration.ErrConflict) {
			t.Fatal("admin lockout permitted", err)
		}
		user, err := admin.SetDisabled(actorCtx, targetID, true)
		if err != nil || user.DisabledAt == nil {
			t.Fatal("disable failed", user, err)
		}
		if _, err := f.sessions.LookupAndTouch(ctx, targetCookie); !errors.Is(err, auth.ErrInvalidSession) {
			t.Fatal("disabled session survived", err)
		}
		if _, err := shares.OpenAccess(actorCtx, strings.TrimPrefix(grant.URL, "/shared-content/"), f.local); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("disabled link survived", err)
		}
		events, err := admin.Audit(actorCtx, 1, nil)
		if err != nil || len(events.Nodes) != 1 || events.Nodes[0].Action != "USER_DISABLED" || events.Nodes[0].RevokedShares != "1" || events.Nodes[0].RevokedSessions == "0" {
			t.Fatal("revocation not audited", events, err)
		}
		user, err = admin.SetDisabled(actorCtx, targetID, false)
		if err != nil || user.DisabledAt != nil {
			t.Fatal("enable failed", user, err)
		}
		if _, err := f.sessions.LookupAndTouch(ctx, targetCookie); !errors.Is(err, auth.ErrInvalidSession) {
			t.Fatal("enable resurrected session", err)
		}
		if _, err := shares.Inspect(actorCtx, strings.TrimPrefix(sh.URL, "/share#")); !errors.Is(err, files.ErrNotFound) {
			t.Fatal("enable resurrected link", err)
		}
		var count int
		if err := db.QueryRow(ctx, "SELECT count(*) FROM vault.files WHERE owner_id=$1", targetID).Scan(&count); err != nil || count != 2 {
			t.Fatal("disable deleted content", count, err)
		}
	})
}
