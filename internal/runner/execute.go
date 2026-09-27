package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rowbird/rowbird/internal/apperr"
	"github.com/rowbird/rowbird/internal/condition"
	"github.com/rowbird/rowbird/internal/delivery"
	"github.com/rowbird/rowbird/internal/params"
	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/queries"
	"github.com/rowbird/rowbird/internal/schedule"
	"github.com/rowbird/rowbird/internal/spool"
	"github.com/rowbird/rowbird/internal/store"
)

// SampleRows is how many rows a run keeps for its detail page.
const SampleRows = 100

// permanent lists failures that another attempt cannot fix.
var permanent = []string{
	plugin.ErrCodeAuthFailed, plugin.ErrCodeDatabaseNotFound, plugin.ErrCodeTLSRequired, plugin.ErrCodePathNotAllowed,
	plugin.ErrCodeSSHAuthFailed, plugin.ErrCodeSSHHostKeyUnknown, plugin.ErrCodeSSHHostKeyChanged,
	plugin.ErrCodeMultiStatement, plugin.ErrCodeReadOnlyViolation, plugin.ErrCodeNetworkBlocked,
	CodeInvalidParams, CodeQueryMissing, plugin.ErrCodeConditionColumn, plugin.ErrCodeConditionValue,
}

// Sample is the stored excerpt of a result, encoded like the preview.
type Sample struct {
	Columns []plugin.Column `json:"columns"`
	Rows    [][]any         `json:"rows"`
}

// ResolvedParams is what a run stores about its parameters.
type ResolvedParams struct {
	Timezone string          `json:"timezone"`
	Values   []ResolvedValue `json:"values"`
}

// ResolvedValue is one parameter as the run bound it.
type ResolvedValue struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Value   string `json:"value"`
	Builtin bool   `json:"builtin"`
}

// collector receives the rows: it writes the spool, keeps the sample and the first row.
type collector struct {
	path   string
	w      *spool.Writer
	sample Sample
	first  []any
}

func (c *collector) Columns(cols []plugin.Column) error {
	_ = os.Remove(c.path)
	w, err := spool.Create(c.path, cols)
	if err != nil {
		return err
	}
	c.w, c.sample.Columns, c.sample.Rows = w, cols, [][]any{}
	return nil
}

func (c *collector) Row(row []any) error {
	if c.first == nil {
		c.first = row
	}
	if len(c.sample.Rows) < SampleRows {
		enc := make([]any, len(row))
		for i, v := range row {
			enc[i] = plugin.JSONValue(v)
		}
		c.sample.Rows = append(c.sample.Rows, enc)
	}
	return c.w.Write(row)
}

// close finishes the spool and returns the result hash.
func (c *collector) close() (string, error) {
	if c.w == nil {
		return "", errors.New("runner: the query returned no result set")
	}
	return c.w.Hash(), c.w.Close()
}

func (c *collector) remove() {
	if c.w != nil {
		_ = c.w.Close()
	}
	_ = os.Remove(c.path)
}

// execute processes a claimed run to its end. It never returns an error: every outcome is
// recorded on the run.
func (r *Runner) execute(run *store.Run) {
	r.mu.Lock()
	base := r.execBase
	r.mu.Unlock()
	base = store.WithWorkspace(base, run.WorkspaceID)
	execCtx, cancel := context.WithCancelCause(base)
	r.mu.Lock()
	r.active[run.ID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.active, run.ID)
		r.mu.Unlock()
		cancel(nil)
	}()
	// Store writes must happen even when the execution was cancelled.
	ctx := context.WithoutCancel(execCtx)
	logger := r.logger.With("run_id", run.ID, "report_id", run.ReportID, "attempt", run.Attempt)
	logger.InfoContext(ctx, "run started", "trigger", run.Trigger)
	r.runUpdated(ctx, run)
	start := r.now()
	if run.StartedAt != nil {
		start = *run.StartedAt
	}

	rp, err := r.store.Reports().Get(ctx, run.ReportID)
	if err != nil {
		logger.ErrorContext(ctx, "run has no report", "error", err)
		return
	}
	res := &result{}
	err = r.runQuery(execCtx, run, rp, res)
	if err == nil {
		err = r.evaluate(execCtx, rp, res)
	}
	if cause := context.Cause(execCtx); err != nil && cause != nil {
		err = cause
	}
	kept := r.finish(ctx, execCtx, run, rp, res, err, start, logger)
	if !kept && res.coll != nil {
		res.coll.remove()
	}
}

// result is what a run learned before it finished.
type result struct {
	exec      *queries.ExecResult
	coll      *collector
	hash      string
	version   *store.QueryVersion
	params    *ResolvedParams
	condition *condition.Result
	loc       *time.Location
}

func (r *Runner) runQuery(ctx context.Context, run *store.Run, rp *store.Report, res *result) error {
	q, v, err := r.queries.Current(ctx, rp.QueryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &runError{code: CodeQueryMissing}
		}
		return err
	}
	res.version = v
	loc, err := schedule.LoadLocation(rp.Timezone)
	if err != nil {
		loc = time.UTC
	}
	res.loc = loc
	defs := queries.Definitions(v)
	values := map[string]string{}
	for k, val := range rp.ParamOverrides {
		if slices.ContainsFunc(defs, func(d params.Definition) bool { return d.Name == k }) {
			values[k] = val
		}
	}
	res.coll = &collector{path: SpoolPath(r.cfg.SpoolDir, run.ID)}
	var maxRows int
	if rp.MaxRows != nil {
		maxRows = *rp.MaxRows
	}
	out, err := r.queries.Execute(ctx, queries.ExecInput{
		ConnectionID: q.ConnectionID, SQL: v.SQL, Params: defs, Values: values, Location: loc, MaxRows: maxRows,
	}, res.coll)
	if err != nil {
		if ae, ok := apperr.As(err); ok && ae.Kind == apperr.KindInvalid {
			return &runError{code: CodeInvalidParams, message: fieldList(ae)}
		}
		if _, ok := plugin.AsConnError(err); ok {
			return &runError{code: codeOf(err), message: r.conns.SafeMessage(ctx, q.ConnectionID, err), cause: err}
		}
		return err
	}
	res.exec = out
	res.params = &ResolvedParams{Timezone: loc.String(), Values: make([]ResolvedValue, len(out.Params))}
	for i, p := range out.Params {
		res.params.Values[i] = ResolvedValue{Name: p.Name, Type: string(p.Type), Value: p.Display, Builtin: p.Builtin}
	}
	res.hash, err = res.coll.close()
	return err
}

func (r *Runner) evaluate(ctx context.Context, rp *store.Report, res *result) error {
	var spec condition.Spec
	if err := json.Unmarshal(rp.Condition, &spec); err != nil {
		spec = condition.Always()
	}
	var prev string
	if rp.LastResultHash != nil {
		prev = *rp.LastResultHash
	}
	out, err := condition.Evaluate(ctx, spec, plugin.ConditionInput{
		Columns: res.exec.Columns, FirstRow: res.coll.first, RowCount: res.exec.RowCount, Truncated: res.exec.Truncated,
		ResultHash: res.hash, PreviousHash: prev, Location: res.loc,
	})
	if err != nil {
		var ce *plugin.ConditionError
		if errors.As(err, &ce) {
			detail, _ := json.Marshal(ce.Detail)
			return &runError{code: ce.Code, message: string(detail)}
		}
		return err
	}
	res.condition = out
	return nil
}

// runError is a failure with a stable code and a message safe to show.
type runError struct {
	code, message string
	cause         error
}

func (e *runError) Error() string { return e.code }
func (e *runError) Unwrap() error { return e.cause }

func codeOf(err error) string {
	if ce, ok := plugin.AsConnError(err); ok {
		return ce.Code
	}
	// Errors with a stable code, such as a connection setting the connector refuses to open.
	if ae, ok := apperr.As(err); ok && ae.Code != "" {
		return ae.Code
	}
	return CodeInternal
}

func fieldList(ae *apperr.Error) string {
	parts := make([]string, len(ae.Fields))
	for i, f := range ae.Fields {
		parts[i] = strings.TrimPrefix(f.Field, "values.") + ": " + f.Code
	}
	return strings.Join(parts, ", ")
}

// finish records the outcome: success, skipped, a retry, failed or cancelled.
//
// It reports whether the result's spool is kept for downloads: runs that succeeded or were skipped
// keep it for ResultRetention.
func (r *Runner) finish(ctx, execCtx context.Context, run *store.Run, rp *store.Report, res *result, err error, start time.Time, logger *slog.Logger) bool {
	now := r.clock()
	owner := r.cfg.InstanceID
	if res.version != nil {
		run.QueryVersionID = &res.version.ID
	}
	if res.params != nil {
		run.ResolvedParams, _ = json.Marshal(res.params)
	}
	if res.exec != nil {
		run.RowCount, run.Truncated = &res.exec.RowCount, res.exec.Truncated
		run.ResultSample, _ = json.Marshal(res.coll.sample)
		run.ResultHash = &res.hash
	}
	if res.condition != nil {
		run.ConditionResult, _ = json.Marshal(res.condition)
	}

	switch {
	case errors.Is(err, errCancelRequested):
		run.Status, run.ErrorCode = store.RunCancelled, ptr(CodeCancelled)
	case errors.Is(err, errShutdown):
		run.Status, run.ErrorCode = store.RunCancelled, ptr(CodeShutdown)
	case err != nil:
		code, msg := CodeInternal, err.Error()
		var re *runError
		if errors.As(err, &re) {
			code, msg = re.code, re.message
		}
		if !slices.Contains(permanent, code) && run.Attempt <= rp.RetryMax {
			backoff := min(time.Duration(rp.RetryBackoffSeconds)*time.Second<<(run.Attempt-1), maxBackoff)
			if ok, rerr := r.store.Runs().Release(ctx, run.ID, owner, run.Attempt+1, now.Add(backoff), code, truncate(msg)); rerr != nil || !ok {
				logger.ErrorContext(ctx, "could not queue the run again", "error", rerr, "released", ok)
				return false
			}
			logger.WarnContext(ctx, "run failed; another attempt is queued", "error_code", code, "retry_in", backoff.String())
			run.Status = store.RunPending
			r.runUpdated(ctx, run)
			return false
		}
		run.Status, run.ErrorCode = store.RunFailed, &code
		if msg != "" {
			run.ErrorMessage = ptr(truncate(msg))
		}
	case !res.condition.Passed:
		run.Status, run.ErrorCode = store.RunSkipped, ptr(CodeConditionFalse)
	default:
		run.Status, run.ErrorCode, run.ErrorMessage = store.RunSuccess, nil, nil
	}
	if run.Status != store.RunCancelled {
		r.deliverRun(ctx, execCtx, run, rp, res, logger)
		now = r.clock()
	}
	if run.Status == store.RunCancelled {
		// A cancelled run keeps what it learned but no stale error from an earlier attempt.
		run.ErrorMessage = nil
	}
	run.FinishedAt = &now
	run.DurationMS = ptr(max(now.Sub(start).Milliseconds(), 0))
	keep := res.exec != nil && (run.Status == store.RunSuccess || run.Status == store.RunPartial || run.Status == store.RunSkipped)
	if keep {
		run.ResultExpiresAt = ptr(now.Add(ResultRetention))
	}
	ok, ferr := r.store.Runs().Finish(ctx, run, owner)
	if ferr != nil || !ok {
		logger.ErrorContext(ctx, "could not record the run outcome", "error", ferr, "owned", ok)
		return false
	}
	attrs := []any{"status", run.Status, "duration_ms", *run.DurationMS}
	if run.RowCount != nil {
		attrs = append(attrs, "rows", *run.RowCount)
	}
	if run.ErrorCode != nil {
		attrs = append(attrs, "error_code", *run.ErrorCode)
	}
	logger.InfoContext(ctx, "run finished", attrs...)
	r.metrics.RunFinished(run.Status, run.Trigger, time.Duration(*run.DurationMS)*time.Millisecond, run.RowCount)
	r.runUpdated(ctx, run)
	if err := r.recordOutcome(ctx, run, logger); err != nil {
		logger.ErrorContext(ctx, "could not update the report after the run", "error", err)
	}
	return keep
}

// deliverRun sends the run to its deliveries. A run that succeeded goes to every delivery; a skipped
// or failed one only to destinations that report every run (Uptime Kuma). Manual runs without
// delivery send nothing. Any failed delivery makes a successful run partial.
func (r *Runner) deliverRun(ctx, execCtx context.Context, run *store.Run, rp *store.Report, res *result, logger *slog.Logger) {
	if r.delivery == nil || !run.Deliver {
		return
	}
	status := plugin.StatusUp
	switch run.Status {
	case store.RunSkipped:
		status = plugin.StatusSkipped
	case store.RunFailed:
		status = plugin.StatusDown
	}
	var deliveries []store.Delivery
	if run.Trigger == store.TriggerTest {
		// A test shows what the deliveries look like, whatever the condition says.
		if status == plugin.StatusSkipped {
			status = plugin.StatusUp
		}
		if status != plugin.StatusUp {
			return
		}
		var err error
		if deliveries, err = r.delivery.TestDeliveries(store.WithWorkspace(ctx, run.WorkspaceID), run); err != nil || len(deliveries) == 0 {
			logger.WarnContext(ctx, "test delivery has nowhere to go", "error", err)
			return
		}
	}
	if res.exec != nil {
		run.DurationMS = &res.exec.DurationMS
	}
	loc := res.loc
	if loc == nil {
		loc = time.UTC
	}
	out, err := r.delivery.Deliver(store.WithWorkspace(execCtx, run.WorkspaceID), delivery.Input{
		Run: run, Report: rp, Status: status, Location: loc, Test: run.Trigger == store.TriggerTest, Deliveries: deliveries,
	})
	switch cause := context.Cause(execCtx); {
	case errors.Is(cause, errCancelRequested):
		run.Status, run.ErrorCode = store.RunCancelled, ptr(CodeCancelled)
	case errors.Is(cause, errShutdown):
		run.Status, run.ErrorCode = store.RunCancelled, ptr(CodeShutdown)
	case err != nil:
		logger.ErrorContext(ctx, "deliveries failed", "error", err)
		if run.Status == store.RunSuccess {
			run.Status, run.ErrorCode = store.RunPartial, ptr(CodeDeliveryFailed)
		}
	case out.Failed > 0 && run.Status == store.RunSuccess:
		run.Status, run.ErrorCode = store.RunPartial, ptr(CodeDeliveryFailed)
	}
	if out.Sent+out.Failed > 0 {
		logger.InfoContext(ctx, "deliveries done", "sent", out.Sent, "failed", out.Failed)
	}
}

// recordOutcome updates the report after a run that counts: scheduled runs and manual runs that
// deliver. Failures extend the streak (and may pause the report); success and skips reset it;
// only a success moves last_result_hash (it is the last delivered result). The notifier then
// learns the outcome.
func (r *Runner) recordOutcome(ctx context.Context, run *store.Run, logger *slog.Logger) error {
	counted := run.Trigger == store.TriggerSchedule || (run.Trigger == store.TriggerManual && run.Deliver)
	if !counted || run.Status == store.RunCancelled {
		return nil
	}
	rp, err := r.store.Reports().Get(ctx, run.ReportID)
	if err != nil {
		return err
	}
	pause := false
	switch run.Status {
	case store.RunFailed:
		failures := rp.ConsecutiveFailures + 1
		pause = rp.Enabled && rp.AutoPauseAfter > 0 && failures >= rp.AutoPauseAfter
		err = r.store.Reports().RecordOutcome(ctx, rp.ID, failures, nil, pause)
		if pause && err == nil {
			logger.WarnContext(ctx, "report paused after consecutive failures", "failures", failures)
		}
	case store.RunSuccess, store.RunPartial:
		err = r.store.Reports().RecordOutcome(ctx, rp.ID, 0, run.ResultHash, false)
	default:
		err = r.store.Reports().RecordOutcome(ctx, rp.ID, 0, nil, false)
	}
	if err != nil {
		return err
	}
	if r.notifier != nil {
		r.notifier.RunFinished(store.WithWorkspace(ctx, run.WorkspaceID), run, rp, pause)
	}
	return nil
}

// runUpdated tells the notifier that a run changed status.
func (r *Runner) runUpdated(ctx context.Context, run *store.Run) {
	if r.notifier != nil {
		r.notifier.RunUpdated(store.WithWorkspace(ctx, run.WorkspaceID), run)
	}
}

func truncate(s string) string {
	if len(s) <= maxMessage {
		return s
	}
	cut := maxMessage
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf(" (%d more bytes)", len(s)-cut)
}
