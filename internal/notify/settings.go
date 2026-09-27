package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/auth"
	"github.com/rowbird/rowbird/internal/channels"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/store"
)

// Heartbeat interval bounds.
const (
	DefaultHeartbeatInterval = time.Minute
	MinHeartbeatInterval     = 30 * time.Second
	MaxHeartbeatInterval     = 24 * time.Hour
)

// EventAlertSettingsChanged is the security event of a change to alert or heartbeat settings.
const EventAlertSettingsChanged = "settings_changed"

// Target is a channel that receives system alerts, with the options a delivery would have
// (recipients, chat, Slack channel), validated by the destination's delivery schema.
type Target struct {
	ChannelID uuid.UUID
	Options   map[string]any
}

// Settings are the system alert and heartbeat settings of a workspace.
type Settings struct {
	Primary  *Target
	Fallback *Target
	// HeartbeatConfigured says a heartbeat URL is stored; the URL itself is secret.
	HeartbeatConfigured bool
	HeartbeatInterval   time.Duration
	// SameDestination warns that primary and fallback have the same type: an outage of that
	// service would take both down.
	SameDestination bool
}

// SettingsInput replaces the settings. HeartbeatURL nil keeps the stored URL, "" removes it.
type SettingsInput struct {
	Primary           *Target
	Fallback          *Target
	HeartbeatURL      *string
	HeartbeatInterval time.Duration
}

// HeartbeatAssociatedData binds the encrypted heartbeat URL to its workspace.
func HeartbeatAssociatedData(ws uuid.UUID) []byte {
	return []byte("setting:" + ws.String() + ":heartbeat_url")
}

// GetSettings reads the alert and heartbeat settings.
func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	out := Settings{HeartbeatInterval: DefaultHeartbeatInterval}
	var err error
	if out.Primary, err = s.target(ctx, store.SettingAlertPrimaryChannel, store.SettingAlertPrimaryOptions); err != nil {
		return out, err
	}
	if out.Fallback, err = s.target(ctx, store.SettingAlertFallbackChannel, store.SettingAlertFallbackOptions); err != nil {
		return out, err
	}
	if _, err := s.store.Settings().Get(ctx, store.SettingHeartbeatURL); err == nil {
		out.HeartbeatConfigured = true
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	if st, err := s.store.Settings().Get(ctx, store.SettingHeartbeatInterval); err == nil {
		var secs int64
		if json.Unmarshal([]byte(st.Value), &secs) == nil && secs > 0 {
			out.HeartbeatInterval = time.Duration(secs) * time.Second
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	out.SameDestination = s.sameDestination(ctx, out.Primary, out.Fallback)
	return out, nil
}

func (s *Service) target(ctx context.Context, channelKey, optionsKey string) (*Target, error) {
	st, err := s.store.Settings().Get(ctx, channelKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t := &Target{Options: map[string]any{}}
	if err := json.Unmarshal([]byte(st.Value), &t.ChannelID); err != nil {
		return nil, err
	}
	if o, err := s.store.Settings().Get(ctx, optionsKey); err == nil {
		_ = json.Unmarshal([]byte(o.Value), &t.Options)
	}
	return t, nil
}

func (s *Service) sameDestination(ctx context.Context, a, b *Target) bool {
	if a == nil || b == nil {
		return false
	}
	ca, err1 := s.store.Channels().Get(ctx, a.ChannelID)
	cb, err2 := s.store.Channels().Get(ctx, b.ChannelID)
	return err1 == nil && err2 == nil && ca.Type == cb.Type
}

// HeartbeatURL returns the stored heartbeat URL, or "" when none is set.
func (s *Service) HeartbeatURL(ctx context.Context) (string, error) {
	st, err := s.store.Settings().Get(ctx, store.SettingHeartbeatURL)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var enc string
	if err := json.Unmarshal([]byte(st.Value), &enc); err != nil {
		return "", err
	}
	ws, ok := store.WorkspaceFrom(ctx)
	if !ok {
		return "", store.ErrNoWorkspace
	}
	b, err := s.keyring.Decrypt(enc, HeartbeatAssociatedData(ws))
	return string(b), err
}

// UpdateSettings validates and replaces the settings (admin only, enforced by the API).
func (s *Service) UpdateSettings(ctx context.Context, p *auth.Principal, in SettingsInput, meta auth.RequestMeta) (Settings, error) {
	var fe []apperr.FieldError
	primary, errs := s.validTarget(ctx, "primary", in.Primary)
	fe = append(fe, errs...)
	fallback, errs := s.validTarget(ctx, "fallback", in.Fallback)
	fe = append(fe, errs...)
	if primary != nil && fallback != nil && primary.ChannelID == fallback.ChannelID {
		fe = append(fe, apperr.Field("fallback.channel_id", "validation.same_channel"))
	}
	interval := in.HeartbeatInterval
	if interval == 0 {
		interval = DefaultHeartbeatInterval
	}
	if interval < MinHeartbeatInterval || interval > MaxHeartbeatInterval {
		fe = append(fe, apperr.Field("heartbeat_interval_seconds", "validation.range"))
	}
	var heartbeat string
	if in.HeartbeatURL != nil {
		heartbeat = strings.TrimSpace(*in.HeartbeatURL)
		if u, err := url.Parse(heartbeat); heartbeat != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
			fe = append(fe, apperr.Field("heartbeat_url", "validation.url"))
		}
	}
	if len(fe) > 0 {
		return Settings{}, apperr.Invalid(fe...)
	}
	ws, ok := store.WorkspaceFrom(ctx)
	if !ok {
		return Settings{}, store.ErrNoWorkspace
	}
	err := s.store.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.putTarget(ctx, store.SettingAlertPrimaryChannel, store.SettingAlertPrimaryOptions, primary); err != nil {
			return err
		}
		if err := s.putTarget(ctx, store.SettingAlertFallbackChannel, store.SettingAlertFallbackOptions, fallback); err != nil {
			return err
		}
		secs, _ := json.Marshal(int64(interval / time.Second))
		if err := s.store.Settings().Put(ctx, store.SettingHeartbeatInterval, string(secs), false); err != nil {
			return err
		}
		changed := []string{"system_alerts", "heartbeat_interval"}
		switch {
		case in.HeartbeatURL == nil:
		case heartbeat == "":
			changed = append(changed, "heartbeat_url")
			if err := s.store.Settings().Delete(ctx, store.SettingHeartbeatURL); err != nil {
				return err
			}
		default:
			changed = append(changed, "heartbeat_url")
			enc, err := s.keyring.Encrypt([]byte(heartbeat), HeartbeatAssociatedData(ws))
			if err != nil {
				return err
			}
			b, _ := json.Marshal(enc)
			if err := s.store.Settings().Put(ctx, store.SettingHeartbeatURL, string(b), true); err != nil {
				return err
			}
		}
		return s.store.SecurityEvents().Record(ctx, &store.SecurityEvent{
			ActorUserID: &p.UserID, Type: EventAlertSettingsChanged, IP: meta.IP, Meta: map[string]any{"keys": changed},
		})
	})
	if err != nil {
		return Settings{}, err
	}
	s.logger.InfoContext(ctx, "alert settings changed")
	return s.GetSettings(ctx)
}

// validTarget checks that the channel exists, can carry alerts, and that the options suit its
// destination. Field errors are prefixed with name.
func (s *Service) validTarget(ctx context.Context, name string, t *Target) (*Target, []apperr.FieldError) {
	if t == nil {
		return nil, nil
	}
	ch, err := s.store.Channels().Get(ctx, t.ChannelID)
	if err != nil {
		return nil, []apperr.FieldError{apperr.Field(name+".channel_id", "validation.invalid_value")}
	}
	dest, ok := channels.Destination(ch.Type)
	if !ok || !plugin.CapabilitiesOf(dest, ch.Config).SupportsAlerts {
		return nil, []apperr.FieldError{apperr.Field(name+".channel_id", "validation.alerts_unsupported")}
	}
	opts := t.Options
	if opts == nil {
		opts = map[string]any{}
	}
	validated, verr := dest.DeliverySchema().Validate(opts)
	var fe []apperr.FieldError
	if ae, ok := apperr.As(verr); ok {
		for _, f := range ae.Fields {
			fe = append(fe, apperr.Field(name+".options."+f.Field, f.Code))
		}
	} else if verr != nil {
		fe = append(fe, apperr.Field(name+".options", "validation.invalid_value"))
	}
	if v, ok := dest.(plugin.DeliveryValidator); ok && verr == nil {
		if field, code, valid := v.ValidateDelivery(validated); !valid {
			fe = append(fe, apperr.Field(name+".options."+field, code))
		}
	}
	if len(fe) > 0 {
		return nil, fe
	}
	return &Target{ChannelID: ch.ID, Options: validated}, nil
}

func (s *Service) putTarget(ctx context.Context, channelKey, optionsKey string, t *Target) error {
	if t == nil {
		if err := s.store.Settings().Delete(ctx, channelKey); err != nil {
			return err
		}
		return s.store.Settings().Delete(ctx, optionsKey)
	}
	id, _ := json.Marshal(t.ChannelID)
	opts, err := json.Marshal(t.Options)
	if err != nil {
		return err
	}
	if err := s.store.Settings().Put(ctx, channelKey, string(id), false); err != nil {
		return err
	}
	return s.store.Settings().Put(ctx, optionsKey, string(opts), false)
}
