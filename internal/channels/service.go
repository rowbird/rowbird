// Package channels manages channels: destinations with their configuration validated against the
// destination's schema, secrets encrypted at rest, tests and health (docs/spec/03-flows.md and
// 04-plugins.md, "Destinations").
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/secretconfig"
	"github.com/rowbird/rowbird/internal/store"
	"github.com/rowbird/rowbird/internal/store/ids"
)

// Timeouts.
const (
	testTimeout = 30 * time.Second
	// SendTimeout bounds one send; large attachments over slow links need time.
	SendTimeout = 5 * time.Minute
)

// Security event types.
const (
	EventCreated = "channel_created"
	EventUpdated = "channel_updated"
	EventDeleted = "channel_deleted"
)

// Errors.
var (
	ErrNameTaken      = apperr.New(apperr.KindConflict, "channel.name_taken")
	ErrInUse          = apperr.New(apperr.KindConflict, "channel.in_use")
	ErrUnknownType    = apperr.Invalid(apperr.Field("type", "validation.invalid_value"))
	errTypeImmutable  = apperr.Invalid(apperr.Field("type", "validation.immutable"))
	errMailerNotEmail = apperr.Invalid(apperr.Field("is_system_mailer", "validation.invalid_value"))
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Service manages channels.
type Service struct {
	store    *store.Store
	secrets  *secretconfig.Codec
	dial     netx.DialFunc
	logger   *slog.Logger
	now      func() time.Time
	observer HealthObserver
}

// HealthObserver learns every outcome recorded on a channel, with the status before it, so that it
// can tell a channel that started failing, keeps failing or recovered.
type HealthObserver interface {
	ChannelHealthRecorded(ctx context.Context, c *store.Channel, prev string, ok bool, message string)
}

// Observe sets the observer of channel health. It is called once, while the app is wired.
func (s *Service) Observe(o HealthObserver) { s.observer = o }

// RecordHealth stores the outcome of a send or a test on a channel and tells the observer.
func (s *Service) RecordHealth(ctx context.Context, c *store.Channel, ok bool, message string) error {
	prev, err := s.store.Channels().RecordHealth(ctx, c.ID, ok, s.clock(), message)
	if err != nil {
		return err
	}
	if s.observer != nil {
		s.observer.ChannelHealthRecorded(ctx, c, prev, ok, message)
	}
	return nil
}

// Options configure the service.
type Options struct {
	// Dial connects through the network policy; every destination uses it.
	Dial   netx.DialFunc
	Logger *slog.Logger
	Now    func() time.Time
}

// NewService builds the service.
func NewService(st *store.Store, kr *crypto.Keyring, opts Options) *Service {
	s := &Service{store: st, secrets: secretconfig.New(kr, "channel"), dial: opts.Dial, logger: opts.Logger, now: opts.Now}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.dial == nil {
		s.dial = netx.NewDialer(netx.PolicyOpen).DialContext
	}
	return s
}

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

// Destination returns the destination plugin of a type.
func Destination(typ string) (plugin.Destination, bool) {
	p, ok := plugin.Get(plugin.KindDestination, typ)
	if !ok {
		return nil, false
	}
	d, ok := p.(plugin.Destination)
	return d, ok
}

// Dependent is something that uses a channel: a report's delivery (named after the report), or
// the system alerts ("primary" or "fallback", with the channel's own id).
type Dependent struct {
	Type string
	ID   uuid.UUID
	Name string
}

// View is a channel as the API shows it: no secret values, which secrets are set, and what uses
// it.
type View struct {
	store.Channel
	SecretsConfigured map[string]bool
	UsedBy            []Dependent
}

// Input creates a channel. Config holds every field of the destination's schema, secrets as plain
// strings.
type Input struct {
	Name           string
	Type           string
	Config         map[string]any
	IsSystemMailer bool
}

// Patch changes a channel. Config, when not nil, replaces the configuration with the placeholder
// rules for secrets (absent or placeholder keeps, null clears, a string replaces).
type Patch struct {
	Version        int64
	Name           *string
	Config         map[string]any
	IsSystemMailer *bool
}

// List returns the channels.
func (s *Service) List(ctx context.Context) ([]View, error) {
	cs, err := s.store.Channels().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(cs))
	for i := range cs {
		v, err := s.view(ctx, &cs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns one channel.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*View, error) {
	c, err := s.store.Channels().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.view(ctx, c)
	return &v, err
}

func (s *Service) view(ctx context.Context, c *store.Channel) (View, error) {
	v := View{Channel: *c, SecretsConfigured: map[string]bool{}}
	v.SecretsEnc = nil
	if d, ok := Destination(c.Type); ok {
		secrets, err := s.secrets.Secrets(c.ID, c.SecretsEnc)
		if err == nil {
			v.SecretsConfigured = secretconfig.Configured(d.ConfigSchema(), secrets)
		}
		for _, k := range d.ConfigSchema().SecretKeys() {
			if !v.SecretsConfigured[k] {
				v.SecretsConfigured[k] = false
			}
		}
	}
	deps, err := s.Dependents(ctx, c.ID)
	if err != nil {
		return v, err
	}
	v.UsedBy = deps
	return v, nil
}

// Dependents lists what uses a channel: reports through their deliveries, and system alerts.
func (s *Service) Dependents(ctx context.Context, id uuid.UUID) ([]Dependent, error) {
	ds, err := s.store.Deliveries().ListByChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Dependent, 0, len(ds))
	seen := map[uuid.UUID]bool{}
	for _, d := range ds {
		if seen[d.ReportID] {
			continue
		}
		seen[d.ReportID] = true
		name := ""
		if rp, err := s.store.Reports().Get(ctx, d.ReportID); err == nil {
			name = rp.Title
		}
		out = append(out, Dependent{Type: "report", ID: d.ReportID, Name: name})
	}
	for _, a := range []struct{ key, name string }{
		{store.SettingAlertPrimaryChannel, "primary"}, {store.SettingAlertFallbackChannel, "fallback"},
	} {
		st, err := s.store.Settings().Get(ctx, a.key)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var used uuid.UUID
		if json.Unmarshal([]byte(st.Value), &used) == nil && used == id {
			out = append(out, Dependent{Type: "system_alert", ID: id, Name: a.name})
		}
	}
	return out, nil
}

// Create validates and stores a channel.
func (s *Service) Create(ctx context.Context, p *auth.Principal, in Input, meta auth.RequestMeta) (*View, error) {
	d, ok := Destination(in.Type)
	if !ok {
		return nil, ErrUnknownType
	}
	fe := checkName(in.Name)
	if in.IsSystemMailer && in.Type != "email" {
		return nil, errMailerNotEmail
	}
	validated, verr := d.ConfigSchema().Validate(secretconfig.Resolve(d.ConfigSchema(), in.Config, nil))
	if err := joinErrors(fe, verr); err != nil {
		return nil, err
	}
	c := &store.Channel{Name: strings.TrimSpace(in.Name), Type: in.Type, IsSystemMailer: in.IsSystemMailer}
	c.ID = ids.New()
	c.CreatedBy = &p.UserID
	var err error
	if c.Config, c.SecretsEnc, err = s.secrets.Seal(c.ID, d.ConfigSchema(), validated); err != nil {
		return nil, err
	}
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Channels().Create(ctx, c); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return ErrNameTaken
			}
			return err
		}
		if c.IsSystemMailer {
			if err := s.store.Channels().ClearSystemMailer(ctx, c.ID); err != nil {
				return err
			}
		}
		return s.record(ctx, p, EventCreated, meta, c)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, c.ID)
}

// Update applies a patch with optimistic concurrency.
func (s *Service) Update(ctx context.Context, p *auth.Principal, id uuid.UUID, patch Patch, meta auth.RequestMeta) (*View, error) {
	c, err := s.store.Channels().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := store.CheckManaged(ctx, c.ManagedBy); err != nil {
		return nil, err
	}
	d, ok := Destination(c.Type)
	if !ok {
		return nil, ErrUnknownType
	}
	c.Version = patch.Version
	var fe []apperr.FieldError
	var cols []string
	if patch.Name != nil {
		fe = append(fe, checkName(*patch.Name)...)
		c.Name = strings.TrimSpace(*patch.Name)
		cols = append(cols, "name")
	}
	if patch.IsSystemMailer != nil {
		if *patch.IsSystemMailer && c.Type != "email" {
			return nil, errMailerNotEmail
		}
		c.IsSystemMailer = *patch.IsSystemMailer
		cols = append(cols, "is_system_mailer")
	}
	var verr error
	if patch.Config != nil {
		if t, ok := patch.Config["type"]; ok && t != c.Type {
			return nil, errTypeImmutable
		}
		existing, err := s.secrets.Secrets(c.ID, c.SecretsEnc)
		if err != nil {
			return nil, err
		}
		var validated map[string]any
		validated, verr = d.ConfigSchema().Validate(secretconfig.Resolve(d.ConfigSchema(), patch.Config, existing))
		if verr == nil {
			if c.Config, c.SecretsEnc, err = s.secrets.Seal(c.ID, d.ConfigSchema(), validated); err != nil {
				return nil, err
			}
			// New settings, unknown health until the next send or test.
			c.Status, c.LastError = store.ChannelUnknown, nil
			cols = append(cols, "config", "secrets_enc", "status", "last_error")
		}
	}
	if err := joinErrors(fe, verr); err != nil {
		return nil, err
	}
	err = s.store.RunInTx(ctx, func(ctx context.Context) error {
		if len(cols) > 0 {
			if err := s.store.Channels().Update(ctx, c, cols...); err != nil {
				if errors.Is(err, store.ErrDuplicate) {
					return ErrNameTaken
				}
				return err
			}
		}
		if c.IsSystemMailer {
			if err := s.store.Channels().ClearSystemMailer(ctx, c.ID); err != nil {
				return err
			}
		}
		return s.record(ctx, p, EventUpdated, meta, c)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete removes a channel that no delivery uses.
func (s *Service) Delete(ctx context.Context, p *auth.Principal, id uuid.UUID, meta auth.RequestMeta) error {
	if c, err := s.store.Channels().Get(ctx, id); err == nil {
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
	c, err := s.store.Channels().Get(ctx, id)
	if err != nil {
		return err
	}
	return s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.store.Channels().Delete(ctx, id); err != nil {
			return err
		}
		return s.record(ctx, p, EventDeleted, meta, c)
	})
}

// Env builds what a destination needs to send through a saved channel: its configuration with
// secrets, and the network through the policy.
func (s *Service) Env(ctx context.Context, id uuid.UUID) (plugin.Destination, plugin.DestinationEnv, *store.Channel, error) {
	c, err := s.store.Channels().Get(ctx, id)
	if err != nil {
		return nil, plugin.DestinationEnv{}, nil, err
	}
	d, ok := Destination(c.Type)
	if !ok {
		return nil, plugin.DestinationEnv{}, nil, ErrUnknownType
	}
	values, err := s.secrets.Values(c.ID, d.ConfigSchema(), c.Config, c.SecretsEnc)
	if err != nil {
		return nil, plugin.DestinationEnv{}, nil, err
	}
	return d, s.env(values), c, nil
}

func (s *Service) env(values map[string]any) plugin.DestinationEnv {
	return plugin.DestinationEnv{Config: values, HTTP: netx.HTTPClient(s.dial, SendTimeout), Dial: plugin.DialFunc(s.dial), Options: map[string]any{}}
}

// SafeMessage returns a destination error's text with the channel's secrets removed, for logs and
// the attempt record.
func SafeMessage(d plugin.Destination, env plugin.DestinationEnv, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// The code is stored and shown on its own; the message is what the destination said.
	var de *plugin.DeliveryError
	if errors.As(err, &de) && de.Err != nil {
		msg = de.Err.Error()
	}
	return secretconfig.Scrub(msg, d.ConfigSchema(), env.Config)
}

// TestResult is the outcome of a channel test.
type TestResult struct {
	OK           bool
	ErrorCode    string
	ErrorMessage string
}

// TestInput tests configuration that is not saved (the form). With ChannelID set, secret
// placeholders come from that saved channel. To, when set, receives the test message (email).
type TestInput struct {
	ChannelID *uuid.UUID
	Type      string
	Config    map[string]any
	To        string
	Locale    string
}

// Test checks unsaved configuration.
func (s *Service) Test(ctx context.Context, in TestInput) (*TestResult, error) {
	d, ok := Destination(in.Type)
	if !ok {
		return nil, ErrUnknownType
	}
	var existing map[string]any
	if in.ChannelID != nil {
		c, err := s.store.Channels().Get(ctx, *in.ChannelID)
		if err != nil {
			return nil, err
		}
		if c.Type != in.Type {
			return nil, errTypeImmutable
		}
		if existing, err = s.secrets.Secrets(c.ID, c.SecretsEnc); err != nil {
			return nil, err
		}
	}
	validated, err := d.ConfigSchema().Validate(secretconfig.Resolve(d.ConfigSchema(), in.Config, existing))
	if err != nil {
		return nil, err
	}
	return s.run(ctx, d, s.env(validated), in.To, in.Locale), nil
}

// TestSaved tests a saved channel and records its health.
func (s *Service) TestSaved(ctx context.Context, id uuid.UUID, to, locale string) (*TestResult, error) {
	d, env, c, err := s.Env(ctx, id)
	if err != nil {
		return nil, err
	}
	res := s.run(ctx, d, env, to, locale)
	if err := s.RecordHealth(ctx, c, res.OK, res.ErrorMessage); err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) run(ctx context.Context, d plugin.Destination, env plugin.DestinationEnv, to, locale string) *TestResult {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	env.Locale = locale
	if to != "" {
		env.Options = map[string]any{"to": to}
	}
	err := d.Test(ctx, env)
	if err == nil {
		return &TestResult{OK: true}
	}
	res := &TestResult{ErrorCode: plugin.ErrCodeDeliveryFailed, ErrorMessage: SafeMessage(d, env, err)}
	var de *plugin.DeliveryError
	if errors.As(err, &de) {
		res.ErrorCode = de.Code
	}
	s.logger.InfoContext(ctx, "channel test failed", "type", d.Meta().ID, "code", res.ErrorCode, "error", res.ErrorMessage)
	return res
}

func (s *Service) record(ctx context.Context, p *auth.Principal, typ string, meta auth.RequestMeta, c *store.Channel) error {
	var actor *uuid.UUID
	if p != nil {
		actor = &p.UserID
	}
	return s.store.SecurityEvents().Record(ctx, &store.SecurityEvent{
		ActorUserID: actor, Type: typ, IP: meta.IP,
		Meta: map[string]any{"channel_id": c.ID.String(), "name": c.Name, "type": c.Type},
	})
}

func checkName(name string) []apperr.FieldError {
	if !nameRe.MatchString(strings.TrimSpace(name)) {
		return []apperr.FieldError{apperr.Field("name", "validation.slug")}
	}
	return nil
}

// joinErrors merges field errors with a schema validation error, prefixing schema fields with
// "config.".
func joinErrors(fe []apperr.FieldError, verr error) error {
	if verr != nil {
		ae, ok := apperr.As(verr)
		if !ok {
			return verr
		}
		for _, f := range ae.Fields {
			fe = append(fe, apperr.Field("config."+f.Field, f.Code))
		}
	}
	if len(fe) > 0 {
		return apperr.Invalid(fe...)
	}
	return nil
}
