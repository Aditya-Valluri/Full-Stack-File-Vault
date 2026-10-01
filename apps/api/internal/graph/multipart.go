package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"full-stack-file-vault.local/api/internal/auth"
	"full-stack-file-vault.local/api/internal/files"
	"full-stack-file-vault.local/api/internal/upload"
	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

const maxUploadMapBytes int64 = 16 << 10

// MultipartConfig has no implicit limits. Directory must already exist with
// service-only permissions. Capacity is per transport instance, not a user rate
// limiter or a cross-replica disk reservation. File bytes are always staged on disk.
type MultipartConfig struct {
	Directory             string
	MaxRequestBytes       int64
	MaxFileBytes          int64
	MaxFiles              int
	MaxConcurrentRequests int
	Timeout               time.Duration
}

// MultipartTransport implements the strict single-operation subset of the GraphQL
// multipart protocol used by uploadFile(file: Upload!) and uploadFiles(files: [Upload!]!).
// NewApplicationHandler registers it behind browser authentication and shared admission.
type MultipartTransport struct {
	config MultipartConfig
	schema *ast.Schema
	logger *slog.Logger
	slots  chan struct{}
}

var _ graphql.Transport = (*MultipartTransport)(nil)

func NewMultipartTransport(c MultipartConfig, schema *ast.Schema, logger *slog.Logger) (*MultipartTransport, error) {
	if c.Directory == "" || c.MaxFileBytes < 1 || c.MaxRequestBytes < c.MaxFileBytes || c.MaxRequestBytes == math.MaxInt64 || c.MaxFiles < 1 || c.MaxFiles > 100 || c.MaxConcurrentRequests < 1 || c.MaxConcurrentRequests > 128 || c.Timeout <= 0 || c.Timeout > 5*time.Minute || schema == nil || logger == nil {
		return nil, errors.New("invalid multipart transport configuration")
	}
	return &MultipartTransport{config: c, schema: schema, logger: logger, slots: make(chan struct{}, c.MaxConcurrentRequests)}, nil
}

func (t *MultipartTransport) Supports(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "multipart/form-data" && r.Method == http.MethodPost && r.Header.Get("Upgrade") == ""
}

func (t *MultipartTransport) Do(w http.ResponseWriter, r *http.Request, exec graphql.GraphExecutor) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json")
	if !t.Supports(r) {
		reject(w, 415, codeUnsupportedMedia, "use multipart/form-data POST")
		return
	}
	// Browser middleware must validate Origin and CSRF before attaching this identity.
	// Anonymous and bootstrap contexts never qualify, even if a caller forges headers.
	if _, err := auth.RequireUser(r.Context()); err != nil {
		reject(w, 401, "UNAUTHENTICATED", "authentication required")
		return
	}
	if r.ContentLength > t.config.MaxRequestBytes {
		reject(w, 413, codeRequestTooLarge, "request too large")
		return
	}
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	default:
		reject(w, 503, "UPLOAD_BUSY", "upload capacity unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), t.config.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		reject(w, 408, "REQUEST_TIMEOUT", "upload interrupted")
		return
	}
	if r.Body == nil {
		reject(w, 400, codeInvalidInput, "invalid multipart request")
		return
	}
	original := r.Body
	// Closing the transport-owned body interrupts a blocked HTTP body read when the
	// request is cancelled. Stage itself only checks cancellation between reads.
	stop := context.AfterFunc(ctx, func() { _ = original.Close() })
	defer stop()
	defer original.Close()
	r = r.Clone(ctx)
	bounded := &multipartBody{ReadCloser: http.MaxBytesReader(w, original, t.config.MaxRequestBytes)}
	r.Body = bounded
	mr, err := r.MultipartReader()
	if err != nil {
		reject(w, 400, codeInvalidInput, "invalid multipart request")
		return
	}
	var files []*upload.Staged
	defer func() {
		for _, file := range files {
			if err := file.Close(); err != nil {
				t.logger.Error("upload temporary cleanup failed", "code", "UPLOAD_CLEANUP_FAILED")
			}
		}
	}()
	start := graphql.Now()
	var params graphql.RawParams
	if err = readMultipartJSON(mr, "operations", MaxRequestBytes, &params); err != nil {
		t.rejectRead(w, ctx, err)
		return
	}
	expected, err := t.uploadPaths(&params)
	if err != nil {
		reject(w, 400, codeInvalidInput, "invalid upload operation")
		return
	}
	var mapping map[string][]string
	if err = readMultipartJSON(mr, "map", maxUploadMapBytes, &mapping); err != nil {
		t.rejectRead(w, ctx, err)
		return
	}
	if len(mapping) != len(expected) {
		reject(w, 400, codeInvalidInput, "invalid upload map")
		return
	}
	seen := map[string]bool{}
	for key, paths := range mapping {
		if key == "" || len(key) > 64 || len(paths) != 1 || !expected[paths[0]] || seen[paths[0]] {
			reject(w, 400, codeInvalidInput, "invalid upload map")
			return
		}
		seen[paths[0]] = true
	}
	for len(mapping) > 0 {
		part, e := mr.NextRawPart()
		if e != nil {
			t.rejectRead(w, ctx, e)
			return
		}
		_, disposition, e := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		paths, ok := mapping[part.FormName()]
		if e != nil || !ok || disposition["filename"] == "" || part.Header.Get("Content-Transfer-Encoding") != "" {
			reject(w, 400, codeInvalidInput, "invalid upload part")
			return
		}
		// Use the original filename parameter. Part.FileName silently strips path
		// components on some platforms, obscuring invalid client metadata.
		staged, e := upload.Stage(ctx, t.config.Directory, part, upload.Metadata{Name: disposition["filename"], DeclaredMIME: part.Header.Get("Content-Type")}, t.config.MaxFileBytes)
		if e != nil {
			if bounded.exceeded {
				e = errors.Join(e, &http.MaxBytesError{Limit: t.config.MaxRequestBytes})
			}
			t.rejectRead(w, ctx, e)
			return
		}
		files = append(files, staged)
		info := staged.Info()
		value := graphql.Upload{File: staged, Size: info.SizeBytes, Filename: info.Name, ContentType: info.DeclaredMIME}
		if e := params.AddUpload(value, part.FormName(), paths[0]); e != nil {
			reject(w, 400, codeInvalidInput, "invalid upload map")
			return
		}
		delete(mapping, part.FormName())
	}
	// Extra parts, including unmapped and repeated file keys, are always rejected.
	if _, err = mr.NextRawPart(); err != io.EOF {
		if err == nil {
			err = upload.ErrInvalidInput
		}
		t.rejectRead(w, ctx, err)
		return
	}
	// Account for epilogue bytes as well; Content-Length is never the size authority.
	if _, err = io.Copy(io.Discard, r.Body); err != nil {
		t.rejectRead(w, ctx, err)
		return
	}
	if ctx.Err() != nil {
		t.rejectRead(w, ctx, ctx.Err())
		return
	}
	params.Headers = r.Header
	params.ReadTime = graphql.TraceTiming{Start: start, End: graphql.Now()}
	operation, issues := exec.CreateOperationContext(ctx, &params)
	if len(issues) > 0 {
		reject(w, 422, "GRAPHQL_VALIDATION_FAILED", "invalid upload operation")
		return
	}
	responses, responseCtx := exec.DispatchOperation(ctx, operation)
	// The executor owns safe resolver-error presentation. Uploaded handles live only
	// through synchronous execution and must never be captured by a background job.
	if err = json.NewEncoder(w).Encode(responses(responseCtx)); err != nil {
		t.logger.Debug("upload response write failed")
	}
}

func readMultipartJSON(mr *multipart.Reader, name string, limit int64, target any) error {
	part, err := mr.NextRawPart()
	if err != nil {
		return err
	}
	if part.FormName() != name || part.FileName() != "" || part.Header.Get("Content-Transfer-Encoding") != "" {
		return upload.ErrInvalidInput
	}
	body, err := io.ReadAll(io.LimitReader(part, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return upload.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return upload.ErrInvalidInput
	}
	if decoder.Decode(new(any)) != io.EOF {
		return upload.ErrInvalidInput
	}
	return nil
}

func (t *MultipartTransport) rejectRead(w http.ResponseWriter, ctx context.Context, err error) {
	if errors.Is(err, upload.ErrCleanup) {
		t.logger.Error("upload temporary cleanup failed", "code", "UPLOAD_CLEANUP_FAILED")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case ctx.Err() != nil:
		reject(w, 408, "REQUEST_TIMEOUT", "upload interrupted")
	case errors.As(err, &tooLarge), errors.Is(err, upload.ErrTooLarge):
		reject(w, 413, "UPLOAD_TOO_LARGE", "upload exceeds size limit")
	case errors.Is(err, upload.ErrStorage), errors.Is(err, upload.ErrCleanup):
		t.logger.Error("upload staging failed", "code", codeInternal)
		reject(w, 503, codeInternal, "upload storage unavailable")
	default:
		reject(w, 400, codeInvalidInput, "invalid multipart request")
	}
}

// uploadPaths validates the selected operation before accepting file parts. Each
// null Upload placeholder maps to exactly one physical part, with no path fan-out.
func (t *MultipartTransport) uploadPaths(params *graphql.RawParams) (map[string]bool, error) {
	invalid := upload.ErrInvalidInput
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: params.Query}, 4096)
	if err != nil || len(doc.Operations) != 1 || len(params.Extensions) != 0 {
		return nil, invalid
	}
	if len(validator.Validate(t.schema, doc)) > 0 {
		return nil, invalid
	}
	op := doc.Operations[0]
	if op.Operation != ast.Mutation || len(op.Directives) > 0 || (params.OperationName != "" && params.OperationName != op.Name) {
		return nil, invalid
	}
	field := singleUploadRoot(doc, op.SelectionSet)
	if field == nil {
		return nil, invalid
	}
	argName := "file"
	if field.Name == "uploadFiles" {
		argName = "files"
	}
	arg := field.Arguments.ForName(argName)
	if arg == nil || arg.Value.Kind != ast.Variable {
		return nil, invalid
	}
	expectedVariables := 1
	if key := field.Arguments.ForName("idempotencyKey"); key != nil {
		expectedVariables++
		if key.Value.Kind != ast.Variable || key.Value.Raw == arg.Value.Raw {
			return nil, invalid
		}
		value, exists := params.Variables[key.Value.Raw]
		if !exists {
			return nil, invalid
		}
		if value != nil {
			text, ok := value.(string)
			if !ok || !upload.ValidRetryKey(text) {
				return nil, invalid
			}
		}
	}
	if tags := field.Arguments.ForName("tags"); tags != nil {
		expectedVariables++
		if tags.Value.Kind != ast.Variable || tags.Value.Raw == arg.Value.Raw {
			return nil, invalid
		}
		value, exists := params.Variables[tags.Value.Raw]
		if !exists {
			return nil, invalid
		}
		if value != nil {
			values, ok := value.([]any)
			if !ok || len(values) > 20 {
				return nil, invalid
			}
			input := make([]string, len(values))
			for i, v := range values {
				text, ok := v.(string)
				if !ok {
					return nil, invalid
				}
				input[i] = text
			}
			if _, err := files.NormalizeTags(input); err != nil {
				return nil, invalid
			}
		}
	}
	if len(field.Arguments) != expectedVariables || len(params.Variables) != expectedVariables {
		return nil, invalid
	}
	prefix := "variables." + arg.Value.Raw
	value, exists := params.Variables[arg.Value.Raw]
	if !exists {
		return nil, invalid
	}
	switch field.Name {
	case "uploadFile":
		if arg.Name != "file" || value != nil {
			return nil, invalid
		}
		return map[string]bool{prefix: true}, nil
	case "uploadFiles":
		values, ok := value.([]any)
		if arg.Name != "files" || !ok || len(values) < 1 || len(values) > t.config.MaxFiles {
			return nil, invalid
		}
		paths := make(map[string]bool, len(values))
		for i, v := range values {
			if v != nil {
				return nil, invalid
			}
			paths[prefix+"."+strconv.Itoa(i)] = true
		}
		return paths, nil
	default:
		return nil, invalid
	}
}

type multipartBody struct {
	io.ReadCloser
	exceeded bool
}

func (b *multipartBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		b.exceeded = true
	}
	return n, err
}

func singleUploadRoot(doc *ast.QueryDocument, set ast.SelectionSet) *ast.Field {
	var root *ast.Field
	budget := 4096
	var walk func(ast.SelectionSet) bool
	walk = func(set ast.SelectionSet) bool {
		for _, selection := range set {
			budget--
			if budget < 0 {
				return false
			}
			switch node := selection.(type) {
			case *ast.Field:
				if root != nil || len(node.Directives) > 0 {
					return false
				}
				root = node
			case *ast.InlineFragment:
				if len(node.Directives) > 0 || !walk(node.SelectionSet) {
					return false
				}
			case *ast.FragmentSpread:
				f := doc.Fragments.ForName(node.Name)
				if f == nil || len(node.Directives) > 0 || len(f.Directives) > 0 || !walk(f.SelectionSet) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	// Schema validation above rejects cyclic fragment references.
	if !walk(set) {
		return nil
	}
	return root
}
