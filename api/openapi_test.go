// Package apispec holds tests for the OpenAPI document. The document itself is api/openapi.yaml.
package apispec

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// publicOperations are the only operations reachable without authentication.
var publicOperations = map[string]bool{
	"getHealthLive":     true,
	"getHealthReady":    true,
	"getSetupStatus":    true,
	"runSetup":          true,
	"login":             true,
	"loginSecondFactor": true,
	// Passkey sign-in, and the passkey step of a password sign-in.
	"beginPasskeyLogin":        true,
	"passkeyLogin":             true,
	"beginPasskeySecondFactor": true,
	// Single sign-on: the browser leaves for the provider and comes back.
	"oidcLogin":    true,
	"oidcCallback": true,
	// "Forgot password".
	"requestPasswordReset": true,
	"confirmPasswordReset": true,
}

func checkAuthz(t *testing.T, where string, raw any) {
	t.Helper()
	m, ok := raw.(map[string]any)
	if !ok {
		t.Errorf("%s: x-rowbird-authz must be an object", where)
		return
	}
	roles := map[any]bool{"viewer": true, "editor": true, "admin": true}
	scopes := map[any]bool{"read": true, "run": true, "write": true, "admin": true}
	if !roles[m["role"]] {
		t.Errorf("%s: x-rowbird-authz.role %v is not a role", where, m["role"])
	}
	if !scopes[m["scope"]] {
		t.Errorf("%s: x-rowbird-authz.scope %v is not a scope", where, m["scope"])
	}
	for k, v := range m {
		switch k {
		case "role", "scope":
		case "session_only", "allow_restricted":
			if _, ok := v.(bool); !ok {
				t.Errorf("%s: x-rowbird-authz.%s must be a boolean", where, k)
			}
		default:
			t.Errorf("%s: unknown x-rowbird-authz key %q", where, k)
		}
	}
}

func load(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("openapi.yaml is invalid: %v", err)
	}
	return doc
}

func TestDocumentIsValid(t *testing.T) {
	load(t)
}

// TestOperationConventions checks rules from docs/spec/05-api.md that a schema validator cannot.
func TestOperationConventions(t *testing.T) {
	doc := load(t)
	if len(doc.Security) == 0 {
		t.Error("the document must require authentication by default (top-level security)")
	}
	seen := map[string]string{}
	for path, item := range doc.Paths.Map() {
		if !strings.HasPrefix(path, "/api/v1/") && !strings.HasPrefix(path, "/health/") {
			t.Errorf("%s: paths must live under /api/v1 or /health", path)
		}
		for method, op := range item.Operations() {
			where := method + " " + path
			if op.OperationID == "" {
				t.Errorf("%s: missing operationId", where)
			} else if prev, dup := seen[op.OperationID]; dup {
				t.Errorf("%s: operationId %s already used by %s", where, op.OperationID, prev)
			}
			seen[op.OperationID] = where
			if len(op.Tags) == 0 {
				t.Errorf("%s: missing tag", where)
			}
			public := op.Security != nil && len(*op.Security) == 0
			if public != publicOperations[op.OperationID] {
				t.Errorf("%s: public=%v, but publicOperations says %v; change both deliberately", where, public, publicOperations[op.OperationID])
			}
			authz, hasAuthz := op.Extensions["x-rowbird-authz"]
			switch {
			case public && hasAuthz:
				t.Errorf("%s: public operations must not declare x-rowbird-authz", where)
			case !public && !hasAuthz:
				t.Errorf("%s: missing x-rowbird-authz (deny by default)", where)
			case !public:
				checkAuthz(t, where, authz)
			}
			for code, resp := range op.Responses.Map() {
				if code[0] == '4' || code[0] == '5' {
					if resp.Value != nil && resp.Value.Content.Get("application/problem+json") == nil && path != "/health/ready" {
						t.Errorf("%s: error response %s must use application/problem+json", where, code)
					}
				}
			}
		}
	}
}
