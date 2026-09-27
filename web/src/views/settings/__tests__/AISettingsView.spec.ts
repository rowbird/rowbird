import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import AISettingsView from '../AISettingsView.vue'

const schema = {
  type: 'object', required: ['api_key', 'model'], 'x-order': ['api_key', 'model'],
  properties: {
    api_key: { type: 'string', 'x-secret': true, 'x-label': 'ai.api_key.label' },
    model: { type: 'string', 'x-label': 'ai.model.label', 'x-help': 'plugin.openai.model.help' },
  },
}
const catalog = {
  plugins: [{ kind: 'ai_provider', id: 'openai', name: 'plugin.openai.name', description: 'plugin.openai.description', icon: '', version: '1', schema, capabilities: { structured: 'json_schema', local: false } }],
  messages: { en: { 'plugin.openai.name': 'OpenAI', 'plugin.openai.description': 'GPT models.', 'ai.api_key.label': 'API key', 'ai.model.label': 'Model', 'plugin.openai.model.help': 'For example {{gpt}} or name@example.com' } },
}

describe('AISettingsView', () => {
  it('chooses a provider, tests it and saves the key as configured', async () => {
    let saved = { provider: '', config: {} as Record<string, unknown> }
    const calls = mockApi({
      'GET /api/v1/plugins': () => json(200, catalog),
      'GET /api/v1/settings/ai': () => json(200, saved),
      'POST /api/v1/settings/ai/test': () => json(200, { ok: false, error_code: 'ai.auth_failed', error_message: 'invalid key' }),
      'PUT /api/v1/settings/ai': async (req) => {
        const body = await req.json()
        saved = { provider: body.provider, config: { ...body.config, api_key: { configured: true } } }
        return json(200, saved)
      },
    })
    const { wrapper } = await mountView(AISettingsView, { path: '/settings/ai' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="ai-provider"]').exists()).toBe(true))
    expect(wrapper.text()).toContain('Rows never leave your databases')
    await wrapper.find('[data-testid="ai-provider"]').setValue('openai')
    await wrapper.find('#ai-api_key').setValue('sk-test')
    await wrapper.find('#ai-model').setValue('gpt-test')
    // Plugin help with braces and @ shows as written.
    expect(wrapper.text()).toContain('For example {{gpt}} or name@example.com')

    await wrapper.find('[data-testid="ai-test"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="ai-test-result"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="ai-test-result"]').text()).toContain('refused the API key')

    await wrapper.find('form').trigger('submit')
    await flush()
    const put = await calls.find((c) => c.method === 'PUT')!.json()
    expect(put).toEqual({ provider: 'openai', config: { api_key: 'sk-test', model: 'gpt-test' } })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="secret-api_key"]').exists()).toBe(true))
    wrapper.unmount()
  })
})
