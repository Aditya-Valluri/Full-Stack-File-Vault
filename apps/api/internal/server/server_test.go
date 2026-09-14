package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProbesAndApplicationBoundary(t *testing.T) {
	calls := 0
	srv := New("", func(ctx context.Context) error {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("readiness has no deadline")
		}
		return errors.New("secret database detail")
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/healthz", 200}, {"/readyz", 503}, {"/graphql", 404}, {"/api/upload", 404},
	} {
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status {
			t.Errorf("%s: got %d", tc.path, w.Code)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("dependency error exposed")
		}
	}
	if calls != 1 {
		t.Fatalf("readiness called %d times", calls)
	}
}

func TestShutdownDrainsInflightRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer srv.Close()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, listener, time.Second) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		res, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			res.Body.Close()
			if res.StatusCode != http.StatusNoContent {
				err = errors.New("request not completed")
			}
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		close(release)
		t.Fatalf("shutdown returned before request completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}
