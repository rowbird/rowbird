// Package destination holds what destination plugins share: template variables built from a
// message, the default texts, and mapping HTTP and network failures to delivery error codes.
// Destinations live in its subpackages (docs/spec/04-plugins.md, "Destinations").
package destination

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rowbird/rowbird/internal/format"
	"github.com/rowbird/rowbird/internal/msgtemplate"
	"github.com/rowbird/rowbird/internal/netx"
	"github.com/rowbird/rowbird/internal/plugin"
)

//go:embed locales/*.json
var catalogFS embed.FS

var catalog = plugin.MustLoadMessages(catalogFS)

// Messages returns the texts shared by destinations (run statuses, notes), for plugins to merge
// into theirs.
func Messages() plugin.Messages { return catalog }

// T returns a shared text in locale, English when missing, with {name} placeholders replaced.
func T(locale, key string, args map[string]any) string {
	return TFrom(catalog, locale, key, args)
}

// TFrom is T over another catalog (a plugin's own texts).
func TFrom(msgs plugin.Messages, locale, key string, args map[string]any) string {
	s, ok := msgs[locale][key]
	if !ok {
		s, ok = msgs["en"][key]
	}
	if !ok {
		return key
	}
	for k, v := range args {
		s = strings.ReplaceAll(s, "{"+k+"}", fmt.Sprint(v))
	}
	return s
}

// Vars builds the template variables of a message, formatted for its language.
func Vars(msg plugin.Message) msgtemplate.Vars {
	l := format.GetLocale(msg.Locale)
	loc := msg.Location
	if loc == nil {
		loc = time.UTC
	}
	started := msg.Run.StartedAt.In(loc)
	v := msgtemplate.Vars{
		ReportTitle: msg.Report.Title, ReportSlug: msg.Report.Slug, ReportURL: msg.Report.URL,
		RunID: msg.Run.ID, RunURL: msg.Run.URL, RunStatus: T(msg.Locale, "destination.status."+statusKey(msg), nil),
		RunRows: l.Number(strconv.FormatInt(msg.Run.Rows, 10), true), RunTruncated: msg.Run.Truncated,
		RunDuration: Duration(msg.Locale, msg.Run.DurationMS), RunStarted: l.FormatDateTime(started.Truncate(time.Minute)),
		RunDate: started.Format(time.DateOnly), ConditionSummary: msg.Run.Condition,
	}
	var formats []string
	for _, a := range msg.Attachments {
		formats = append(formats, a.Format)
	}
	for _, k := range msg.Links {
		formats = append(formats, k.Format)
	}
	v.Format = strings.Join(formats, ", ")
	if len(msg.Links) > 0 {
		v.LinkURL = msg.Links[0].URL
		v.LinkExpiresAt = l.FormatDateTime(msg.Links[0].ExpiresAt.In(loc).Truncate(time.Minute))
	}
	return v
}

func statusKey(msg plugin.Message) string {
	switch {
	case msg.Test:
		return "test"
	case msg.Status == plugin.StatusDown:
		return "failed"
	case msg.Status == plugin.StatusSkipped:
		return "skipped"
	}
	return "success"
}

// AlertView is the label of the link in a system alert.
func AlertView(locale string) string { return T(locale, "destination.alertView", nil) }

// Duration reads a run's duration: "850 ms", "1.2 s", "3 min 5 s".
func Duration(locale string, ms int64) string {
	l := format.GetLocale(locale)
	switch {
	case ms < 1000:
		return strconv.FormatInt(ms, 10) + " ms"
	case ms < 60_000:
		return l.Number(strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64), false) + " s"
	}
	return fmt.Sprintf("%d min %d s", ms/60_000, (ms%60_000)/1000)
}

// Render renders an option template, or the default text key of msgs when the option is empty.
func Render(msgs plugin.Messages, opts map[string]any, option, defaultKey string, msg plugin.Message, esc msgtemplate.Escape) (string, error) {
	src, _ := opts[option].(string)
	if strings.TrimSpace(src) == "" {
		src = TFrom(msgs, msg.Locale, defaultKey, nil)
	}
	return msgtemplate.Render(src, Vars(msg), esc)
}

// Err builds a delivery error.
func Err(code string, retry bool, err error) *plugin.DeliveryError {
	return &plugin.DeliveryError{Code: code, Retry: retry, Err: err}
}

// NetworkError maps a failed request: blocked by the network policy, timed out or unreachable.
func NetworkError(err error) *plugin.DeliveryError {
	var ne net.Error
	switch {
	case errors.Is(err, netx.ErrBlocked):
		return Err(plugin.ErrCodeDeliveryBlocked, false, err)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return Err(plugin.ErrCodeDeliveryTimeout, true, err)
	case errors.Is(err, context.Canceled):
		return Err(plugin.ErrCodeDeliveryFailed, false, err)
	}
	return Err(plugin.ErrCodeDeliveryUnreachable, true, err)
}

// StatusError maps an unsuccessful HTTP response; body is a short excerpt for the log.
func StatusError(status int, body string) *plugin.DeliveryError {
	err := fmt.Errorf("HTTP %d: %s", status, body)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return Err(plugin.ErrCodeDeliveryAuth, false, err)
	case status == http.StatusRequestEntityTooLarge:
		return Err(plugin.ErrCodeDeliveryTooLarge, false, err)
	case status == http.StatusTooManyRequests:
		return Err(plugin.ErrCodeDeliveryRateLimited, true, err)
	case status >= 500:
		return Err(plugin.ErrCodeDeliveryFailed, true, err)
	}
	return Err(plugin.ErrCodeDeliveryRejected, false, err)
}

// Do sends req and returns the response body, mapping failures to delivery errors. At most 1 MB of
// the body is read.
func Do(client *http.Client, req *http.Request) ([]byte, int, error) {
	// The URL is the channel's configuration; the client dials through the network policy.
	res, err := client.Do(req) //nolint:gosec // see above
	if err != nil {
		return nil, 0, NetworkError(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, res.StatusCode, NetworkError(err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return body, res.StatusCode, StatusError(res.StatusCode, excerpt(body))
	}
	return body, res.StatusCode, nil
}

func excerpt(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// FitText cuts a message to limit characters, keeping whole lines, and says whether it cut.
func FitText(s string, limit int) (string, bool) {
	r := []rune(s)
	if limit <= 0 || len(r) <= limit {
		return s, false
	}
	cut := string(r[:limit])
	if i := strings.LastIndex(cut, "\n"); i > limit/2 {
		cut = cut[:i]
	}
	return cut, true
}

// Compose joins the parts of a chat message (text, inline result, links, notes) under a limit.
// When everything does not fit, the inline result goes first, then the text is cut.
func Compose(limit int, text, inline string, tail ...string) (string, bool) {
	join := func(parts ...string) string {
		var out []string
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				out = append(out, strings.TrimRight(p, "\n"))
			}
		}
		return strings.Join(out, "\n\n")
	}
	all := join(append([]string{text, inline}, tail...)...)
	if limit <= 0 || len([]rune(all)) <= limit {
		return all, false
	}
	without := join(append([]string{text}, tail...)...)
	if len([]rune(without)) <= limit {
		return without, true
	}
	return FitText(without, limit)
}

// LinkLines lists the links of a message, one per line, with render turning a name and a URL
// into the destination's markup.
func LinkLines(msg plugin.Message, render func(name, url string) string) string {
	if len(msg.Links) == 0 {
		return ""
	}
	lines := []string{T(msg.Locale, "destination.files", nil) + ":"}
	for _, l := range msg.Links {
		lines = append(lines, render(l.Name, l.URL))
	}
	loc := msg.Location
	if loc == nil {
		loc = time.UTC
	}
	lines = append(lines, T(msg.Locale, "destination.expires", map[string]any{"time": format.GetLocale(msg.Locale).FormatDateTime(msg.Links[0].ExpiresAt.In(loc).Truncate(time.Minute))}))
	if msg.FallbackNote {
		lines = append(lines, T(msg.Locale, "destination.fallback", nil))
	}
	return strings.Join(lines, "\n")
}
