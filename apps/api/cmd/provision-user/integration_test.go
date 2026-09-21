//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"github.com/jackc/pgx/v5"
)

// This test owns a separate container, database and runtime role. Never point it
// at a development database: migration 2 creates a cluster-scoped role.
func TestProvisionCommandIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	random := func() string {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			t.Fatal("random generation failed")
		}
		return hex.EncodeToString(b)
	}
	adminPassword, runtimePassword, accountPassword := random(), random(), random()+" "
	name := "vault-credentials-test-" + random()[:12]
	docker := func(input string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = append(os.Environ(), "POSTGRES_PASSWORD="+adminPassword)
		return cmd.CombinedOutput()
	}
	if _, err := docker("", "info", "--format", "{{.ServerVersion}}"); err != nil {
		t.Fatal("Docker engine unavailable")
	}
	// Cleanup only the exact randomly named container owned by this test; -v removes
	// its anonymous PostgreSQL volume. The Compose project is never touched.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := exec.CommandContext(cleanupCtx, "docker", "rm", "-f", "-v", name).Run(); err != nil {
			t.Error("test container cleanup failed")
		}
	})
	if _, err := docker("", "run", "-d", "--name", name, "-e", "POSTGRES_PASSWORD", "-e", "POSTGRES_DB=vault_test", "-p", "127.0.0.1::5432", "postgres:17-bookworm"); err != nil {
		t.Fatal("isolated PostgreSQL startup failed")
	}
	ready := false
	for i := 0; i < 60; i++ {
		if _, err := docker("", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "vault_test"); err == nil {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("database startup timed out")
		case <-time.After(time.Second):
		}
	}
	if !ready {
		t.Fatal("database not ready")
	}
	port, err := docker("", "port", name, "5432/tcp")
	if err != nil {
		t.Fatal("cannot resolve database port")
	}
	dsn := func(user, password string) string {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: strings.TrimSpace(string(port)), Path: "/vault_test", RawQuery: "sslmode=disable"}).String()
	}
	conn, err := pgx.Connect(ctx, dsn("postgres", adminPassword))
	if err != nil {
		t.Fatal("test database connection failed")
	}
	defer conn.Close(context.Background())
	for _, file := range []string{"000001_initial_schema.up.sql", "000002_runtime_role.up.sql", "000003_credentials.up.sql", "000004_sessions.up.sql", "000005_prelogin.up.sql", "000006_bootstrap_budget.up.sql", "000007_login_throttle.up.sql"} {
		sql, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("migration %s failed", file)
		}
	}
	t.Run("publication migration", func(t *testing.T) { testPublicationMigration(t, ctx, conn) })
	if _, err := conn.Exec(ctx, migrationSQL(t, "000009_user_rate_limit.up.sql")); err != nil {
		t.Fatal(err)
	}
	// Empty-schema round trips verify worker-role privileges can be rolled back.
	for _, migration := range []string{"000010_file_access.up.sql", "000010_file_access.down.sql", "000010_file_access.up.sql", "000011_cleanup_worker.up.sql", "000011_cleanup_worker.down.sql", "000011_cleanup_worker.up.sql", "000012_sharing.up.sql", "000012_sharing.down.sql", "000012_sharing.up.sql", "000013_administration.up.sql", "000013_administration.down.sql", "000013_administration.up.sql", "000014_upload_receipts.up.sql", "000014_upload_receipts.down.sql", "000014_upload_receipts.up.sql", "000015_private_tags.up.sql", "000015_private_tags.down.sql", "000015_private_tags.up.sql"} {
		if _, err := conn.Exec(ctx, migrationSQL(t, migration)); err != nil {
			t.Fatalf("migration %s: %v", migration, err)
		}
	}
	if _, err := conn.Exec(ctx, "ALTER ROLE vault_gc LOGIN PASSWORD '"+runtimePassword+"'"); err != nil {
		t.Fatal("cleanup test role setup failed")
	}
	// Generated hex cannot contain quotes; never include an operator-supplied value.
	if _, err := conn.Exec(ctx, "ALTER ROLE vault_runtime LOGIN PASSWORD '"+runtimePassword+"'"); err != nil {
		t.Fatal("test runtime setup failed")
	}
	binary := filepath.Join(t.TempDir(), "provision-user")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %s", out)
	}
	run := func(databaseURL, password string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, binary, args...)
		// Replace, rather than duplicate, any caller's operator DSN in the child env.
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(entry), "PROVISION_DATABASE_URL=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "PROVISION_DATABASE_URL="+databaseURL)
		cmd.Stdin = strings.NewReader(password)
		output, err := cmd.CombinedOutput()
		for _, secret := range []string{adminPassword, runtimePassword, accountPassword, "$argon2id$"} {
			if strings.Contains(string(output), secret) {
				t.Fatal("command leaked sensitive output")
			}
		}
		return string(output), err
	}
	operatorDSN := dsn("postgres", adminPassword)
	for _, tc := range []struct{ name, role string }{{"Reviewer.One", "USER"}, {"reviewer.admin", "ADMIN"}} {
		output, err := run(operatorDSN, accountPassword, "-login", tc.name, "-role", tc.role)
		if err != nil || !strings.Contains(output, "Created user") {
			t.Fatal("valid provisioning failed")
		}
		var role, hash string
		err = conn.QueryRow(ctx, `SELECT u.role,c.password_hash FROM vault.users u JOIN vault.credentials c ON c.user_id=u.id WHERE c.login_name=$1`, strings.ToLower(tc.name)).Scan(&role, &hash)
		if err != nil || role != tc.role {
			t.Fatal("account role/normalization incorrect")
		}
		ok, err := auth.VerifyPassword([]byte(accountPassword), hash)
		if err != nil || !ok {
			t.Fatal("stored password does not verify")
		}
		ok, err = auth.VerifyPassword([]byte(strings.TrimSpace(accountPassword)), hash)
		if err != nil || ok {
			t.Fatal("command changed password whitespace")
		}
	}
	var beforeHash string
	if err := conn.QueryRow(ctx, "SELECT password_hash FROM vault.credentials WHERE login_name='reviewer.one'").Scan(&beforeHash); err != nil {
		t.Fatal("hash read failed")
	}
	output, err := run(operatorDSN, random(), "-login", " REVIEWER.ONE ", "-role", "ADMIN")
	if err == nil || !strings.Contains(output, "login already exists") {
		t.Fatal("duplicate did not fail correctly")
	}
	var afterHash, afterRole string
	if err := conn.QueryRow(ctx, `SELECT c.password_hash,u.role FROM vault.credentials c JOIN vault.users u ON u.id=c.user_id WHERE c.login_name='reviewer.one'`).Scan(&afterHash, &afterRole); err != nil {
		t.Fatal("duplicate state check failed")
	}
	if beforeHash != afterHash || afterRole != "USER" {
		t.Fatal("duplicate changed existing account")
	}
	output, err = run(dsn("vault_runtime", runtimePassword), accountPassword, "-login", "unauthorized", "-role", "ADMIN")
	if err == nil || !strings.Contains(output, "cannot create user") {
		t.Fatal("runtime provisioning was not denied at user insertion")
	}
	var users, credentials int
	if err := conn.QueryRow(ctx, "SELECT (SELECT count(*) FROM vault.users),(SELECT count(*) FROM vault.credentials)").Scan(&users, &credentials); err != nil {
		t.Fatal("count check failed")
	}
	if users != 2 || credentials != 2 {
		t.Fatal("failed command left partial records")
	}
	t.Run("session lifecycle", func(t *testing.T) { testSessionLifecycle(t, ctx, conn, dsn("vault_runtime", runtimePassword)) })
	t.Run("pre-login rotation", func(t *testing.T) {
		testLoginRotation(t, ctx, conn, dsn("vault_runtime", runtimePassword), accountPassword)
	})
	t.Run("browser boundary", func(t *testing.T) { testBrowserSecurity(t, ctx, conn, dsn("vault_runtime", runtimePassword)) })
	t.Run("GraphQL bootstrap", func(t *testing.T) { testBootstrap(t, ctx, conn, dsn("vault_runtime", runtimePassword)) })
	t.Run("GraphQL login", func(t *testing.T) {
		testGraphQLLogin(t, ctx, conn, dsn("vault_runtime", runtimePassword), accountPassword)
	})
	t.Run("authenticated me", func(t *testing.T) { testMe(t, ctx, conn, dsn("vault_runtime", runtimePassword)) })
	t.Run("GraphQL logout", func(t *testing.T) { testLogout(t, ctx, conn, dsn("vault_runtime", runtimePassword)) })
	storageDirectory := t.TempDir()
	t.Run("file publication", func(t *testing.T) {
		testPublication(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("per-user rate limit", func(t *testing.T) {
		testUserRateLimit(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("GraphQL uploads", func(t *testing.T) {
		testUploadAPI(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("private tags", func(t *testing.T) {
		testPrivateTags(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("file queries", func(t *testing.T) { testFiles(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory) })
	t.Run("file lifecycle", func(t *testing.T) {
		testLifecycle(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("sharing", func(t *testing.T) { testSharing(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory) })
	t.Run("administration", func(t *testing.T) {
		testAdministration(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("upload receipts", func(t *testing.T) {
		testUploadReceipts(t, ctx, conn, dsn("vault_runtime", runtimePassword), storageDirectory)
	})
	t.Run("cleanup", func(t *testing.T) {
		testCleanup(t, ctx, conn, dsn("vault_runtime", runtimePassword), dsn("vault_gc", runtimePassword), storageDirectory)
	})
	t.Run("demo PostgreSQL storage", func(t *testing.T) {
		testDemoStorage(t, ctx, conn, dsn("vault_runtime", runtimePassword), dsn("vault_gc", runtimePassword))
	})
	t.Log(fmt.Sprintf("Verified accounts and sessions; test container %s will be removed", name))
}
