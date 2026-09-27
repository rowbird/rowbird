// Package delivery sends a run's result to its report's deliveries (docs/spec/03-flows.md,
// sections 5 and 6): each file format is generated once from the spool and kept as an artifact,
// each destination gets the result rendered for it, files it cannot take (too large, or not
// supported) become shared links, and each delivery retries on its own without running the query
// again.
package delivery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/metrics"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/spool"
	"github.com/rowbird/rowbird/internal/store"
)

// Defaults.
const (
	DefaultArtifactDays = store.DefaultRetentionArtifactsDays
	MaxAttempts         = 3
	parallel            = 4
	// textReserve is room kept for the message text around an inline result.
	textReserve = 800
	maxRowsSent = 1000
)

// Error codes set by the engine.
const (
	CodeBaseURLMissing = "delivery.base_url_missing"
	CodeChannel        = "delivery.channel_unavailable"
	CodeNoResult       = "delivery.result_unavailable"
)

// Engine delivers runs.
type Engine struct {
	store    *store.Store
	channels *channels.Service
	storage  plugin.Storage
	backend  string
	baseURL  string
	spoolDir string
	logger   *slog.Logger
	now      func() time.Time
	backoff  time.Duration
	notifier Notifier
	metrics  *metrics.Metrics
}

// Notifier is told when a resend changed a run's deliveries.
type Notifier interface {
	RunUpdated(ctx context.Context, run *store.Run)
}

// Options configure the engine.
type Options struct {
	Storage plugin.Storage
	// Backend names the storage in artifact rows ("local", "s3").
	Backend string
	// BaseURL is the public address for links; empty disables links (ROWBIRD_BASE_URL).
	BaseURL  string
	SpoolDir string
	Logger   *slog.Logger
	Now      func() time.Time
	// Backoff is the wait before the second attempt, doubled for each later one (default 2 s).
	Backoff time.Duration
	// Notifier learns about resends; nil tells nobody.
	Notifier Notifier
	// Metrics counts sends; nil records nothing.
	Metrics *metrics.Metrics
}

// New builds an engine.
func New(st *store.Store, ch *channels.Service, opts Options) *Engine {
	e := &Engine{
		store: st, channels: ch, storage: opts.Storage, backend: opts.Backend, baseURL: strings.TrimRight(opts.BaseURL, "/"),
		spoolDir: opts.SpoolDir, logger: opts.Logger, now: opts.Now, backoff: opts.Backoff, notifier: opts.Notifier, metrics: opts.Metrics,
	}
	if e.logger == nil {
		e.logger = slog.New(slog.DiscardHandler)
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.backoff <= 0 {
		e.backoff = 2 * time.Second
	}
	return e
}

func (e *Engine) clock() time.Time { return e.now().UTC().Truncate(time.Microsecond) }

// BaseURL is the public address links use, empty when none is configured.
func (e *Engine) BaseURL() string { return e.baseURL }

// Outcome of a run's deliveries.
type Outcome struct {
	// Failed counts deliveries that failed after their attempts.
	Failed int
	Sent   int
}

// Input is what a run hands to the engine.
type Input struct {
	Run    *store.Run
	Report *store.Report
	// Status is plugin.StatusUp, StatusDown or StatusSkipped; only always-notify destinations get
	// down and skipped runs.
	Status   string
	Location *time.Location
	// Condition summarizes the condition for messages.
	Condition string
	// Deliveries replace the report's deliveries (test deliveries).
	Deliveries []store.Delivery
	Test       bool
}

// Deliver sends a run to its deliveries and records one attempt per delivery.
func (e *Engine) Deliver(ctx context.Context, in Input) (Outcome, error) {
	ds := in.Deliveries
	if ds == nil {
		all, err := e.store.Deliveries().ListByReport(ctx, in.Report.ID)
		if err != nil {
			return Outcome{}, err
		}
		for _, d := range all {
			if d.Enabled {
				ds = append(ds, d)
			}
		}
	}
	type job struct {
		d    store.Delivery
		dest plugin.Destination
		env  plugin.DestinationEnv
		caps plugin.DestinationCapabilities
		ch   *store.Channel
	}
	var jobs []job
	for _, d := range ds {
		dest, env, ch, err := e.channels.Env(ctx, d.ChannelID)
		if err != nil {
			e.logger.ErrorContext(ctx, "delivery channel unavailable", "delivery_id", d.ID, "error", err)
			e.record(ctx, in.Run, d, nil, store.AttemptFailed, CodeChannel, "", nil, 0)
			continue
		}
		caps := plugin.CapabilitiesOf(dest, env.Config)
		if in.Status != plugin.StatusUp && !caps.AlwaysNotify {
			continue
		}
		// Options come back from JSON with float64 numbers; the schema restores their types.
		opts, err := dest.DeliverySchema().Validate(d.Options)
		if err != nil {
			e.record(ctx, in.Run, d, &ch.ID, store.AttemptFailed, plugin.ErrCodeDeliveryRejected, "the delivery's options are no longer valid", nil, 0)
			continue
		}
		env.Options, d.Options = opts, opts
		jobs = append(jobs, job{d: d, dest: dest, env: env, caps: caps, ch: ch})
	}
	if len(jobs) == 0 {
		return Outcome{}, nil
	}
	locale := e.locale(ctx)
	r := &run{e: e, in: in, locale: locale, artifacts: map[string]*store.Artifact{}}
	var mu sync.Mutex
	var out Outcome
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			ok := r.deliver(ctx, j.d, j.dest, j.env, j.caps, j.ch)
			mu.Lock()
			if ok {
				out.Sent++
			} else {
				out.Failed++
			}
			mu.Unlock()
		})
	}
	wg.Wait()
	return out, ctx.Err()
}

// run holds what the deliveries of one run share: artifacts generated once, the spool.
type run struct {
	e         *Engine
	in        Input
	locale    string
	mu        sync.Mutex
	artifacts map[string]*store.Artifact
}

func (r *run) deliver(ctx context.Context, d store.Delivery, dest plugin.Destination, env plugin.DestinationEnv, caps plugin.DestinationCapabilities, ch *store.Channel) bool {
	e := r.e
	logger := e.logger.With("run_id", r.in.Run.ID, "report_id", r.in.Report.ID, "delivery_id", d.ID, "channel", ch.Name)
	env.Locale = r.locale
	msg, meta, err := r.message(ctx, d, caps)
	if err != nil {
		code := plugin.ErrCodeDeliveryFailed
		var de *plugin.DeliveryError
		if errors.As(err, &de) {
			code = de.Code
		}
		logger.WarnContext(ctx, "delivery could not be prepared", "error_code", code, "error", err)
		e.record(ctx, r.in.Run, d, &ch.ID, store.AttemptFailed, code, err.Error(), meta, 0)
		_ = e.channels.RecordHealth(ctx, ch, false, code)
		return false
	}
	att := e.record(ctx, r.in.Run, d, &ch.ID, store.AttemptSending, "", "", meta, 0)
	return e.send(ctx, att, dest, env, ch, msg, meta, logger)
}

// send tries a message up to MaxAttempts times and records the attempt (when att is not nil) and
// the channel's health. A resend adds its tries to the attempt's count.
func (e *Engine) send(ctx context.Context, att *store.DeliveryAttempt, dest plugin.Destination, env plugin.DestinationEnv, ch *store.Channel,
	msg plugin.Message, meta map[string]any, logger *slog.Logger,
) bool {
	base := 0
	if att != nil {
		base = att.Attempts
	}
	started := time.Now()
	var err error
	for i := range MaxAttempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(e.backoff << (i - 1)):
			}
			if ctx.Err() != nil {
				break
			}
		}
		sctx, cancel := context.WithTimeout(ctx, channels.SendTimeout)
		var res plugin.SendResult
		res, err = dest.Send(sctx, env, msg)
		cancel()
		if att != nil {
			att.Attempts = base + i + 1
		}
		if err == nil {
			for k, v := range res.Meta {
				meta[k] = v
			}
			break
		}
		var de *plugin.DeliveryError
		if !errors.As(err, &de) || !de.Retry {
			break
		}
	}
	e.metrics.DeliverySent(dest.Meta().ID, err == nil, time.Since(started))
	now := e.clock()
	if att != nil {
		att.Meta = meta
		if err == nil {
			att.Status, att.SentAt, att.LastErrorCode, att.LastError = store.AttemptSent, &now, nil, nil
		} else {
			code := plugin.ErrCodeDeliveryFailed
			var de *plugin.DeliveryError
			if errors.As(err, &de) {
				code = de.Code
			}
			message := channels.SafeMessage(dest, env, err)
			att.Status, att.LastErrorCode, att.LastError = store.AttemptFailed, &code, &message
		}
		if serr := e.store.Attempts().Save(ctx, att); serr != nil {
			logger.ErrorContext(ctx, "could not record a delivery attempt", "error", serr)
		}
	}
	health := ""
	if err != nil {
		health = channels.SafeMessage(dest, env, err)
		logger.WarnContext(ctx, "delivery failed", "error", health)
	} else {
		logger.InfoContext(ctx, "delivered")
	}
	if herr := e.channels.RecordHealth(ctx, ch, err == nil, health); herr != nil {
		logger.ErrorContext(ctx, "could not record channel health", "error", herr)
	}
	return err == nil
}

// record creates an attempt row; it returns nil when the row could not be written.
func (e *Engine) record(ctx context.Context, run *store.Run, d store.Delivery, channel *uuid.UUID, status, code, message string, meta map[string]any, attempts int) *store.DeliveryAttempt {
	a := &store.DeliveryAttempt{RunID: run.ID, ChannelID: channel, Status: status, Attempts: attempts, Meta: meta}
	if d.ID != uuid.Nil {
		a.DeliveryID = &d.ID
	}
	if code != "" {
		a.LastErrorCode = &code
	}
	if message != "" {
		a.LastError = &message
	}
	a.CreatedAt = e.clock()
	if err := e.store.Attempts().Create(ctx, a); err != nil {
		e.logger.ErrorContext(ctx, "could not record a delivery attempt", "run_id", run.ID, "error", err)
		return nil
	}
	return a
}

func (e *Engine) locale(ctx context.Context) string {
	st, err := e.store.Settings().Get(ctx, auth.SettingDefaultLocale)
	if err != nil {
		return "en"
	}
	var l string
	if json.Unmarshal([]byte(st.Value), &l) != nil || l == "" {
		return "en"
	}
	return l
}

// newToken returns a link token and its hash (docs/spec/07-security.md: 256 random bits).
func newToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := "rbl_" + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is how link tokens are stored and looked up.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// hashingWriter counts and hashes what goes through it.
type hashingWriter struct {
	w    io.Writer
	h    interface{ Write([]byte) (int, error) }
	size int64
}

func (hw *hashingWriter) Write(p []byte) (int, error) {
	n, err := hw.w.Write(p)
	_, _ = hw.h.Write(p[:n])
	hw.size += int64(n)
	return n, err
}

func openSpool(dir string, runID uuid.UUID) (*spool.Reader, error) {
	r, err := spool.Open(spool.Path(dir, runID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, &plugin.DeliveryError{Code: CodeNoResult, Err: err}
	}
	return r, err
}

func errorf(code string, format string, args ...any) error {
	return &plugin.DeliveryError{Code: code, Err: fmt.Errorf(format, args...)}
}
