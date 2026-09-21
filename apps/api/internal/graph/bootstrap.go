package graph

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"full-stack-file-vault.local/api/internal/auth"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

// NewBrowserHandler is the production composition; bootstrap never relies on the
// client-supplied operation name alone to decide what may execute.
func NewBrowserHandler(logger *slog.Logger, browser *auth.BrowserSecurity, begin auth.BootstrapFunc) http.Handler {
	return browser.WrapBootstrap(NewHandler(logger), validateBootstrapRequest, begin)
}

func NewAuthenticationHandler(logger *slog.Logger, browser *auth.BrowserSecurity, begin auth.BootstrapFunc, login auth.LoginFunc, logout auth.LogoutFunc) http.Handler {
	return browser.WrapAuthentication(NewHandler(logger), validateBootstrapRequest, begin, login, logout)
}

func validateBootstrapRequest(r *http.Request) error {
	invalid := errors.New("invalid bootstrap operation")
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return invalid
	}
	original := r.Body
	if original == nil {
		return invalid
	}
	payload, err := io.ReadAll(io.LimitReader(original, MaxRequestBytes+1))
	_ = original.Close()
	if err != nil || int64(len(payload)) > MaxRequestBytes {
		return invalid
	}
	r.Body = io.NopCloser(bytes.NewReader(payload))
	var input struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	if err = json.Unmarshal(payload, &input); err != nil {
		return invalid
	}
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: input.Query}, 4096)
	if err != nil {
		return invalid
	}
	if issues := validator.Validate(parsedSchema, doc); len(issues) > 0 {
		return invalid
	}
	var selected *ast.OperationDefinition
	if input.OperationName == "" {
		if len(doc.Operations) != 1 {
			return invalid
		}
		selected = doc.Operations[0]
	} else {
		selected = doc.Operations.ForName(input.OperationName)
	}
	if !singleAuthRoot(doc, selected, "beginSession") {
		return invalid
	}
	return nil
}

// singleAuthRoot rejects ambiguous identity changes before any resolver executes.
// An empty expected name allows any implemented authentication mutation.
func singleAuthRoot(doc *ast.QueryDocument, selected *ast.OperationDefinition, expected string) bool {
	if selected == nil || selected.Operation != ast.Mutation || len(selected.Directives) > 0 {
		return false
	}
	roots := 0
	budget := 4096
	active := map[string]bool{}
	var walk func(ast.SelectionSet) bool
	walk = func(set ast.SelectionSet) bool {
		for _, selection := range set {
			budget--
			if budget < 0 {
				return false
			}
			switch node := selection.(type) {
			case *ast.Field:
				roots++
				if roots > 1 || (expected != "" && node.Name != expected) || (node.Name != "beginSession" && node.Name != "login" && node.Name != "logout") || len(node.Directives) > 0 {
					return false
				}
			case *ast.InlineFragment:
				if len(node.Directives) > 0 || !walk(node.SelectionSet) {
					return false
				}
			case *ast.FragmentSpread:
				f := doc.Fragments.ForName(node.Name)
				if f == nil || active[node.Name] || len(node.Directives) > 0 || len(f.Directives) > 0 {
					return false
				}
				active[node.Name] = true
				if !walk(f.SelectionSet) {
					return false
				}
				delete(active, node.Name)
			default:
				return false
			}
		}
		return true
	}
	return walk(selected.SelectionSet) && roots == 1
}

// Authentication and file mutations may not be mixed or repeated through aliases.
func singleMutationRoot(doc *ast.QueryDocument, op *ast.OperationDefinition) bool {
	if singleAuthRoot(doc, op, "") {
		return true
	}
	if op == nil || op.Operation != ast.Mutation || len(op.Directives) > 0 {
		return false
	}
	field := singleUploadRoot(doc, op.SelectionSet)
	return field != nil && (field.Name == "uploadFile" || field.Name == "uploadFiles" || field.Name == "createFileAccess" || field.Name == "deleteFile" || field.Name == "setFileTags" || field.Name == "createShare" || field.Name == "revokeShare" || field.Name == "createSharedAccess" || field.Name == "adminSetQuota" || field.Name == "adminSetUserDisabled")
}
