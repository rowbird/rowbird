// Package notify tells people when something needs attention (docs/spec/03-flows.md, section 8):
// in-app notifications grouped by what they are about, system alerts sent through a primary
// channel with a fallback, emails to report owners, recovery messages, the outbound heartbeat and
// the real-time events the UI listens to.
package notify

import (
	"context"
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/crypto"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// Notification types. The UI translates notifications.<type> with the params.
const (
	TypeChannelFailing   = "channel_failing"
	TypeChannelRecovered = "channel_recovered"
	TypeReportFailing    = "report_failing"
	TypeReportRecovered  = "report_recovered"
	TypeReportPaused     = "report_paused"
	TypeGitOpsFailed     = "gitops_failed"
	TypeReportOrphaned   = "report_orphaned"
	TypeBackupFailed     = "backup_failed"
)

// AlertTimeout bounds one alert send; alerts must not wait as long as report deliveries.
const AlertTimeout = 30 * time.Second

// alertQueue bounds alerts waiting to be sent; beyond it they are dropped and logged.
const alertQueue = 100

//go:embed locales/*.json
var catalogFS embed.FS

var catalog = plugin.MustLoadMessages(catalogFS)

// Service records notifications and sends alerts.
type Service struct {
	store    *store.Store
	keyring  *crypto.Keyring
	channels *channels.Service
	baseURL  string
	logger   *slog.Logger
	now      func() time.Time
	events   *Broker
	http     *http.Client

	// Alerts are sent by Run, in the background, unless the service is synchronous (tests).
	sync   bool
	queue  chan alert
	sendMu sync.Mutex
}

// Options configure the service.
type Options struct {
	// BaseURL is the public address for links in alerts (ROWBIRD_BASE_URL); empty sends none.
	BaseURL string
	Logger  *slog.Logger
	Now     func() time.Time
	// Events receives the real-time events; nil creates a broker.
	Events *Broker
	// Synchronous sends alerts before returning, for tests.
	Synchronous bool
	// Dial connects through the network policy, for the heartbeat.
	Dial netx.DialFunc
}

// New builds the service.
func New(st *store.Store, kr *crypto.Keyring, ch *channels.Service, opts Options) *Service {
	s := &Service{
		store: st, keyring: kr, channels: ch, baseURL: strings.TrimRight(opts.BaseURL, "/"), logger: opts.Logger, now: opts.Now,
		events: opts.Events, sync: opts.Synchronous, queue: make(chan alert, alertQueue),
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.events == nil {
		s.events = NewBroker()
	}
	dial := opts.Dial
	if dial == nil {
		dial = netx.NewDialer(netx.PolicyOpen).DialContext
	}
	s.http = netx.HTTPClient(dial, heartbeatTimeout)
	return s
}

// Events returns the broker of real-time events.
func (s *Service) Events() *Broker { return s.events }

func (s *Service) clock() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

func (s *Service) url(path string) string {
	if s.baseURL == "" {
		return ""
	}
	return s.baseURL + path
}

// occurrence is one thing to notify about.
type occurrence struct {
	typ, severity, group string
	entityType           string
	entityID             uuid.UUID
	params               map[string]any
	users                []uuid.UUID
}

// record stores an occurrence for each user and publishes the new or changed notifications.
func (s *Service) record(ctx context.Context, o occurrence, at time.Time) {
	for _, u := range o.users {
		n := &store.Notification{
			UserID: u, Type: o.typ, Severity: o.severity, TitleKey: "notifications." + o.typ, Params: o.params,
			EntityType: o.entityType, EntityID: &o.entityID, GroupKey: o.group, LastAt: at,
		}
		if o.group == "" {
			// Recoveries and other one-off notices are closed from the start.
			n.ResolvedAt = &at
		}
		stored, _, err := s.store.Notifications().Record(ctx, n)
		if err != nil {
			s.logger.ErrorContext(ctx, "could not record a notification", "type", o.typ, "error", err)
			continue
		}
		s.publishNotification(ctx, stored)
	}
}

// admins returns the active admins of the workspace in ctx.
func (s *Service) admins(ctx context.Context) []uuid.UUID {
	ids, err := s.store.Members().ActiveAdminIDs(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "could not list admins", "error", err)
	}
	return ids
}

// locale is the workspace's language, used for alerts.
func (s *Service) locale(ctx context.Context) string {
	st, err := s.store.Settings().Get(ctx, auth.SettingDefaultLocale)
	if err != nil {
		return "en"
	}
	var l string
	if json.Unmarshal([]byte(st.Value), &l) != nil || l == "" {
		return "en"
	}
	return l
}

// t translates a notify text, with {name} placeholders replaced.
func t(locale, key string, args map[string]any) string {
	s, ok := catalog[locale][key]
	if !ok {
		s, ok = catalog["en"][key]
	}
	if !ok {
		return key
	}
	for k, v := range args {
		s = strings.ReplaceAll(s, "{"+k+"}", toString(v))
	}
	return s
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}
