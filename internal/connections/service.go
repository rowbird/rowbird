// Package connections manages user database connections: configuration validated against the
// connector's schema, secrets encrypted at rest, connection tests, write-permission checks and the
// schema cache (docs/spec/03-flows.md, section 2).
package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/logging"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/ids"
)

// Limits and defaults from docs/spec/02-data-model.md.
const (
	DefaultQueryTimeoutSeconds = 60
	DefaultMaxRows             = 100000
	maxQueryTimeoutSeconds     = 3600
	maxMaxRows                 = 10_000_000
	maxExcludedTables          = 1000
	checkTimeout               = 30 * time.Second
)

// Security event types.
const (
	EventCreated = "connection_created"
	EventUpdated = "connection_updated"
	EventDeleted = "connection_deleted"
)

// Errors.
var (
	ErrNameTaken       = apperr.New(apperr.KindConflict, "connection.name_taken")
	ErrInUse           = apperr.New(apperr.KindConflict, "connection.in_use")
	ErrUnknownDriver   = apperr.Invalid(apperr.Field("driver", "validation.invalid_value"))
	errDriverImmutable = apperr.Invalid(apperr.Field("driver", "validation.immutable"))
)

// Service manages connections.
type Service struct {
	store      *store.Store
	keyring    *crypto.Keyring
	secrets    *secretconfig.Codec
	dial       plugin.DialFunc
	sqliteDirs []string
	forbidden  []string
	logger     *slog.Logger
	now        func() time.Time
}

// Options configure the service.
type Options struct {
	Dial       plugin.DialFunc
	SQLiteDirs []string
	// ForbiddenPaths may never be opened by file connectors (the internal store).
	ForbiddenPaths []string
	Logger         *slog.Logger
	Now            func() time.Time
}

// NewService builds the service.
func NewService(st *store.Store, kr *crypto.Keyring, opts Options) *Service {
	s := &Service{store: st, keyring: kr, secrets: secretconfig.New(kr, "connection"), dial: opts.Dial, sqliteDirs: opts.SQLiteDirs, forbidden: opts.ForbiddenPaths, logger: opts.Logger, now: opts.Now}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// View is a connection as the API shows it: configuration without secret values, and which
// secrets are set.
type View struct {
	store.Connection
	SecretsConfigured map[string]bool
}

// Input creates a connection. Config holds every field of the connector's schema, secrets as
// plain strings.
type Input struct {
	Name                string
	Driver              string
	Config              map[string]any
	QueryTimeoutSeconds *int
	MaxRows             *int
	AllowMultiStatement *bool
	AIExcludedTables    []string
}

// Patch changes a connection. Config, when not nil, replaces the whole configuration; secret
// fields follow the placeholder rules (absent or placeholder keeps, null clears, a string replaces).
type Patch struct {
	Version             int64
	Name                *string
	Config              map[string]any
	QueryTimeoutSeconds *int
	MaxRows             *int
	AllowMultiStatement *bool
	AIExcludedTables    *[]string
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func connector(driver string) (plugin.Connector, bool) {
	p, ok := plugin.Get(plugin.KindConnector, driver)
	if !ok {
		return nil, false
	}
	c, ok := p.(plugin.Connector)
	return c, ok
}

// List returns the workspace's connections.
func (s *Service) List(ctx context.Context) ([]View, error) {
	cs, err := s.store.Connections().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, len(cs))
	for i, c := range cs {
		out[i] = s.view(&c)
	}
	return out, nil
}

// Get returns one connection.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*View, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := s.view(c)
	return &v, nil
}

func (s *Service) view(c *store.Connection) View {
	v := View{Connection: *c, SecretsConfigured: map[string]bool{}}
	v.SecretsEnc = nil
	v.SchemaCache = nil
	if conn, ok := connector(c.Driver); ok {
		secrets, err := s.decrypt(c)
		for _, k := range conn.ConfigSchema().SecretKeys() {
			_, set := secrets[k]
			v.SecretsConfigured[k] = err == nil && set
		}
	}
	return v
}

// Create validates and stores a connection, then checks it (best effort: a failing database does
// not prevent saving, it is recorded as the connection's status).
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input, meta auth.RequestMeta) (*View, error) {
	conn, ok := connector(in.Driver)
	if !ok {
		return nil, ErrUnknownDriver
	}
	fe := checkName(in.Name)
	values := secretconfig.Resolve(conn.ConfigSchema(), in.Config, nil)
	validated, verr := conn.ConfigSchema().Validate(values)
	c := &store.Connection{
		Name: strings.TrimSpace(in.Name), Driver: in.Driver,
		QueryTimeoutSeconds: DefaultQueryTimeoutSeconds, MaxRows: DefaultMaxRows,
	}
	fe = append(fe, applyLimits(c, in.QueryTimeoutSeconds, in.MaxRows, in.AllowMultiStatement, &in.AIExcludedTables)...)
	if err := joinErrors(fe, verr); err != nil {
		return nil, err
	}
	c.ID = ids.New()
	c.CreatedBy = &p.UserID
	if err := s.setValues(c, conn, validated); err != nil {
		return nil, err
	}
	err := s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Connections().Create(ctx, c); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrNameTaken
			}
			return err
		}
		return s.record(ctx, p, EventCreated, meta, c)
	})
	if err != nil {
		return nil, err
	}
	if !checksDeferred(ctx) {
		s.checkAndStore(ctx, c)
	}
	return s.Get(ctx, c.ID)
}

// Update applies a patch with optimistic concurrency and re-checks the connection when its
// configuration changed.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, patch Patch, meta auth.RequestMeta) (*View, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := store.CheckManaged(ctx, c.ManagedBy); err != nil {
		return nil, err
	}
	conn, ok := connector(c.Driver)
	if !ok {
		return nil, ErrUnknownDriver
	}
	c.Version = patch.Version
	var fe []apperr.FieldError
	cols := []string{}
	if patch.Name != nil {
		fe = append(fe, checkName(*patch.Name)...)
		c.Name = strings.TrimSpace(*patch.Name)
		cols = append(cols, "name")
	}
	var validated map[string]any
	var verr error
	if patch.Config != nil {
		if d, ok := patch.Config["driver"]; ok && d != c.Driver {
			return nil, errDriverImmutable
		}
		existing, err := s.decrypt(c)
		if err != nil {
			return nil, err
		}
		values := secretconfig.Resolve(conn.ConfigSchema(), patch.Config, existing)
		validated, verr = conn.ConfigSchema().Validate(values)
		cols = append(cols, "config", "secrets_enc")
	}
	fe = append(fe, applyLimits(c, patch.QueryTimeoutSeconds, patch.MaxRows, patch.AllowMultiStatement, patch.AIExcludedTables)...)
	if patch.QueryTimeoutSeconds != nil {
		cols = append(cols, "query_timeout_seconds")
	}
	if patch.MaxRows != nil {
		cols = append(cols, "max_rows")
	}
	if patch.AllowMultiStatement != nil {
		cols = append(cols, "allow_multi_statement")
	}
	if patch.AIExcludedTables != nil {
		cols = append(cols, "ai_excluded_tables")
	}
	if err := joinErrors(fe, verr); err != nil {
		return nil, err
	}
	if validated != nil {
		if err := s.setValues(c, conn, validated); err != nil {
			return nil, err
		}
	}
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Connections().Update(ctx, c, cols...); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrNameTaken
			}
			return err
		}
		return s.record(ctx, p, EventUpdated, meta, c)
	})
	if err != nil {
		return nil, err
	}
	if validated != nil && !checksDeferred(ctx) {
		s.checkAndStore(ctx, c)
	}
	return s.Get(ctx, id)
}

// Dependent is something that uses a connection and blocks its deletion.
type Dependent struct {
	Type string
	ID   uuid.UUID
	Name string
}

// Dependents lists the queries that use the connection.
func (s *Service) Dependents(ctx context.Context, id uuid.UUID) ([]Dependent, error) {
	qs, err := s.store.Queries().ListByConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Dependent, len(qs))
	for i, q := range qs {
		out[i] = Dependent{Type: "query", ID: q.ID, Name: q.Title}
	}
	return out, nil
}

// Delete removes a connection that nothing uses.
func (s *Service) Delete(ctx context.Context, p *auth.Principal, id uuid.UUID, meta auth.RequestMeta) error {
	if c, err := s.store.Connections().Get(ctx, id); err == nil {
		if err := store.CheckManaged(ctx, c.ManagedBy); err != nil {
			return err
		}
	}
	deps, err := s.Dependents(ctx, id)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		e := *ErrInUse
		for _, d := range deps {
			e.Dependents = append(e.Dependents, apperr.Dependent{Type: d.Type, ID: d.ID.String(), Name: d.Name})
		}
		return &e
	}
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return err
	}
	return s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Connections().Delete(ctx, id); err != nil {
			return err
		}
		return s.record(ctx, p, EventDeleted, meta, c)
	})
}

// TestResult is the outcome of a connection check. A failure is data, not an error.
type TestResult struct {
	OK            bool
	LatencyMS     int64
	ServerVersion string
	CanWrite      *bool
	ErrorCode     string
	ErrorDetail   map[string]string
	Schema        *plugin.DBSchema
}

// TestInput tests configuration that is not saved (the form). With ConnectionID set, secret
// placeholders are resolved from that saved connection, so an edited form can be tested before
// saving without re-entering passwords.
type TestInput struct {
	ConnectionID *uuid.UUID
	Driver       string
	Config       map[string]any
}

// Test checks unsaved configuration.
func (s *Service) Test(ctx context.Context, in TestInput) (*TestResult, error) {
	conn, ok := connector(in.Driver)
	if !ok {
		return nil, ErrUnknownDriver
	}
	var existing map[string]any
	if in.ConnectionID != nil {
		c, err := s.store.Connections().Get(ctx, *in.ConnectionID)
		if err != nil {
			return nil, err
		}
		if c.Driver != in.Driver {
			return nil, errDriverImmutable
		}
		if existing, err = s.decrypt(c); err != nil {
			return nil, err
		}
	}
	values := secretconfig.Resolve(conn.ConfigSchema(), in.Config, existing)
	validated, err := conn.ConfigSchema().Validate(values)
	if err != nil {
		return nil, err
	}
	return s.check(ctx, conn, validated), nil
}

// TestSaved checks a saved connection and records the outcome and the schema.
func (s *Service) TestSaved(ctx context.Context, id uuid.UUID) (*TestResult, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.checkAndStore(ctx, c), nil
}

// Schema returns the cached schema; cachedAt is nil when the schema was never introspected.
func (s *Service) Schema(ctx context.Context, id uuid.UUID) (*plugin.DBSchema, *time.Time, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	schema := &plugin.DBSchema{Tables: []plugin.Table{}}
	if len(c.SchemaCache) > 0 {
		if err := json.Unmarshal(c.SchemaCache, schema); err != nil {
			return nil, nil, fmt.Errorf("connections: decode schema cache: %w", err)
		}
	}
	return schema, c.SchemaCachedAt, nil
}

// RefreshSchema introspects again. A connection failure is returned as a 422 domain error.
func (s *Service) RefreshSchema(ctx context.Context, id uuid.UUID) (*plugin.DBSchema, *time.Time, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	res := s.checkAndStore(ctx, c)
	if !res.OK {
		return nil, nil, &apperr.Error{Kind: apperr.KindUnprocessable, Code: res.ErrorCode}
	}
	return s.Schema(ctx, id)
}

// Open opens a saved connection for queries (Phase 3). The caller closes it.
func (s *Service) Open(ctx context.Context, id uuid.UUID) (plugin.Conn, *store.Connection, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	conn, ok := connector(c.Driver)
	if !ok {
		return nil, nil, ErrUnknownDriver
	}
	values, err := s.values(c)
	if err != nil {
		return nil, nil, err
	}
	pc, err := conn.Open(ctx, s.openOptions(values))
	if err != nil {
		return nil, nil, toDomain(err)
	}
	return pc, c, nil
}

func (s *Service) openOptions(values map[string]any) plugin.OpenOptions {
	return plugin.OpenOptions{Values: values, Dial: s.dial, SQLiteDirs: s.sqliteDirs, ForbiddenPaths: s.forbidden, Logger: s.logger}
}

// check opens the connection and runs ping, the write check and introspection.
func (s *Service) check(ctx context.Context, conn plugin.Connector, values map[string]any) *TestResult {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	start := time.Now()
	fail := func(err error) *TestResult {
		res := &TestResult{ErrorCode: plugin.ErrCodeFailed}
		if ce, ok := plugin.AsConnError(err); ok {
			res.ErrorCode, res.ErrorDetail = ce.Code, ce.Detail
		}
		s.logger.InfoContext(ctx, "connection check failed", "driver", conn.Meta().ID, "code", res.ErrorCode, "error", secretconfig.Scrub(err.Error(), conn.ConfigSchema(), values))
		return res
	}
	pc, err := conn.Open(ctx, s.openOptions(values))
	if err != nil {
		return fail(err)
	}
	defer func() { _ = pc.Close() }()
	info, err := pc.Ping(ctx)
	if err != nil {
		return fail(err)
	}
	res := &TestResult{OK: true, LatencyMS: time.Since(start).Milliseconds(), ServerVersion: info.Version}
	if res.CanWrite, err = pc.CanWrite(ctx); err != nil {
		res.CanWrite = nil // best effort
	}
	if res.Schema, err = pc.Schema(ctx); err != nil {
		s.logger.InfoContext(ctx, "schema introspection failed", "driver", conn.Meta().ID, "error", secretconfig.Scrub(err.Error(), conn.ConfigSchema(), values))
	}
	return res
}

func (s *Service) checkAndStore(ctx context.Context, c *store.Connection) *TestResult {
	conn, ok := connector(c.Driver)
	if !ok {
		return &TestResult{ErrorCode: plugin.ErrCodeFailed}
	}
	values, err := s.values(c)
	if err != nil {
		return &TestResult{ErrorCode: plugin.ErrCodeFailed}
	}
	res := s.check(ctx, conn, values)
	h := store.ConnectionHealth{Status: store.ConnectionOK, ServerVersion: res.ServerVersion, HasWritePermission: res.CanWrite, CheckedAt: s.clock()}
	if !res.OK {
		h.Status, h.LastError = store.ConnectionError, res.ErrorCode
	}
	if err := s.store.Connections().SetHealth(ctx, c.ID, h); err != nil {
		s.logger.WarnContext(ctx, "could not record connection health", "error", err)
	}
	if res.Schema != nil {
		if b, err := json.Marshal(res.Schema); err == nil {
			if err := s.store.Connections().SetSchemaCache(ctx, c.ID, b, s.clock()); err != nil {
				s.logger.WarnContext(ctx, "could not store the schema cache", "error", err)
			}
		}
	}
	return res
}

// values rebuilds the full configuration: plain settings plus decrypted secrets.
type deferKey struct{}

// DeferChecks makes Create and Update skip the connection check, for callers that change
// connections inside a transaction (config as code) and call Recheck after it commits.
func DeferChecks(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferKey{}, true)
}

func checksDeferred(ctx context.Context) bool { v, _ := ctx.Value(deferKey{}).(bool); return v }

// Recheck connects to a saved connection and records its health and schema, like a save does.
func (s *Service) Recheck(ctx context.Context, id uuid.UUID) error {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return err
	}
	s.checkAndStore(ctx, c)
	return nil
}

// Values returns a connection's configuration with its secrets decrypted. It never leaves the
// server: config as code uses it to tell whether an imported secret differs from the stored one.
func (s *Service) Values(ctx context.Context, id uuid.UUID) (map[string]any, error) {
	c, err := s.store.Connections().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.values(c)
}

func (s *Service) values(c *store.Connection) (map[string]any, error) {
	var schema *plugin.Schema
	if conn, ok := connector(c.Driver); ok {
		schema = conn.ConfigSchema()
	}
	return s.secrets.Values(c.ID, schema, c.Config, c.SecretsEnc)
}

// SafeMessage returns the driver's message behind a failed query on a saved connection, with the
// connection's secret values and anything that looks like a credential removed, so that it can be
// shown on a run. It returns "" when there is no driver message.
func (s *Service) SafeMessage(ctx context.Context, id uuid.UUID, err error) string {
	ce, ok := plugin.AsConnError(err)
	if !ok || ce.Err == nil {
		return ""
	}
	msg := ce.Err.Error()
	c, gerr := s.store.Connections().Get(ctx, id)
	if gerr != nil {
		return logging.RedactString(msg)
	}
	conn, ok := connector(c.Driver)
	values, verr := s.values(c)
	if !ok || verr != nil {
		return logging.RedactString(msg)
	}
	return secretconfig.Scrub(msg, conn.ConfigSchema(), values)
}

func (s *Service) decrypt(c *store.Connection) (map[string]any, error) {
	return s.secrets.Secrets(c.ID, c.SecretsEnc)
}

// setValues splits validated values into config and encrypted secrets.
func (s *Service) setValues(c *store.Connection, conn plugin.Connector, validated map[string]any) error {
	cfg, enc, err := s.secrets.Seal(c.ID, conn.ConfigSchema(), validated)
	if err != nil {
		return err
	}
	c.Config, c.SecretsEnc = cfg, enc
	return nil
}

func checkName(name string) []apperr.FieldError {
	if !nameRe.MatchString(strings.TrimSpace(name)) {
		return []apperr.FieldError{apperr.Field("name", "validation.slug")}
	}
	return nil
}

func applyLimits(c *store.Connection, timeout, maxRows *int, multi *bool, excluded *[]string) []apperr.FieldError {
	var fe []apperr.FieldError
	if timeout != nil {
		if *timeout < 1 || *timeout > maxQueryTimeoutSeconds {
			fe = append(fe, apperr.Field("query_timeout_seconds", plugin.CodeOutOfRange))
		}
		c.QueryTimeoutSeconds = *timeout
	}
	if maxRows != nil {
		if *maxRows < 1 || *maxRows > maxMaxRows {
			fe = append(fe, apperr.Field("max_rows", plugin.CodeOutOfRange))
		}
		c.MaxRows = *maxRows
	}
	if multi != nil {
		c.AllowMultiStatement = *multi
	}
	if excluded != nil {
		clean := make([]string, 0, len(*excluded))
		for _, t := range *excluded {
			if t = strings.TrimSpace(t); t != "" && !slices.Contains(clean, t) {
				clean = append(clean, t)
			}
		}
		if len(clean) > maxExcludedTables {
			fe = append(fe, apperr.Field("ai_excluded_tables", plugin.CodeTooLong))
		}
		slices.Sort(clean)
		c.AIExcludedTables = clean
	}
	return fe
}

// joinErrors merges field errors with a schema validation error. Schema fields are reported under
// "config.<key>" so the form can place them.
func joinErrors(fe []apperr.FieldError, verr error) error {
	if verr != nil {
		ve, ok := apperr.As(verr)
		if !ok {
			return verr
		}
		for _, f := range ve.Fields {
			fe = append(fe, apperr.Field("config."+f.Field, f.Code))
		}
	}
	if len(fe) == 0 {
		return nil
	}
	return apperr.Invalid(fe...)
}

func toDomain(err error) error {
	if ce, ok := plugin.AsConnError(err); ok {
		return &apperr.Error{Kind: apperr.KindUnprocessable, Code: ce.Code}
	}
	return err
}

func (s *Service) record(ctx context.Context, p *auth.Principal, typ string, meta auth.RequestMeta, c *store.Connection) error {
	var actor *uuid.UUID
	if p != nil {
		actor = &p.UserID
	}
	return s.store.SecurityEvents().Record(ctx, &store.SecurityEvent{
		ActorUserID: actor, Type: typ, IP: meta.IP,
		Meta: map[string]any{"connection_id": c.ID.String(), "name": c.Name, "driver": c.Driver},
	})
}
