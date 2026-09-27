package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/store/ids"
)

// expectedAccess is written by hand, independently of x-rowbird-authz in openapi.yaml, so that a
// change to either one without the other fails this test. Keys are operation ids in Go form.
var expectedAccess = map[string]accessRule{
	"GetHealthLive":              {public: true},
	"GetHealthReady":             {public: true},
	"GetSetupStatus":             {public: true},
	"RunSetup":                   {public: true},
	"Login":                      {public: true},
	"LoginSecondFactor":          {public: true},
	"BeginPasskeySecondFactor":   {public: true},
	"BeginPasskeyLogin":          {public: true},
	"PasskeyLogin":               {public: true},
	"OidcLogin":                  {public: true},
	"OidcCallback":               {public: true},
	"RequestPasswordReset":       {public: true},
	"ConfirmPasswordReset":       {public: true},
	"Logout":                     {role: "viewer", scope: "read", sessionOnly: true, allowRestricted: true},
	"GetMe":                      {role: "viewer", scope: "read", allowRestricted: true},
	"UpdateMe":                   {role: "viewer", scope: "write", sessionOnly: true, allowRestricted: true},
	"ChangeMyPassword":           {role: "viewer", scope: "write", sessionOnly: true, allowRestricted: true},
	"ListMySessions":             {role: "viewer", scope: "read", sessionOnly: true},
	"RevokeMySession":            {role: "viewer", scope: "write", sessionOnly: true},
	"RevokeAllMySessions":        {role: "viewer", scope: "write", sessionOnly: true},
	"BeginTotpSetup":             {role: "viewer", scope: "write", sessionOnly: true, allowRestricted: true},
	"ConfirmTotpSetup":           {role: "viewer", scope: "write", sessionOnly: true, allowRestricted: true},
	"DisableTotp":                {role: "viewer", scope: "write", sessionOnly: true},
	"RegenerateRecoveryCodes":    {role: "viewer", scope: "write", sessionOnly: true},
	"ListMyPasskeys":             {role: "viewer", scope: "read", sessionOnly: true, allowRestricted: true},
	"BeginMyPasskeyRegistration": {role: "viewer", scope: "write", sessionOnly: true, allowRestricted: true},
	"AddMyPasskey":               {role: "viewer", scope: "write", sessionOnly: true, allowRestricted: true},
	"RenameMyPasskey":            {role: "viewer", scope: "write", sessionOnly: true},
	"RemoveMyPasskey":            {role: "viewer", scope: "write", sessionOnly: true},
	"ListUsers":                  {role: "viewer", scope: "read"},
	"GetUser":                    {role: "viewer", scope: "read"},
	"CreateUser":                 {role: "admin", scope: "admin"},
	"UpdateUser":                 {role: "admin", scope: "admin"},
	"ResetUserPassword":          {role: "admin", scope: "admin"},
	"DisableUserTotp":            {role: "admin", scope: "admin"},
	"ListApiKeys":                {role: "admin", scope: "admin"},
	"CreateApiKey":               {role: "admin", scope: "admin"},
	"RevokeApiKey":               {role: "admin", scope: "admin"},
	"GetSettings":                {role: "viewer", scope: "read"},
	"UpdateSettings":             {role: "admin", scope: "admin"},
	"GetAlertSettings":           {role: "admin", scope: "admin"},
	"GetStorageStatus":           {role: "admin", scope: "admin"},
	"GetOIDCSettings":            {role: "admin", scope: "admin"},
	"UpdateOIDCSettings":         {role: "admin", scope: "admin"},
	"TestOIDCSettings":           {role: "admin", scope: "admin"},
	"GetAbout":                   {role: "viewer", scope: "read"},
	"GetEvents":                  {role: "viewer", scope: "read"},
	"ListNotifications":          {role: "viewer", scope: "read"},
	"MarkNotificationRead":       {role: "viewer", scope: "read"},
	"MarkAllNotificationsRead":   {role: "viewer", scope: "read"},
	"GetDashboard":               {role: "viewer", scope: "read"},
	"UpdateAlertSettings":        {role: "admin", scope: "admin"},
	"ListSecurityEvents":         {role: "admin", scope: "admin"},
	"ListPlugins":                {role: "viewer", scope: "read"},
	"ListConnections":            {role: "viewer", scope: "read"},
	"GetConnection":              {role: "viewer", scope: "read"},
	"GetConnectionSchema":        {role: "viewer", scope: "read"},
	"CreateConnection":           {role: "admin", scope: "admin"},
	"UpdateConnection":           {role: "admin", scope: "admin"},
	"DeleteConnection":           {role: "admin", scope: "admin"},
	"TestConnectionConfig":       {role: "admin", scope: "admin"},
	"TestConnection":             {role: "admin", scope: "admin"},
	"RefreshConnectionSchema":    {role: "editor", scope: "write"},
	"ListQueries":                {role: "viewer", scope: "read"},
	"GetQuery":                   {role: "viewer", scope: "read"},
	"ListQueryVersions":          {role: "viewer", scope: "read"},
	"GetQueryVersion":            {role: "viewer", scope: "read"},
	"CreateQuery":                {role: "editor", scope: "write"},
	"UpdateQuery":                {role: "editor", scope: "write"},
	"DeleteQuery":                {role: "editor", scope: "write"},
	"RestoreQueryVersion":        {role: "editor", scope: "write"},
	"PreviewQuery":               {role: "editor", scope: "run"},
	"ListReports":                {role: "viewer", scope: "read"},
	"GetReport":                  {role: "viewer", scope: "read"},
	"CreateReport":               {role: "editor", scope: "write"},
	"UpdateReport":               {role: "editor", scope: "write"},
	"DeleteReport":               {role: "editor", scope: "write"},
	"PauseReport":                {role: "editor", scope: "write"},
	"ResumeReport":               {role: "editor", scope: "write"},
	"RunReport":                  {role: "editor", scope: "run"},
	"PreviewSchedule":            {role: "viewer", scope: "read"},
	"ListRuns":                   {role: "viewer", scope: "read"},
	"GetRun":                     {role: "viewer", scope: "read"},
	"CancelRun":                  {role: "editor", scope: "run"},
	"DownloadRunResult":          {role: "viewer", scope: "read"},
	"ListChannels":               {role: "viewer", scope: "read"},
	"GetChannel":                 {role: "viewer", scope: "read"},
	"CreateChannel":              {role: "admin", scope: "admin"},
	"UpdateChannel":              {role: "admin", scope: "admin"},
	"DeleteChannel":              {role: "admin", scope: "admin"},
	"TestChannelConfig":          {role: "admin", scope: "admin"},
	"TestChannel":                {role: "editor", scope: "run"},
	"ListLinks":                  {role: "viewer", scope: "read"},
	"ListDeliveries":             {role: "viewer", scope: "read"},
	"CreateDelivery":             {role: "editor", scope: "write"},
	"UpdateDelivery":             {role: "editor", scope: "write"},
	"DeleteDelivery":             {role: "editor", scope: "write"},
	"PreviewDelivery":            {role: "editor", scope: "run"},
	"TestDelivery":               {role: "editor", scope: "run"},
	"RetryAttempt":               {role: "editor", scope: "run"},
	"ExportConfig":               {role: "editor", scope: "read"},
	"ImportConfig":               {role: "admin", scope: "admin"},
	"DetachFromGitOps":           {role: "admin", scope: "admin"},
	"AttachToGitOps":             {role: "admin", scope: "admin"},
	"GetAISettings":              {role: "admin", scope: "admin"},
	"UpdateAISettings":           {role: "admin", scope: "admin"},
	"TestAISettings":             {role: "admin", scope: "admin"},
	"GetAIStatus":                {role: "viewer", scope: "read"},
	"GenerateAIProposal":         {role: "editor", scope: "run"},
	"RetryFailedDeliveries":      {role: "editor", scope: "run"},
	"GetLink":                    {role: "viewer", scope: "read"},
	"RevokeLink":                 {role: "editor", scope: "write"},
}

type accessRule struct {
	public          bool
	role, scope     string
	sessionOnly     bool
	allowRestricted bool
}

// caller is one kind of principal in the matrix.
type caller struct {
	name        string
	role        string // "" for anonymous
	scope       string // API keys only
	restriction string // "password" or "mfa"
}

var callers = []caller{
	{name: "anonymous"},
	{name: "viewer", role: "viewer"},
	{name: "editor", role: "editor"},
	{name: "admin", role: "admin"},
	{name: "key:read", role: "admin", scope: "read"},
	{name: "key:run", role: "admin", scope: "run"},
	{name: "key:write", role: "admin", scope: "write"},
	{name: "key:admin", role: "admin", scope: "admin"},
	{name: "editor:must-change-password", role: "editor", restriction: "password"},
	{name: "admin:must-set-up-2fa", role: "admin", restriction: "mfa"},
}

func roleRank(r string) int  { return map[string]int{"viewer": 1, "editor": 2, "admin": 3}[r] }
func scopeRank(s string) int { return map[string]int{"read": 1, "run": 2, "write": 3, "admin": 4}[s] }

// expected returns the problem code the caller must get, or "" when access is granted.
func expected(r accessRule, c caller) string {
	switch {
	case r.public:
		return ""
	case c.role == "":
		return CodeUnauthenticated
	case c.scope != "" && r.sessionOnly:
		return CodeForbidden
	case c.restriction == "password" && !r.allowRestricted:
		return "auth.password_change_required"
	case c.restriction == "mfa" && !r.allowRestricted:
		return "auth.mfa_setup_required"
	case roleRank(c.role) < roleRank(r.role):
		return CodeForbidden
	case c.scope != "" && scopeRank(c.scope) < scopeRank(r.scope):
		return CodeForbidden
	}
	return ""
}

var denialCodes = map[string]bool{
	CodeUnauthenticated: true, CodeForbidden: true,
	"auth.password_change_required": true, "auth.mfa_setup_required": true,
}

// sessionClient signs in through the service, bypassing the per-IP login throttle, and returns a
// client holding the session cookies.
func (ts *testServer) sessionClient(email, password string) *client {
	ts.t.Helper()
	res, err := ts.svc.Login(ts.t.Context(), email, password, auth.RequestMeta{IP: "198.51.100.1"})
	if err != nil || res.Session == nil {
		ts.t.Fatalf("login %s: %v", email, err)
	}
	cl := ts.client()
	cl.cookies[SessionCookie] = &http.Cookie{Name: SessionCookie, Value: res.Session.Token}
	cl.cookies[CSRFCookie] = &http.Cookie{Name: CSRFCookie, Value: res.Session.CSRFToken}
	return cl
}

// TestPermissionMatrix calls every operation of the API as every kind of caller and checks that
// authorization grants or denies exactly as expectedAccess says (docs/spec/07-security.md).
func TestPermissionMatrix(t *testing.T) {
	// Server A: roles, API key scopes and a session that must change its password.
	a := newTestServer(t)
	admin := a.setup()
	passwords := map[string]string{"admin": testAdminPassword}
	emails := map[string]string{"admin": "admin@example.com"}
	for _, role := range []string{"viewer", "editor"} {
		email := role + "@example.com"
		_, temp := admin.createUser(email, role)
		a.activate(email, temp)
		emails[role], passwords[role] = email, "fresh passphrase 42"
	}
	_, temp := admin.createUser("restricted@example.com", "editor")
	emails["editor:must-change-password"], passwords["editor:must-change-password"] = "restricted@example.com", temp
	keys := map[string]string{}
	for _, sc := range []string{"read", "run", "write", "admin"} {
		keys[sc] = admin.createKey(sc)
	}

	// Server B: the workspace requires 2FA and this admin has not enrolled yet.
	b := newTestServer(t)
	bAdmin := b.setup()
	if res := bAdmin.do(http.MethodPatch, "/api/v1/settings", map[string]any{"require_2fa": true}); res.Code != http.StatusOK {
		t.Fatalf("require 2fa: %d %s", res.Code, res.Body)
	}

	// Every call gets a fresh session, so logout or revoke-all cannot affect later calls.
	newCaller := func(c caller) *client {
		switch {
		case c.role == "":
			return a.client()
		case c.scope != "":
			cl := a.client()
			cl.bearer = keys[c.scope]
			return cl
		case c.restriction == "mfa":
			return b.sessionClient("admin@example.com", testAdminPassword)
		}
		return a.sessionClient(emails[c.name], passwords[c.name])
	}

	doc, err := gen.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			ops = append(ops, op.OperationID+" "+method+" "+path)
		}
	}
	sort.Strings(ops)
	seen := map[string]bool{}
	checked := 0
	for _, entry := range ops {
		parts := strings.SplitN(entry, " ", 3)
		opID, method, path := parts[0], parts[1], parts[2]
		seen[opID] = true
		rule, ok := expectedAccess[opID]
		if !ok {
			t.Errorf("%s has no row in expectedAccess; decide its access and add it", opID)
			continue
		}
		url := path
		for _, param := range []string{"{userId}", "{sessionId}", "{apiKeyId}", "{connectionId}", "{queryId}", "{reportId}", "{runId}", "{channelId}", "{linkId}", "{deliveryId}", "{attemptId}", "{notificationId}", "{passkeyId}"} {
			url = strings.ReplaceAll(url, param, ids.New().String())
		}
		url = strings.ReplaceAll(url, "{number}", "1")
		for _, c := range callers {
			var body any
			if method != http.MethodGet {
				body = map[string]any{}
			}
			var opts []func(*http.Request)
			if opID == "GetEvents" {
				// The stream stays open; end it soon after the authorization answered.
				opts = append(opts, func(r *http.Request) {
					ctx, cancel := context.WithTimeout(r.Context(), 20*time.Millisecond)
					t.Cleanup(cancel)
					*r = *r.WithContext(ctx)
				})
			}
			res := newCaller(c).do(method, url, body, opts...)
			got := ""
			if res.Code == http.StatusUnauthorized || res.Code == http.StatusForbidden {
				if p := res.problem(t); denialCodes[p.Code] {
					got = p.Code
				}
			}
			if want := expected(rule, c); got != want {
				t.Errorf("%s %s as %s: got %q (HTTP %d), want %q", method, path, c.name, got, res.Code, want)
			}
			checked++
		}
	}
	for opID := range expectedAccess {
		if !seen[opID] {
			t.Errorf("expectedAccess lists %s, which is not in the API", opID)
		}
	}
	t.Logf("permission matrix: %d operations x %d callers = %d checks", len(ops), len(callers), checked)
}
