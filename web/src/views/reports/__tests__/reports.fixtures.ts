export const queryId = '01900000-0000-7000-8000-0000000000q1'
export const reportId = '01900000-0000-7000-8000-0000000000r1'
export const runId = '01900000-0000-7000-8000-0000000000u1'

export const querySummary = {
  id: queryId, title: 'Big orders', slug: 'big-orders', description: '', connection_id: 'c', connection_name: 'shop', driver: 'sqlite',
  current_version: 2, author_name: 'Ana', report_count: 0, updated_at: '2026-09-25T12:00:00Z',
}
export const query = {
  ...querySummary, sql: 'select * from orders where total >= {{min}} and region = {{region}}',
  params: [{ name: 'min', type: 'decimal', default: '50' }, { name: 'region', type: 'text', default: null }],
  current: { number: 2, note: '', created_at: '', author_name: 'Ana' }, created_at: '', version: 3,
}

export const runSummary = {
  id: runId, report_id: reportId, report_title: 'Daily sales', trigger: 'manual', triggered_by_name: 'Ana', deliver: false,
  status: 'success', scheduled_for: null, started_at: '2026-09-25T12:00:00Z', finished_at: '2026-09-25T12:00:01Z',
  duration_ms: 120, row_count: 150, truncated: false, attempt: 1, error_code: null, created_at: '2026-09-25T12:00:00Z',
}

export const report = {
  id: reportId, title: 'Daily sales', slug: 'daily-sales', description: '', query_id: queryId, query_title: 'Big orders', enabled: true,
  status: 'active', cron: '0 7 * * 1-5', timezone: 'America/Sao_Paulo', next_run_at: '2026-09-28T10:00:00Z', paused_reason: null,
  consecutive_failures: 0, last_run: runSummary, updated_at: '', condition: { match: 'all', rules: [{ type: 'row_count', op: 'gt', value: 0 }] },
  param_overrides: { region: 'south' }, max_rows: null, retry_max: 2, retry_backoff_seconds: 30, misfire_policy: 'run_once',
  overlap_policy: 'skip', auto_pause_after: 5, notify_owner_on_failure: true, created_at: '', version: 1,
}
