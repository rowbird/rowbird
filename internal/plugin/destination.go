package plugin

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Destination delivers a run's result somewhere: an inbox, a chat, a URL, a bucket
// (docs/spec/04-plugins.md). A channel is a destination with its configuration; a delivery sends a
// report's runs to a channel with per-delivery options validated by DeliverySchema.
type Destination interface {
	Plugin
	// DeliverySchema is the schema of per-delivery options (recipients, templates, paths, ...).
	DeliverySchema() *Schema
	// Send delivers a message. Errors should be DeliveryError with a stable code.
	Send(ctx context.Context, env DestinationEnv, msg Message) (SendResult, error)
	// Test checks the configuration, sending a short test message where the destination can.
	Test(ctx context.Context, env DestinationEnv) error
	// Preview shows what Send would deliver, without sending anything.
	Preview(ctx context.Context, env DestinationEnv, msg Message) (Preview, error)
}

// DestinationEnv is what a destination needs besides the message: its channel configuration and
// the network through which it must connect.
type DestinationEnv struct {
	// Config is the channel's configuration, secrets included, validated by ConfigSchema.
	Config map[string]any
	// Options are the delivery's options, validated by DeliverySchema (empty for tests).
	Options map[string]any
	// HTTP is a client bound to the network policy; Dial dials through it for other protocols.
	HTTP *http.Client
	Dial DialFunc
	// Locale of the texts a destination writes itself (test messages).
	Locale string
}

// Run statuses as destinations see them.
const (
	StatusUp      = "up"
	StatusDown    = "down"
	StatusSkipped = "skipped"
)

// Message is one delivery of one run.
type Message struct {
	// DeliveryID and RunID identify the delivery; webhooks send them as headers.
	DeliveryID string
	RunID      string
	Report     MessageReport
	Run        MessageRun
	// Status is StatusUp for a run that succeeded, StatusDown for a failure and StatusSkipped for
	// a run whose condition did not hold. Only destinations with AlwaysNotify see down and skipped.
	Status string
	Locale string
	// Inline is the result rendered for the destination's inline target (empty when the delivery
	// does not show rows inline). Destinations render their own templates from the other fields.
	Inline string
	// InlineCut says the inline rendering left rows out.
	InlineCut bool
	// Attachments are the files to attach; Links the files shared as links instead.
	Attachments []Attachment
	Links       []Link
	// Rows are the first rows, for destinations that send data (webhooks).
	Columns []Column
	Rows    [][]any
	// Test is true for test deliveries ("send test to me").
	Test bool
	// Location is the report's time zone, for dates in messages.
	Location *time.Location
	// FallbackNote explains that attachments were sent as links (too large, or not supported).
	FallbackNote bool
	// Alert, when set, makes this a system alert instead of a run delivery: destinations send its
	// title and text and ignore the report, run and result fields.
	Alert *MessageAlert
}

// MessageAlert is a system alert (a channel or report that started failing, or recovered). Title
// and Text are already translated into the message's locale and are plain text.
type MessageAlert struct {
	Title    string `json:"title"`
	Text     string `json:"text"`
	Severity string `json:"severity"`
	// Recovered is true for the message that ends an alert.
	Recovered bool `json:"recovered"`
	// URL is the page of what the alert is about, absolute, or empty without ROWBIRD_BASE_URL.
	URL string `json:"url,omitempty"`
}

// Alert severities.
const (
	AlertInfo    = "info"
	AlertWarning = "warning"
	AlertError   = "error"
)

// MessageReport describes the report in a message.
type MessageReport struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
	URL   string `json:"url,omitempty"`
}

// MessageRun describes the run in a message.
type MessageRun struct {
	ID         string     `json:"id"`
	URL        string     `json:"url,omitempty"`
	Status     string     `json:"status"`
	Rows       int64      `json:"rows"`
	Truncated  bool       `json:"truncated"`
	DurationMS int64      `json:"duration_ms"`
	StartedAt  time.Time  `json:"started_at"`
	ErrorCode  string     `json:"error_code,omitempty"`
	Condition  string     `json:"condition,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Attachment is a file to send. Open returns a new reader each time (a retry reads it again).
type Attachment struct {
	Name        string
	Format      string
	ContentType string
	Size        int64
	Open        func() (io.ReadCloser, error)
}

// Link is a file shared through a Rowbird link.
type Link struct {
	Name      string    `json:"name"`
	Format    string    `json:"format"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SendResult carries what the destination reports back (message ids, object keys, HTTP status).
type SendResult struct {
	Meta map[string]any
}

// Preview is a rendered message for the delivery editor. Body is HTML for HTML destinations and
// text otherwise.
type Preview struct {
	Subject     string   `json:"subject,omitempty"`
	Body        string   `json:"body"`
	BodyType    string   `json:"body_type"`
	Attachments []string `json:"attachments,omitempty"`
	Links       []string `json:"links,omitempty"`
}

// DestinationCapabilities describe a destination to the delivery engine and the UI.
type DestinationCapabilities struct {
	SupportsAttachments bool `json:"supports_attachments"`
	// MaxAttachmentBytes is the largest message the destination accepts with files; larger
	// attachments are sent as links.
	MaxAttachmentBytes int64 `json:"max_attachment_bytes,omitempty"`
	// MaxTextChars bounds the message text (0 = no limit).
	MaxTextChars int `json:"max_text_chars,omitempty"`
	// InlineTarget is the inline formatter family the destination shows rows with ("" = none).
	InlineTarget string `json:"inline_target,omitempty"`
	// SupportsStatus: the destination reports run status (Uptime Kuma).
	SupportsStatus bool `json:"supports_status"`
	// AlwaysNotify: the destination gets every run, also failed and skipped ones.
	AlwaysNotify bool `json:"always_notify"`
	// SupportsAlerts: the destination can carry system alerts (Message.Alert).
	SupportsAlerts bool `json:"supports_alerts"`
	// Modes the destination supports: "inline", "attachment", "link".
	Modes []string `json:"modes"`
}

// Delivery modes.
const (
	ModeInline     = "inline"
	ModeAttachment = "attachment"
	ModeLink       = "link"
)

// Delivery error codes.
const (
	ErrCodeDeliveryAuth        = "delivery.auth_failed"
	ErrCodeDeliveryRejected    = "delivery.rejected"
	ErrCodeDeliveryUnreachable = "delivery.unreachable"
	ErrCodeDeliveryTooLarge    = "delivery.too_large"
	ErrCodeDeliveryRateLimited = "delivery.rate_limited"
	ErrCodeDeliveryTimeout     = "delivery.timeout"
	ErrCodeDeliveryBlocked     = "delivery.network_blocked"
	ErrCodeDeliveryFailed      = "delivery.failed"
)

// DeliveryError is a failed delivery with a stable code. Retry says another attempt may succeed;
// Err keeps the underlying error for logs (scrubbed of secrets before it is stored).
type DeliveryError struct {
	Code  string
	Retry bool
	Err   error
}

func (e *DeliveryError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Err.Error()
	}
	return e.Code
}

func (e *DeliveryError) Unwrap() error { return e.Err }

// ConfiguredCapabilities is implemented by destinations whose capabilities depend on the channel's
// configuration (a Slack incoming webhook cannot attach files; attachment limits may be set).
type ConfiguredCapabilities interface {
	CapabilitiesFor(config map[string]any) DestinationCapabilities
}

// CapabilitiesOf returns a destination's capabilities for a channel configuration.
func CapabilitiesOf(d Destination, config map[string]any) DestinationCapabilities {
	if c, ok := d.(ConfiguredCapabilities); ok {
		return c.CapabilitiesFor(config)
	}
	caps, _ := d.Capabilities().(DestinationCapabilities)
	return caps
}

// DeliveryValidator is implemented by destinations that check delivery options beyond their
// schema (email address lists). It returns the offending option and a validation code.
type DeliveryValidator interface {
	ValidateDelivery(options map[string]any) (field, code string, ok bool)
}
