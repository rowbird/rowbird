import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'

import { createAppI18n } from '@/i18n'
import { json, mockApi } from '@/test/api'

import ScheduleBuilder from '../ScheduleBuilder.vue'

function mountBuilder(cron: string, timezone = 'America/Sao_Paulo', locale: 'en' | 'pt-BR' = 'en') {
  const model = ref(cron)
  const tz = ref(timezone)
  const Host = defineComponent({
    setup: () => () => h(ScheduleBuilder, {
      modelValue: model.value, 'onUpdate:modelValue': (v: string) => (model.value = v),
      timezone: tz.value, 'onUpdate:timezone': (v: string) => (tz.value = v),
    }),
  })
  const wrapper = mount(Host, { global: { plugins: [createAppI18n(locale)] } })
  return { wrapper, model, tz }
}

describe('ScheduleBuilder', () => {
  it('builds the expression and shows the server preview', async () => {
    const calls = mockApi({
      'POST /api/v1/schedules/preview': async (req) => {
        const body = await req.json()
        if (body.cron === '0 7 * * 1-5') {
          return json(200, { expression: body.cron, timezone: body.timezone, description: 'Às 07:00, de segunda-feira a sexta-feira', next: ['2026-09-28T10:00:00Z', '2026-09-29T10:00:00Z'] })
        }
        return json(200, { expression: body.cron, timezone: body.timezone, description: 'x', next: [] })
      },
    })
    const { wrapper, model } = mountBuilder('0 8 * * *', 'America/Sao_Paulo', 'pt-BR')
    expect((wrapper.find('#schedule-kind').element as HTMLSelectElement).value).toBe('daily')
    await wrapper.find('#schedule-kind').setValue('weekdays')
    await wrapper.find('#schedule-time').setValue('07:00')
    expect(model.value).toBe('0 7 * * 1-5')
    expect((wrapper.find('#schedule-cron').element as HTMLInputElement).value).toBe('0 7 * * 1-5')

    await vi.waitFor(() => expect(wrapper.find('[data-testid="schedule-description"]').text()).toBe('Às 07:00, de segunda-feira a sexta-feira'))
    expect(wrapper.findAll('[data-testid="schedule-next"] li')).toHaveLength(2)
    const last = await calls[calls.length - 1]!.json()
    expect(last).toEqual({ cron: '0 7 * * 1-5', timezone: 'America/Sao_Paulo', count: 5, locale: 'pt-BR' })

    await wrapper.find('#schedule-kind').setValue('weekly')
    await wrapper.find('#schedule-day-3').trigger('click')
    expect(model.value).toBe('0 7 * * 1,3')
    wrapper.unmount()
  })

  it('switches to custom for expressions it does not know and shows server errors', async () => {
    mockApi({ 'POST /api/v1/schedules/preview': () => json(400, { code: 'validation.failed', status: 400, errors: [{ field: 'cron', code: 'validation.cron' }] }) })
    const { wrapper, model } = mountBuilder('0 7 1-7 * 1')
    expect((wrapper.find('#schedule-kind').element as HTMLSelectElement).value).toBe('custom')
    await wrapper.find('#schedule-cron').setValue('61 * * * *')
    expect(model.value).toBe('61 * * * *')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="schedule-error"]').text()).toContain('valid cron expression'))
    wrapper.unmount()
  })
})

describe('ScheduleBuilder with the AI assistant', () => {
  it('turns a description into a schedule applied only on request', async () => {
    const calls = mockApi({
      'GET /api/v1/ai/status': () => json(200, { enabled: true, provider: 'openai' }),
      'POST /api/v1/schedules/preview': async (req) => {
        const body = await req.json()
        return json(200, { expression: body.cron, timezone: body.timezone, description: body.cron, next: [] })
      },
      'POST /api/v1/ai/generate': () => json(200, {
        task: 'schedule', sql: '', explanation: 'Weekdays at 8 in Lisbon.', suggested_name: '', params: [], warnings: [], model: 'm',
        schedule: { cron: '0 8 * * 1-5', timezone: 'Europe/Lisbon', description: 'At 08:00, Monday through Friday', next: ['2026-09-28T07:00:00Z'] },
      }),
    })
    const { wrapper, model, tz } = mountBuilder('0 8 * * *')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="schedule-assistant"]').exists()).toBe(true))
    await wrapper.find('[data-testid="schedule-ai-text"]').setValue('weekdays at 8, Lisbon time')
    await wrapper.find('[data-testid="schedule-assistant"] form').trigger('submit')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="schedule-ai-description"]').exists()).toBe(true))
    expect(model.value).toBe('0 8 * * *')
    const body = await calls.find((c) => c.url.endsWith('/ai/generate'))!.json()
    expect(body).toEqual({ task: 'schedule', prompt: 'weekdays at 8, Lisbon time', timezone: 'America/Sao_Paulo' })

    await wrapper.find('[data-testid="schedule-ai-apply"]').trigger('click')
    expect(model.value).toBe('0 8 * * 1-5')
    expect(tz.value).toBe('Europe/Lisbon')
    expect(wrapper.find('[data-testid="schedule-ai-proposal"]').exists()).toBe(false)
  })

  it('stays hidden while the assistant is off', async () => {
    mockApi({
      'GET /api/v1/ai/status': () => json(200, { enabled: false, provider: '' }),
      'POST /api/v1/schedules/preview': () => json(200, { expression: '0 8 * * *', timezone: 'UTC', description: '', next: [] }),
    })
    const { wrapper } = mountBuilder('0 8 * * *')
    await flushAll()
    expect(wrapper.find('[data-testid="schedule-assistant"]').exists()).toBe(false)
  })
})

const flushAll = () => new Promise((r) => setTimeout(r, 10))
