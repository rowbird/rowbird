package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store"
)

// policy is the authorization rule of one operation, read from x-rowbird-authz in the embedded
// OpenAPI document (ADR-0017). oapi-codegen embeds the document with operation ids already in Go
// form ("login" becomes "Login"), which is also the name it passes to strict middleware.
type policy struct {
	public          bool
	role            store.Role
	scope           auth.Scope
	sessionOnly     bool
	allowRestricted bool
}

var (
	policiesOnce sync.Once
	policies     map[string]policy
	policiesErr  error
)

// loadPolicies builds the table once. An operation that is neither public nor declares a valid
// x-rowbird-authz is an error, so a new endpoint cannot ship without an explicit rule.
func loadPolicies() (map[string]policy, error) {
	policiesOnce.Do(func() {
		doc, err := gen.GetSpec()
		if err != nil {
			policiesErr = fmt.Errorf("api: load embedded OpenAPI document: %w", err)
			return
		}
		out := map[string]policy{}
		for path, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				if op.Security != nil && len(*op.Security) == 0 {
					out[op.OperationID] = policy{public: true}
					continue
				}
				raw, ok := op.Extensions["x-rowbird-authz"].(map[string]any)
				if !ok {
					policiesErr = fmt.Errorf("api: %s %s has no x-rowbird-authz", method, path)
					return
				}
				p := policy{role: store.Role(fmt.Sprint(raw["role"])), scope: auth.Scope(fmt.Sprint(raw["scope"]))}
				p.sessionOnly, _ = raw["session_only"].(bool)
				p.allowRestricted, _ = raw["allow_restricted"].(bool)
				if auth.RoleRank(p.role) == 0 || p.scope.Rank() == 0 {
					policiesErr = fmt.Errorf("api: %s %s has an invalid x-rowbird-authz", method, path)
					return
				}
				out[op.OperationID] = p
			}
		}
		policies = out
	})
	return policies, policiesErr
}

// rateLimitedOperations are throttled per client IP before they run. Keys are operation names as
// the generated code passes them (the operationId in Go form, for example "Login").
var rateLimitedOperations = map[string]bool{
	"Login": true, "LoginSecondFactor": true, "RunSetup": true,
	"BeginPasskeyLogin": true, "PasskeyLogin": true, "BeginPasskeySecondFactor": true,
	"RequestPasswordReset": true, "ConfirmPasswordReset": true,
}

// authorize enforces the policy of each operation. Anything without a policy is denied.
func authorize(table map[string]policy, limiter *rateLimiter) gen.StrictMiddlewareFunc {
	return func(next gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		pol, known := table[operationID]
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if rateLimitedOperations[operationID] && limiter != nil {
				if ok, wait := limiter.allow(ClientIPFrom(ctx)); !ok {
					return nil, &Error{Status: http.StatusTooManyRequests, Code: CodeRateLimited, RetryAfter: wait}
				}
			}
			if !known {
				return nil, &Error{Status: http.StatusForbidden, Code: CodeForbidden}
			}
			if pol.public {
				return next(ctx, w, r, request)
			}
			p := PrincipalFrom(ctx)
			switch {
			case p == nil:
				return nil, &Error{Status: http.StatusUnauthorized, Code: CodeUnauthenticated}
			case p.IsAPIKey() && pol.sessionOnly:
				return nil, &Error{Status: http.StatusForbidden, Code: CodeForbidden}
			case p.Restriction != auth.RestrictionNone && !pol.allowRestricted:
				return nil, &Error{Status: http.StatusForbidden, Code: "auth." + string(p.Restriction)}
			case auth.RoleRank(p.Role) < auth.RoleRank(pol.role) || !p.HasScope(pol.scope):
				return nil, &Error{Status: http.StatusForbidden, Code: CodeForbidden}
			}
			return next(p.Context(ctx), w, r, request)
		}
	}
}
