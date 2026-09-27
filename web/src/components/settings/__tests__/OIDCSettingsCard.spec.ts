import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import OIDCSettingsCard from '../OIDCSettingsCard.vue'

const saved = {
  enabled: false, issuer: '', client_id: '', scopes: ['openid', 'email', 'profile'], button_label: '', auto_provision: false,
  default_role: 'viewer', allowed_domains: [], client_secret_configured: false, redirect_uri: 'https://rowbird.example.com/api/v1/auth/oidc/callback', available: true,
}

describe('OIDCSettingsCard', () => {
  it('tests the provider and saves the client secret once', async () => {
    let current = { ...saved }
    const calls = mockApi({
      'GET /api/v1/settings/oidc': () => json(200, current),
      'POST /api/v1/settings/oidc/test': () => json(200, { ok: true }),
      'PUT /api/v1/settings/oidc': async (req) => {
        const b = await req.json()
        current = { ...current, ...b, client_secret_configured: !!b.client_secret }
        delete (current as Record<string, unknown>).client_secret
        return json(200, current)
      },
    })
    const { wrapper } = await mountView(OIDCSettingsCard, { path: '/settings/security' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="oidc-issuer"]').exists()).toBe(true))
    expect((wrapper.find('[data-testid="oidc-redirect-uri"]').element as HTMLInputElement).value).toBe(saved.redirect_uri)

    await wrapper.find('[data-testid="oidc-issuer"]').setValue('https://idp.example.com')
    await wrapper.find('[data-testid="oidc-test"]').trigger('click')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="oidc-test-result"]').text()).toBe('The provider answered.'))

    await wrapper.find('[data-testid="oidc-enabled"]').trigger('click')
    await wrapper.find('[data-testid="oidc-client-id"]').setValue('rowbird')
    await wrapper.find('[data-testid="oidc-client-secret"]').setValue('s3cret')
    await wrapper.find('#oidc-domains').setValue('example.com, acme.io')
    await wrapper.find('form').trigger('submit')
    await flush()
    const body = await calls.find((c) => c.method === 'PUT')!.json()
    expect(body).toMatchObject({ enabled: true, issuer: 'https://idp.example.com', client_id: 'rowbird', client_secret: 's3cret', allowed_domains: ['example.com', 'acme.io'] })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="oidc-secret-configured"]').exists()).toBe(true))
    expect(wrapper.html()).not.toContain('s3cret')
    wrapper.unmount()
  })
})
