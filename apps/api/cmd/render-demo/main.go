// Command render-demo packages a temporary free demo behind one public gateway.
// File bytes live in PostgreSQL; local directories hold disposable staging only.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
)

type credentials struct{ operator, runtime, gc, runtimePassword, gcPassword string }

func loadCredentials() (credentials, error) {
	var c credentials
	u, err := url.Parse(os.Getenv("DEMO_OPERATOR_URL"))
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.Path == "" {
		return c, errors.New("valid DEMO_OPERATOR_URL required")
	}
	if password, ok := u.User.Password(); !ok || password == "" {
		return c, errors.New("operator password required")
	}
	// Render's private database certificates do not support verify-full; require TLS.
	// A local isolated rehearsal may explicitly select disable in its input URL.
	query := u.Query()
	if query.Get("sslmode") == "" {
		query.Set("sslmode", "require")
	}
	u.RawQuery = query.Encode()
	c.operator = u.String()
	for _, item := range []struct {
		key, role     string
		password, url *string
	}{
		{"DEMO_RUNTIME_SEED", "vault_runtime", &c.runtimePassword, &c.runtime},
		{"DEMO_GC_SEED", "vault_gc", &c.gcPassword, &c.gc},
	} {
		seed := os.Getenv(item.key)
		if len(seed) < 32 {
			return c, errors.New(item.key + " must contain at least 32 random characters")
		}
		sum := sha256.Sum256([]byte(seed))
		*item.password = hex.EncodeToString(sum[:])
		clone := *u
		clone.User = url.UserPassword(item.role, *item.password)
		*item.url = clone.String()
	}
	return c, nil
}

func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "setup" {
		err = setup()
	} else if len(os.Args) == 1 {
		// Free Render services have no pre-deploy phase or shell. Setup must be
		// repeatable on every wake/replacement before workers accept requests.
		if err = setup(); err == nil {
			err = serve()
		}
	} else {
		err = errors.New("use render-demo or render-demo setup")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func serve() error {
	c, err := loadCredentials()
	if err != nil {
		return err
	}
	origin := os.Getenv("PUBLIC_ORIGIN")
	if origin == "" {
		origin = os.Getenv("RENDER_EXTERNAL_URL")
	}
	if err = auth.ValidateBrowserConfig(auth.BrowserConfig{Origin: origin}); err != nil {
		return errors.New("PUBLIC_ORIGIN or RENDER_EXTERNAL_URL must be an HTTPS origin")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "10000"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 || n == 8080 || n == 8082 {
		return errors.New("invalid gateway PORT")
	}
	for _, path := range []string{"/data/staging", "/data/blobs"} {
		if err = os.MkdirAll(path, 0700); err != nil {
			return errors.New("temporary staging directory must be writable by UID 65532")
		}
		if err = os.Chmod(path, 0700); err != nil {
			return errors.New("cannot secure storage directories")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return errors.New("gateway port unavailable")
	}
	defer listener.Close()
	common := []string{"APP_ENV=production", "BLOB_STORAGE_BACKEND=postgres-demo", "BLOB_STORAGE_DIR=/data/blobs", "UPLOAD_STAGING_DIR=/data/staging", "GOMEMLIMIT=128MiB"}
	children := []*exec.Cmd{
		exec.Command("/app/server"),
		exec.Command("/app/collect"),
	}
	// Do not inherit operator, demo-account or the other worker's credentials.
	children[0].Env = append(append([]string{}, common...), "DATABASE_URL="+c.runtime, "PUBLIC_ORIGIN="+origin, "HTTP_ADDR=127.0.0.1:8080", "USER_CALLS_PER_SECOND=2", "UPLOAD_MAX_FILE_BYTES=10000000", "UPLOAD_MAX_REQUEST_BYTES=11000000", "UPLOAD_MAX_CONCURRENT=2")
	children[1].Env = append(append([]string{}, common...), "GC_DATABASE_URL="+c.gc, "GC_METRICS_ADDR=127.0.0.1:8082")
	exited := make(chan error, 2)
	started := 0
	observed := 0
	defer func() {
		for _, child := range children[:started] {
			_ = child.Process.Signal(syscall.SIGTERM)
		}
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for observed < started {
			select {
			case <-exited:
				observed++
			case <-timer.C:
				for _, child := range children[:started] {
					_ = child.Process.Kill()
				}
				for observed < started {
					<-exited
					observed++
				}
				return
			}
		}
	}()
	for _, child := range children {
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err = child.Start(); err != nil {
			return errors.New("demo worker could not start")
		}
		started++
		go func(cmd *exec.Cmd) { exited <- cmd.Wait() }(child)
	}
	srv := &http.Server{Handler: gateway("/app/public"), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	var result error
	select {
	case <-ctx.Done():
	case <-exited:
		observed++
		result = errors.New("demo worker stopped; restarting the service is required")
	case <-done:
		result = errors.New("demo gateway stopped")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	return result
}
