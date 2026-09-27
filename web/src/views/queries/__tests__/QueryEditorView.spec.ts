import { describe, expect, it, vi } from 'vitest'

import SqlEditor from '@/components/query/SqlEditor.vue'
import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import QueryEditorView from '../QueryEditorView.vue'

const conn = { id: '01900000-0000-7000-8000-0000000000c1', name: 'shop', driver: 'sqlite', config: {}, query_timeout_seconds: 60, max_rows: 1000, allow_multi_statement: false, ai_excluded_tables: [], status: 'ok', server_version: '', last_error: '', created_at: '', updated_at: '', version: 1 }
const catalog = { plugins: [{ kind: 'connector', id: 'sqlite', name: 'n', description: 'd', icon: '', version: '1', schema: { type: 'object', properties: {}, required: [], 'x-order': [] }, capabilities: { dialect: 'sqlite' } }], messages: {} }
const created = {
  id: '01900000-0000-7000-8000-0000000000q1', title: 'Pedidos grandes', slug: 'pedidos-grandes', description: '', connection_id: conn.id, connection_name: 'shop', driver: 'sqlite',
  current_version: 1, author_name: 'Ana', report_count: 0, updated_at: '', sql: 'select * from orders where total > {{min}}', params: [{ name: 'min', type: 'decimal', default: '100' }],
  current: { number: 1, note: '', created_at: '', author_name: 'Ana' }, created_at: '', version: 1,
}

describe('QueryEditorView', () => {
  it('runs a preview with parameters and creates the query', async () => {
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'GET /api/v1/connections': () => json(200, { items: [conn] }),
      [`GET /api/v1/connections/${conn.id}/schema`]: () => json(200, { tables: [{ name: 'orders', kind: 'table', columns: [{ name: 'total', type: 'decimal', db_type: 'DECIMAL', nullable: true }] }] }),
      'POST /api/v1/queries/preview': () => json(200, { columns: [{ name: 'total', type: 'decimal', db_type: 'DECIMAL' }], rows: [['120.50']], truncated: false, duration_ms: 3, params: [{ name: 'min', type: 'decimal', value: '100', builtin: false }], timezone: 'UTC' }),
      'POST /api/v1/queries': () => json(201, created),
      [`GET /api/v1/queries/${created.id}`]: () => json(200, created),
    })
    const { wrapper, router } = await mountView(QueryEditorView, { path: '/queries/new' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="schema-insert-orders"]').exists()).toBe(true))

    await wrapper.find('[data-testid="query-title"]').setValue('Pedidos grandes')
    expect((wrapper.find('#query-slug').element as HTMLInputElement).value).toBe('pedidos-grandes')
    wrapper.findComponent(SqlEditor).vm.$emit('update:modelValue', 'select * from orders where total > {{min}}')
    await flush()
    await wrapper.find('#param-min-type').setValue('decimal')
    await wrapper.find('#param-min-default').setValue('100')

    await wrapper.find('[data-testid="query-run"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="results"]').exists()).toBe(true))
    const preview = await calls.find((c) => new URL(c.url).pathname === '/api/v1/queries/preview')!.json()
    expect(preview).toMatchObject({ connection_id: conn.id, params: [{ name: 'min', type: 'decimal', default: '100' }] })
    expect(typeof preview.timezone).toBe('string')

    await wrapper.find('[data-testid="query-save"]').trigger('click')
    await vi.waitFor(() => expect(document.querySelector('[data-testid="query-save-confirm"]')).not.toBeNull())
    ;(document.querySelector('[data-testid="query-save-confirm"]') as HTMLButtonElement).click()
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe(`/queries/${created.id}`))
    const body = await calls.find((c) => c.method === 'POST' && new URL(c.url).pathname === '/api/v1/queries')!.json()
    expect(body).toMatchObject({ title: 'Pedidos grandes', slug: 'pedidos-grandes', sql: 'select * from orders where total > {{min}}' })
    wrapper.unmount()
  })

  it('shows translated preview errors', async () => {
    mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'GET /api/v1/connections': () => json(200, { items: [conn] }),
      [`GET /api/v1/connections/${conn.id}/schema`]: () => json(200, { tables: [] }),
      'POST /api/v1/queries/preview': () => json(422, { code: 'query.timeout', status: 422 }),
    })
    const { wrapper } = await mountView(QueryEditorView, { path: '/queries/new', locale: 'pt-BR' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await flush()
    wrapper.findComponent(SqlEditor).vm.$emit('update:modelValue', 'select 1')
    await wrapper.find('[data-testid="query-run"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="preview-error"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="preview-error"]').text()).toBe('A consulta passou do tempo limite.')
    wrapper.unmount()
  })
})

describe('QueryEditorView with the AI assistant', () => {
  const proposal = {
    task: 'query', sql: 'select region, sum(total) from orders where region = {{region}} group by region', explanation: 'Totals per region.',
    suggested_name: 'Sales per region', params: [{ name: 'region', type: 'text', default: null }], schedule: null, warnings: ['ai.warning.schema_truncated'], model: 'm',
  }

  it('opens with Ctrl+I, shows the proposal as a diff and applies it', async () => {
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'GET /api/v1/connections': () => json(200, { items: [conn] }),
      [`GET /api/v1/connections/${conn.id}/schema`]: () => json(200, { tables: [] }),
      'GET /api/v1/ai/status': () => json(200, { enabled: true, provider: 'openai' }),
      'POST /api/v1/ai/generate': () => json(200, proposal),
    })
    const { wrapper } = await mountView(QueryEditorView, { path: '/queries/new' })
    useSessionStore().me = { ...sampleMe, role: 'editor' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="ai-open"]').exists()).toBe(true))
    wrapper.findComponent(SqlEditor).vm.$emit('update:modelValue', 'select * from orders')
    await flush()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'i', ctrlKey: true }))
    await vi.waitFor(() => expect(document.querySelector('[data-testid="ai-prompt"]')).not.toBeNull())
    const prompt = document.querySelector<HTMLTextAreaElement>('[data-testid="ai-prompt"]')!
    prompt.value = 'sales per region'
    prompt.dispatchEvent(new Event('input'))
    await flush()
    // Ctrl+Enter asks for the proposal.
    prompt.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true }))
    await vi.waitFor(() => expect(document.querySelector('[data-testid="ai-proposal"]')).not.toBeNull())
    const body = await calls.find((c) => c.url.endsWith('/ai/generate'))!.json()
    expect(body).toMatchObject({ task: 'query', prompt: 'sales per region', connection_id: conn.id, sql: 'select * from orders' })
    expect(document.querySelector('[data-testid="ai-explanation"]')!.textContent).toBe('Totals per region.')
    expect(document.querySelector('[data-testid="ai-warning"]')!.textContent).toContain('only part of it')
    expect(document.querySelector('[data-testid="ai-diff"]')).not.toBeNull()
    // Nothing changes before Apply.
    expect(wrapper.findComponent(SqlEditor).props('modelValue')).toBe('select * from orders')

    document.querySelector<HTMLButtonElement>('[data-testid="ai-apply"]')!.click()
    await flush()
    expect(wrapper.findComponent(SqlEditor).props('modelValue')).toBe(proposal.sql)
    expect((wrapper.find('[data-testid="query-title"]').element as HTMLInputElement).value).toBe('Sales per region')
    await vi.waitFor(() => expect(wrapper.find('#param-region-type').exists()).toBe(true))
    expect(calls.some((c) => c.url.endsWith('/queries/preview'))).toBe(false)
    wrapper.unmount()
  })

  it('explains how to set the assistant up when it is off', async () => {
    mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'GET /api/v1/connections': () => json(200, { items: [conn] }),
      [`GET /api/v1/connections/${conn.id}/schema`]: () => json(200, { tables: [] }),
      'GET /api/v1/ai/status': () => json(200, { enabled: false, provider: '' }),
    })
    const { wrapper } = await mountView(QueryEditorView, { path: '/queries/new', locale: 'pt-BR' })
    useSessionStore().me = { ...sampleMe, role: 'admin' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="ai-open"]').exists()).toBe(true))
    await wrapper.find('[data-testid="ai-open"]').trigger('click')
    await vi.waitFor(() => expect(document.querySelector('[data-testid="ai-off"]')).not.toBeNull())
    expect(document.querySelector('[data-testid="ai-off"]')!.textContent).toContain('Configurações > IA')
    expect(document.querySelector('[data-testid="ai-prompt"]')).toBeNull()
    wrapper.unmount()
  })
})

describe('QueryEditorView for a GitOps query', () => {
  it('is read only until an admin detaches it', async () => {
    let managed = 'gitops'
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'GET /api/v1/connections': () => json(200, { items: [conn] }),
      [`GET /api/v1/connections/${conn.id}/schema`]: () => json(200, { tables: [] }),
      [`GET /api/v1/queries/${created.id}`]: () => json(200, { ...created, managed_by: managed }),
      'POST /api/v1/gitops/detach': () => {
        managed = 'gitops_detached'
        return new Response(null, { status: 204 })
      },
    })
    const { wrapper } = await mountView(QueryEditorView, { path: `/queries/${created.id}` })
    useSessionStore().me = { ...sampleMe, role: 'admin' }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="managed-notice"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="query-save"]').exists()).toBe(false)
    await wrapper.find('[data-testid="managed-detach"]').trigger('click')
    await vi.waitFor(() => expect(document.querySelector('[data-testid="confirm-action"]')).not.toBeNull())
    document.querySelector<HTMLButtonElement>('[data-testid="confirm-action"]')!.click()
    await vi.waitFor(() => expect(wrapper.find('[data-testid="query-save"]').exists()).toBe(true))
    expect(await calls.find((c) => c.url.endsWith('/gitops/detach'))!.json()).toEqual({ kind: 'query', id: created.id })
    expect(wrapper.find('[data-testid="managed-detached"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
