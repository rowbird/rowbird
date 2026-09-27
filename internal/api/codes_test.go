package api

import (
	"reflect"
	"testing"

	"github.com/rowbird/rowbird/internal/api/gen"

	"github.com/rowbird/rowbird/internal/ai"
	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/connections"
	"github.com/rowbird/rowbird/internal/delivery"
	"github.com/rowbird/rowbird/internal/gitops"
	"github.com/rowbird/rowbird/internal/i18n"
	"github.com/rowbird/rowbird/internal/links"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/reports"
	"github.com/rowbird/rowbird/internal/store"
)

// TestEveryErrorCodeHasATitle keeps problem titles translatable: each code an API response can
// carry must exist in the server catalogs.
func TestEveryErrorCodeHasATitle(t *testing.T) {
	codes := []string{
		CodeInternal, CodeRequestInvalid, CodeMethodNotAllowed, CodeRouteNotFound, CodeNotFound,
		CodeConflictVersion, CodeConflictDup, CodeValidation, CodeUnsupportedMedia, CodeUnauthenticated,
		CodeForbidden, CodeCSRFFailed, CodeRateLimited, "auth.locked",
		"auth." + string(auth.RestrictionPasswordChange), "auth." + string(auth.RestrictionMFASetup),
	}
	for _, e := range []*apperr.Error{
		auth.ErrUnauthenticated, auth.ErrInvalidCredentials, auth.ErrMFAInvalid, auth.ErrChallengeExpired,
		auth.ErrForbidden, auth.ErrSetupCompleted, auth.ErrSetupInvalidToken, auth.ErrLastAdmin,
		auth.ErrSelfModification, auth.ErrEmailTaken, auth.ErrTOTPAlreadyEnabled, auth.ErrTOTPNotEnabled,
		auth.ErrTOTPNotStarted, connections.ErrNameTaken, connections.ErrInUse, queries.ErrSlugTaken, queries.ErrInUse,
		reports.ErrSlugTaken, reports.ErrRunActive, reports.ErrRunNotActive, reports.ErrResultUnavailable, reports.ErrNoSystemMailer, delivery.ErrNotFailed, delivery.ErrNotResendable, channels.ErrNameTaken, channels.ErrInUse, links.ErrRevoked, links.ErrGone, links.ErrNotFound, links.ErrLoginRequired,
		ai.ErrNotConfigured, ai.ErrNoSchema, store.ErrManaged, gitops.ErrNotManaged, gitops.ErrNotDetached,
	} {
		codes = append(codes, e.Code)
	}
	codes = append(codes, []string{
		plugin.ErrCodeAuthFailed, plugin.ErrCodeHostUnreachable, plugin.ErrCodeTLSRequired, plugin.ErrCodeTLSFailed,
		plugin.ErrCodeDatabaseNotFound, plugin.ErrCodeTimeout, plugin.ErrCodeNetworkBlocked, plugin.ErrCodePathNotAllowed,
		plugin.ErrCodeSSHAuthFailed, plugin.ErrCodeSSHHostKeyUnknown, plugin.ErrCodeSSHHostKeyChanged,
		plugin.ErrCodeSSHUnreachable, plugin.ErrCodeFailed,
		plugin.ErrCodeQueryTimeout, plugin.ErrCodeQueryCancelled, plugin.ErrCodeMultiStatement,
		plugin.ErrCodeReadOnlyViolation, plugin.ErrCodeQueryFailed,
		CodeAITooManyRequests, plugin.ErrCodeAIAuth, plugin.ErrCodeAIRejected, plugin.ErrCodeAIUnreachable, plugin.ErrCodeAIRateLimited,
		plugin.ErrCodeAITimeout, plugin.ErrCodeAIBlocked, plugin.ErrCodeAIFailed, plugin.ErrCodeAIInvalidOutput,
	}...)
	for _, locale := range i18n.Default().Locales() {
		keys := i18n.Default().Keys(locale)
		for _, c := range codes {
			if _, ok := keys["errors."+c]; !ok {
				t.Errorf("%s catalog lacks errors.%s", locale, c)
			}
		}
	}
}

func TestPolicyTableCoversEveryOperation(t *testing.T) {
	table, err := loadPolicies()
	if err != nil {
		t.Fatal(err)
	}
	for op := range rateLimitedOperations {
		if p, ok := table[op]; !ok || !p.public {
			t.Errorf("rate limited operation %s is not a known public operation", op)
		}
	}
	methods := reflect.TypeFor[gen.StrictServerInterface]()
	for i := range methods.NumMethod() {
		if _, ok := table[methods.Method(i).Name]; !ok {
			t.Errorf("%s has no policy", methods.Method(i).Name)
		}
	}
	if len(table) != methods.NumMethod() {
		t.Errorf("%d policies for %d operations", len(table), methods.NumMethod())
	}
}
