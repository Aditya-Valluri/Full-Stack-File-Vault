package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"file-vault.local/api/internal/auth"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

const uploadTestSDL = `scalar Upload
type Query { serviceInfo: String! }
type Mutation { uploadFile(file: Upload!): Boolean! uploadFiles(files: [Upload!]!): Boolean! logout: Boolean! }`
const singleUploadJSON = `{"query":"mutation($file:Upload!){uploadFile(file:$file)}","variables":{"file":null}}`
const multipleUploadJSON = `{"query":"mutation($files:[Upload!]!){uploadFiles(files:$files)}","variables":{"files":[null,null]}}`

type multipartSchema struct {
	schema *ast.Schema
	run    func(context.Context) *graphql.Response
}

func (s multipartSchema) Schema() *ast.Schema { return s.schema }
func (s multipartSchema) Complexity(context.Context, string, string, int, map[string]any) (int, bool) {
	return 0, false
}
func (s multipartSchema) Exec(ctx context.Context) graphql.ResponseHandler {
	return graphql.OneShot(s.run(ctx))
}

type multipartFile struct{ key, name, data string }

func multipartPayload(t *testing.T, operations, mapping string, files ...multipartFile) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	writer := multipart.NewWriter(&b)
	if err := writer.WriteField("operations", operations); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("map", mapping); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="`+f.key+`"; filename="`+f.name+`"`)
		header.Set("Content-Type", "application/octet-stream")
		p, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(p, f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), writer.FormDataContentType()
}

func multipartHarness(t *testing.T, modify func(*MultipartConfig), run func(context.Context) *graphql.Response, identity ...auth.Session) (http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	config := MultipartConfig{Directory: dir, MaxRequestBytes: 2 << 20, MaxFileBytes: 1 << 20, MaxFiles: 2, MaxConcurrentRequests: 1, Timeout: time.Second}
	if modify != nil {
		modify(&config)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Input: uploadTestSDL})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	transport, err := NewMultipartTransport(config, schema, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv := handler.New(multipartSchema{schema: schema, run: run})
	srv.AddTransport(transport)
	srv.SetErrorPresenter(errorPresenter(logger))
	state := auth.Session{UserID: "user", Role: "USER", CSRFToken: strings.Repeat("c", 43)}
	if len(identity) > 0 {
		state = identity[0]
	}
	boundary, err := auth.NewBrowserSecurity(auth.BrowserConfig{Origin: "https://vault.example.com"}, loginTestSessions{state})
	if err != nil {
		t.Fatal(err)
	}
	return boundary.WrapBootstrap(srv, validateBootstrapRequest, func(context.Context, string) (string, auth.Session, bool, error) {
		t.Fatal("unexpected bootstrap")
		return "", auth.Session{}, false, nil
	}), dir
}

func multipartRequest(body io.Reader, contentType string) *http.Request {
	req := httptest.NewRequest("POST", "/graphql", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "https://vault.example.com")
	req.Header.Set("X-CSRF-Token", strings.Repeat("c", 43))
	req.AddCookie(&http.Cookie{Name: "__Host-vault_session", Value: strings.Repeat("A", 43)})
	return req
}

func noMultipartFiles(t *testing.T, dir string) {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary files remain: %d %v", len(files), err)
	}
}

func TestMultipartGqlgenExecutionAndCleanup(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		for _, length := range []int64{-1, 1, 0} {
			var held []io.ReadSeeker
			calls := 0
			h, dir := multipartHarness(t, nil, func(ctx context.Context) *graphql.Response {
				calls++
				variables := graphql.GetOperationContext(ctx).Variables
				raw := []any{variables["file"]}
				if multiple {
					raw = variables["files"].([]any)
				}
				for _, value := range raw {
					file, err := graphql.UnmarshalUpload(value)
					if err != nil {
						t.Fatal(err)
					}
					got, err := io.ReadAll(file.File)
					if err != nil || string(got) != "actual bytes" || file.Size != 12 {
						t.Fatal("gqlgen upload values incorrect")
					}
					held = append(held, file.File)
				}
				return &graphql.Response{Data: json.RawMessage(`{"ok":true}`)}
			})
			operations, mapping := singleUploadJSON, `{"0":["variables.file"]}`
			files := []multipartFile{{"0", "file.txt", "actual bytes"}}
			if multiple {
				operations = multipleUploadJSON
				mapping = `{"0":["variables.files.0"],"1":["variables.files.1"]}`
				files = append(files, multipartFile{"1", "copy.txt", "actual bytes"})
			}
			payload, ct := multipartPayload(t, operations, mapping, files...)
			req := multipartRequest(bytes.NewReader(payload), ct)
			if length != 0 {
				req.ContentLength = length
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 || calls != 1 || strings.Contains(w.Body.String(), `"errors"`) {
				t.Fatalf("execution failed: %d %s", w.Code, w.Body.String())
			}
			for _, reader := range held {
				if _, err := reader.Read(make([]byte, 1)); err == nil {
					t.Fatal("temporary reader still open")
				}
			}
			noMultipartFiles(t, dir)
		}
	}
}

func TestMultipartRejectsInvalidMapsOperationsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, operations, mapping string
		files                     []multipartFile
		modify                    func(*MultipartConfig)
		status                    int
	}{
		{name: "fanout", operations: singleUploadJSON, mapping: `{"0":["variables.file","variables.file"]}`, status: 400},
		{name: "missing file", operations: singleUploadJSON, mapping: `{"0":["variables.file"]}`, status: 400},
		{name: "unknown path", operations: singleUploadJSON, mapping: `{"0":["variables.other"]}`, status: 400},
		{name: "nonnull placeholder", operations: strings.Replace(singleUploadJSON, `null`, `"value"`, 1), mapping: `{"0":["variables.file"]}`, status: 400},
		{name: "batch", operations: `[` + singleUploadJSON + `]`, mapping: `{}`, status: 400},
		{name: "auth mutation", operations: `{"query":"mutation {logout}"}`, mapping: `{}`, status: 400},
		{name: "mixed roots", operations: strings.Replace(singleUploadJSON, `uploadFile(file:$file)`, `uploadFile(file:$file) logout`, 1), mapping: `{"0":["variables.file"]}`, status: 400},
		{name: "file count", operations: multipleUploadJSON, mapping: `{"0":["variables.files.0"],"1":["variables.files.1"]}`, modify: func(c *MultipartConfig) { c.MaxFiles = 1 }, status: 400},
		{name: "duplicate part", operations: singleUploadJSON, mapping: `{"0":["variables.file"]}`, files: []multipartFile{{"0", "a.txt", "ok"}, {"0", "b.txt", "ok"}}, status: 400},
		{name: "filename path", operations: singleUploadJSON, mapping: `{"0":["variables.file"]}`, files: []multipartFile{{"0", "../private", "ok"}}, status: 400},
		{name: "per file", operations: singleUploadJSON, mapping: `{"0":["variables.file"]}`, files: []multipartFile{{"0", "a.txt", "12345"}}, modify: func(c *MultipartConfig) { c.MaxFileBytes = 4 }, status: 413},
		{name: "whole request", operations: singleUploadJSON, mapping: `{"0":["variables.file"]}`, files: []multipartFile{{"0", "a.txt", strings.Repeat("x", 2000)}}, modify: func(c *MultipartConfig) { c.MaxRequestBytes = 1500; c.MaxFileBytes = 1500 }, status: 413},
		{name: "operations limit", operations: strings.Repeat(" ", int(MaxRequestBytes)+1), mapping: `{}`, status: 413},
		{name: "map limit", operations: singleUploadJSON, mapping: strings.Repeat(" ", int(maxUploadMapBytes)+1), status: 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := multipartHarness(t, tc.modify, func(context.Context) *graphql.Response { t.Fatal("rejected upload executed"); return nil })
			payload, ct := multipartPayload(t, tc.operations, tc.mapping, tc.files...)
			req := multipartRequest(bytes.NewReader(payload), ct)
			req.ContentLength = -1
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
			}
			noMultipartFiles(t, dir)
		})
	}
}

type observedBody struct {
	io.ReadCloser
	reads atomic.Int32
}

func (b *observedBody) Read(p []byte) (int, error) { b.reads.Add(1); return b.ReadCloser.Read(p) }

func TestMultipartRejectsBeforeBodyRead(t *testing.T) {
	for _, kind := range []string{"missing cookie", "wrong csrf", "wrong origin", "known oversize", "bootstrap header"} {
		t.Run(kind, func(t *testing.T) {
			h, dir := multipartHarness(t, nil, func(context.Context) *graphql.Response { t.Fatal("unexpected execution"); return nil })
			payload, ct := multipartPayload(t, singleUploadJSON, `{"0":["variables.file"]}`)
			req := multipartRequest(bytes.NewReader(payload), ct)
			body := &observedBody{ReadCloser: req.Body}
			req.Body = body
			switch kind {
			case "missing cookie":
				req.Header.Del("Cookie")
			case "wrong csrf":
				req.Header.Set("X-CSRF-Token", "bad")
			case "wrong origin":
				req.Header.Set("Origin", "https://evil.example.com")
			case "known oversize":
				req.ContentLength = 3 << 20
			case "bootstrap header":
				req.Header.Set("X-Vault-CSRF-Bootstrap", "1")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code < 400 || body.reads.Load() != 0 {
				t.Fatal("rejection read the body")
			}
			noMultipartFiles(t, dir)
		})
	}
}

func TestMultipartDeadlineClosesBlockedBody(t *testing.T) {
	h, dir := multipartHarness(t, func(c *MultipartConfig) { c.Timeout = 30 * time.Millisecond }, func(context.Context) *graphql.Response { t.Fatal("unexpected execution"); return nil })
	reader, writer := io.Pipe()
	defer writer.Close()
	req := multipartRequest(reader, "multipart/form-data; boundary=blocked")
	req.Body = reader
	done := make(chan struct{})
	w := httptest.NewRecorder()
	go func() { defer close(done); h.ServeHTTP(w, req) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = reader.Close()
		t.Fatal("deadline did not unblock body")
	}
	if w.Code != 408 {
		t.Fatalf("status=%d", w.Code)
	}
	noMultipartFiles(t, dir)
}

func TestMultipartCapacityHeldThroughExecution(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h, dir := multipartHarness(t, nil, func(context.Context) *graphql.Response {
		close(entered)
		<-release
		return &graphql.Response{Data: json.RawMessage(`{"ok":true}`)}
	})
	payload, ct := multipartPayload(t, singleUploadJSON, `{"0":["variables.file"]}`, multipartFile{"0", "f", "ok"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(), multipartRequest(bytes.NewReader(payload), ct))
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("first request not executing")
	}
	req := multipartRequest(bytes.NewReader(payload), ct)
	body := &observedBody{ReadCloser: req.Body}
	req.Body = body
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	close(release)
	<-done
	if w.Code != 503 || body.reads.Load() != 0 {
		t.Fatal("capacity check did not precede parsing")
	}
	noMultipartFiles(t, dir)
}

func TestMultipartAnonymousRejectedBeforeRead(t *testing.T) {
	h, dir := multipartHarness(t, nil, func(context.Context) *graphql.Response { t.Fatal("anonymous upload executed"); return nil }, auth.Session{CSRFToken: strings.Repeat("c", 43)})
	payload, ct := multipartPayload(t, singleUploadJSON, `{"0":["variables.file"]}`)
	req := multipartRequest(bytes.NewReader(payload), ct)
	body := &observedBody{ReadCloser: req.Body}
	req.Body = body
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 || body.reads.Load() != 0 {
		t.Fatal("anonymous request reached parsing")
	}
	noMultipartFiles(t, dir)
}

func TestMultipartTruncatedBodyAndExecutionPanicCleanup(t *testing.T) {
	for _, panicExecution := range []bool{false, true} {
		calls := 0
		h, dir := multipartHarness(t, nil, func(context.Context) *graphql.Response { calls++; panic("test execution failure") })
		payload, ct := multipartPayload(t, singleUploadJSON, `{"0":["variables.file"]}`, multipartFile{"0", "f", "data"})
		if !panicExecution {
			payload = payload[:len(payload)-30]
		}
		req := multipartRequest(bytes.NewReader(payload), ct)
		req.ContentLength = -1
		func() { defer func() { _ = recover() }(); h.ServeHTTP(httptest.NewRecorder(), req) }()
		if (calls == 1) != panicExecution {
			t.Fatal("unexpected execution count")
		}
		noMultipartFiles(t, dir)
	}
}

func TestMultipartAliasesFragmentsAndEpilogueLimit(t *testing.T) {
	operations := `{"query":"mutation M($file:Upload!){...F} fragment F on Mutation{alias:uploadFile(file:$file)}","operationName":"M","variables":{"file":null}}`
	calls := 0
	h, dir := multipartHarness(t, func(c *MultipartConfig) { c.MaxRequestBytes = 1500; c.MaxFileBytes = 100 }, func(context.Context) *graphql.Response {
		calls++
		return &graphql.Response{Data: json.RawMessage(`{"alias":true}`)}
	})
	payload, ct := multipartPayload(t, operations, `{"0":["variables.file"]}`, multipartFile{"0", "f", "data"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, multipartRequest(bytes.NewReader(payload), ct))
	if w.Code != 200 || calls != 1 {
		t.Fatalf("alias/fragment failed: %s", w.Body.String())
	}
	noMultipartFiles(t, dir)
	payload = append(payload, bytes.Repeat([]byte("x"), 2000)...)
	req := multipartRequest(bytes.NewReader(payload), ct)
	req.ContentLength = -1
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 413 || calls != 1 {
		t.Fatal("epilogue bypassed request limit")
	}
	noMultipartFiles(t, dir)
}

func TestMultipartRetryKeyPaths(t *testing.T) {
	transport := &MultipartTransport{schema: parsedSchema, config: MultipartConfig{MaxFiles: 10}}
	for _, test := range []struct {
		name, query string
		variables   map[string]any
		valid       bool
	}{
		{"key first", "mutation($f:Upload!,$k:ID!){uploadFile(idempotencyKey:$k,file:$f){id}}", map[string]any{"f": nil, "k": "12345678-1234-4234-9234-123456789abc"}, true},
		{"batch", "mutation($f:[Upload!]!,$k:ID!){uploadFiles(files:$f,idempotencyKey:$k){id}}", map[string]any{"f": []any{nil, nil}, "k": "12345678-1234-4234-9234-123456789abc"}, true},
		{"bad key", "mutation($f:Upload!,$k:ID!){uploadFile(file:$f,idempotencyKey:$k){id}}", map[string]any{"f": nil, "k": "invalid"}, false},
		{"extra variable", "mutation($f:Upload!){uploadFile(file:$f){id}}", map[string]any{"f": nil, "extra": "x"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := transport.uploadPaths(&graphql.RawParams{Query: test.query, Variables: test.variables})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}
