import { describe, expect, it, vi } from 'vitest'

import { getPasskey } from '@/lib/passkeys'
import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import LoginView from '../LoginView.vue'

vi.mock('@/lib/passkeys', () => ({
  getPasskey: vi.fn(async () => ({ id: 'cred' })),
  createPasskey: vi.fn(),
  passkeysSupported: () => true,
  passkeyCancelled: (e: { name?: string }) => e?.name === 'NotAllowedError',
}))

const withPasskeys = { setup_required: false, token_required: false, master_key: { source: 'env' as const }, passkeys_available: true, oidc_enabled: false, password_reset_available: false }

describe('LoginView', () => {
  it('signs in with a passkey, and ignores a closed prompt', async () => {
    mockApi({
      'POST /api/v1/auth/passkey/options': () => json(200, { challenge_token: 't1', options: { challenge: 'abc' } }),
      'POST /api/v1/auth/passkey': () => json(200, sampleMe),
    })
    const { wrapper, router } = await mountView(LoginView, { path: '/login?redirect=/reports' })
    useSessionStore().setupStatus = withPasskeys
    await flush()
    vi.mocked(getPasskey).mockRejectedValueOnce(Object.assign(new Error('closed'), { name: 'NotAllowedError' }))
    await wrapper.find('[data-testid="passkey-login"]').trigger('click')
    await flush()
    expect(wrapper.find('[data-testid="login-error"]').exists()).toBe(false)

    await wrapper.find('[data-testid="passkey-login"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/reports'))
    wrapper.unmount()
  })

  it('offers the passkey as the second factor', async () => {
    const calls = mockApi({
      'POST /api/v1/auth/login': () => json(200, { status: 'mfa_required', challenge_token: 'c1', methods: ['passkey'] }),
      'POST /api/v1/auth/login/second-factor/passkey': () => json(200, { options: { challenge: 'def' } }),
      'POST /api/v1/auth/login/second-factor': () => json(200, sampleMe),
    })
    const { wrapper, router } = await mountView(LoginView, { path: '/login' })
    useSessionStore().setupStatus = withPasskeys
    await wrapper.find('#email').setValue('ana@example.com')
    await wrapper.find('#password').setValue('secret passphrase')
    await wrapper.find('form').trigger('submit')
    await flush()
    expect(wrapper.text()).toContain('Confirm with the passkey')
    await wrapper.find('[data-testid="passkey-second-factor"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/'))
    expect(await calls[2]!.json()).toEqual({ challenge_token: 'c1', passkey: { id: 'cred' } })
    wrapper.unmount()
  })

  it('offers single sign-on and explains a failed one', async () => {
    mockApi({})
    const { wrapper } = await mountView(LoginView, { locale: 'pt-BR', path: '/login?redirect=/runs&oidc_error=auth.oidc_no_account' })
    useSessionStore().setupStatus = { ...withPasskeys, passkeys_available: false, oidc_enabled: true, password_reset_available: false, oidc_label: 'Entrar com Acme' }
    await flush()
    const sso = wrapper.find('[data-testid="sso-login"]')
    expect(sso.text()).toBe('Entrar com Acme')
    expect(sso.attributes('href')).toBe('/api/v1/auth/oidc/login?redirect=%2Fruns')
    expect(wrapper.find('[data-testid="login-error"]').text()).toContain('Não há conta no Rowbird')
    wrapper.unmount()
  })

  it('signs in with a second factor and follows the redirect', async () => {
    const calls = mockApi({
      'POST /api/v1/auth/login': () => json(200, { status: 'mfa_required', challenge_token: 'c1' }),
      'POST /api/v1/auth/login/second-factor': () => json(200, sampleMe),
    })
    const { wrapper, router } = await mountView(LoginView, { path: '/login?redirect=/reports' })
    await wrapper.find('#email').setValue('ana@example.com')
    await wrapper.find('#password').setValue('secret passphrase')
    await wrapper.find('form').trigger('submit')
    await flush()

    expect(wrapper.text()).toContain('Verify it is you')
    await wrapper.find('#code').setValue('123456')
    await wrapper.find('form').trigger('submit')
    await flush()
    await flush()

    expect(await calls[1]!.json()).toEqual({ challenge_token: 'c1', code: '123456' })
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/reports'))
    wrapper.unmount()
  })

  it('shows translated errors, including the lockout wait', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => json(429, { code: 'auth.locked', status: 429 }, { 'Retry-After': '60' }) })
    const { wrapper } = await mountView(LoginView, { locale: 'pt-BR', path: '/login' })
    await wrapper.find('#email').setValue('ana@example.com')
    await wrapper.find('#password').setValue('x')
    await wrapper.find('form').trigger('submit')
    await flush()
    expect(wrapper.find('[data-testid="login-error"]').text()).toBe('Muitas tentativas sem sucesso. Tente de novo em 60 segundos.')
    wrapper.unmount()
  })

  it('leaves the app for a shared link, which the server serves', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => json(200, { status: 'authenticated', me: sampleMe }) })
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    const { wrapper } = await mountView(LoginView, { path: '/login?redirect=/r/rbl_abc' })
    await wrapper.find('#email').setValue('ana@example.com')
    await wrapper.find('#password').setValue('secret passphrase')
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(assign).toHaveBeenCalledWith('/r/rbl_abc'))
    vi.unstubAllGlobals()
    wrapper.unmount()
  })

  it('ignores an external redirect', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => json(200, { status: 'authenticated', me: sampleMe }) })
    const { wrapper, router } = await mountView(LoginView, { path: '/login?redirect=//evil.example' })
    await wrapper.find('#email').setValue('ana@example.com')
    await wrapper.find('#password').setValue('secret passphrase')
    await wrapper.find('form').trigger('submit')
    await flush()
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/'))
    wrapper.unmount()
  })
})
