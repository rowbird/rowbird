import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppI18n } from '@/i18n'
import { createAppRouter } from '@/router'
import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { deliveryCatalog } from '@/test/catalog'
import { flush } from '@/test/mount'

import DeliveriesSection from '../DeliveriesSection.vue'

const reportId = '01900000-0000-7000-8000-0000000000r1'
const mail = {
  id: '01900000-0000-7000-8000-0000000000c1', name: 'team-mail', type: 'email', config: {}, capabilities: deliveryCatalog.plugins[3]!.capabilities,
  status: 'ok', last_success_at: null, last_failure_at: null, last_error: null, is_system_mailer: true, used_by: [], created_at: '', updated_at: '', version: 1,
}
const hook = { ...mail, id: '01900000-0000-7000-8000-0000000000c2', name: 'erp-hook', type: 'webhook', capabilities: deliveryCatalog.plugins[4]!.capabilities, is_system_mailer: false }
const delivery = {
  id: 'd1', report_id: reportId, channel_id: mail.id, channel_name: 'team-mail', channel_type: 'email', position: 0, enabled: true, mode: 'attachment',
  formats: ['csv'], inline_row_limit: 20, include_inline_with_files: true, link_expires_seconds: 604800, link_require_login: false, options: { to: 'a@example.com' }, version: 1,
}

async function mountSection(routes: Parameters<typeof mockApi>[0]) {
  const calls = mockApi({
    'GET /api/v1/plugins': () => json(200, deliveryCatalog),
    'GET /api/v1/channels': () => json(200, { items: [hook, mail] }),
    'GET /api/v1/settings': () => json(200, { default_locale: 'en', default_timezone: 'UTC', require_2fa: false, links_enabled: false }),
    ...routes,
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  useSessionStore().me = { ...sampleMe, role: 'editor' }
  const router = createAppRouter(createMemoryHistory())
  await router.push(`/reports/${reportId}`)
  const wrapper = mount(DeliveriesSection, { props: { reportId }, global: { plugins: [pinia, createAppI18n('en'), router] }, attachTo: document.body })
  return { wrapper, router, calls }
}

const $ = <T extends Element = HTMLElement>(sel: string) => document.body.querySelector<T>(sel)

describe('DeliveriesSection', () => {
  it('creates a delivery with a live preview and warns that links are disabled', async () => {
    let list: unknown[] = []
    const { wrapper, calls } = await mountSection({
      [`GET /api/v1/reports/${reportId}/deliveries`]: () => json(200, { items: list }),
      'POST /api/v1/deliveries/preview': () => json(200, { subject: 'Daily sales', body: '<p>2 rows</p>', body_type: 'html', attachments: ['daily-sales.csv'], links: [] }),
      [`POST /api/v1/reports/${reportId}/deliveries`]: () => {
        list = [delivery]
        return json(201, delivery)
      },
    })
    await vi.waitFor(() => expect(wrapper.text()).toContain('delivers nowhere yet'))
    await wrapper.find('[data-testid="delivery-add"]').trigger('click')
    await vi.waitFor(() => expect($('[data-testid="delivery-editor"]')).not.toBeNull())
    // The first channel is the webhook; choosing the email channel shows its options.
    const channel = $<HTMLSelectElement>('#delivery-channel')!
    channel.value = mail.id
    channel.dispatchEvent(new Event('change'))
    await flush()

    const mode = $<HTMLSelectElement>('#delivery-mode')!
    mode.value = 'attachment'
    mode.dispatchEvent(new Event('change'))
    await flush()
    $<HTMLButtonElement>('[data-testid="delivery-format-csv"]')!.click()
    const to = $<HTMLInputElement>('#delivery-option-to')!
    to.value = 'a@example.com'
    to.dispatchEvent(new Event('input'))
    await flush()
    // Attachments too large for the destination become links, which need ROWBIRD_BASE_URL.
    expect($('[data-testid="links-disabled"]')).not.toBeNull()

    await vi.waitFor(() => expect($('[data-testid="delivery-preview"]')).not.toBeNull(), { timeout: 2000 })
    expect($<HTMLIFrameElement>('[data-testid="preview-html"]')!.getAttribute('sandbox')).toBe('')
    expect($('[data-testid="delivery-preview"]')!.textContent).toContain('daily-sales.csv')
    const previewBody = await calls.filter((c) => c.url.endsWith('/deliveries/preview')).pop()!.json()
    expect(previewBody).toMatchObject({ report_id: reportId, channel_id: mail.id, mode: 'attachment', formats: ['csv'], options: { to: 'a@example.com' } })

    $<HTMLButtonElement>('[data-testid="delivery-save"]')!.click()
    await vi.waitFor(() => expect(wrapper.find('[data-testid="delivery-team-mail"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="delivery-summary"]').text()).toBe('Attached files: CSV')
    wrapper.unmount()
  })

  it('sends a test to the caller and opens the run', async () => {
    const { wrapper, router, calls } = await mountSection({
      [`GET /api/v1/reports/${reportId}/deliveries`]: () => json(200, { items: [delivery] }),
      [`POST /api/v1/reports/${reportId}/test-delivery`]: () => json(202, { id: 'run-1' }),
    })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="delivery-test-me"]').exists()).toBe(true))
    await wrapper.find('[data-testid="delivery-test-me"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/runs/run-1'))
    expect(await calls.find((c) => c.url.endsWith('/test-delivery'))!.json()).toEqual({})
    wrapper.unmount()
  })
})
