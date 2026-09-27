import { describe, expect, it, vi } from 'vitest'

import { createPasskey } from '@/lib/passkeys'
import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import PasskeysCard from '../PasskeysCard.vue'

vi.mock('@/lib/passkeys', () => ({
  getPasskey: vi.fn(),
  createPasskey: vi.fn(async () => ({ id: 'new-cred' })),
  passkeysSupported: () => true,
  passkeyCancelled: () => false,
}))

const key = { id: '01900000-0000-7000-8000-0000000000aa', name: 'Laptop', created_at: '2026-09-01T12:00:00Z', last_used_at: null, synced: true }

describe('PasskeysCard', () => {
  it('adds a passkey with the password and removes one', async () => {
    let items = [key]
    const calls = mockApi({
      'GET /api/v1/me/passkeys': () => json(200, { items }),
      'GET /api/v1/me': () => json(200, sampleMe),
      'POST /api/v1/me/passkeys/options': () => json(200, { challenge_token: 't1', options: { challenge: 'abc' } }),
      'POST /api/v1/me/passkeys': async (req) => {
        const body = await req.json()
        const added = { ...key, id: '01900000-0000-7000-8000-0000000000bb', name: body.name }
        items = [...items, added]
        return json(201, added)
      },
      [`POST /api/v1/me/passkeys/${key.id}/remove`]: () => new Response(null, { status: 204 }),
    })
    const { wrapper } = await mountView(PasskeysCard, { path: '/profile' })
    const session = useSessionStore()
    session.me = { ...sampleMe }
    session.setupStatus = { setup_required: false, token_required: false, master_key: { source: 'env' }, passkeys_available: true, oidc_enabled: false, password_reset_available: false }
    await vi.waitFor(() => expect(wrapper.findAll('[data-testid="passkey-item"]')).toHaveLength(1))
    expect(wrapper.text()).toContain('Synced')

    await wrapper.find('[data-testid="passkey-add"]').trigger('click')
    await flush()
    await flush()
    const dialog = document.querySelector('[data-testid="add-passkey"]') as HTMLFormElement
    ;(dialog.querySelector('#passkey-name') as HTMLInputElement).value = 'Phone'
    dialog.querySelector('#passkey-name')!.dispatchEvent(new Event('input'))
    ;(dialog.querySelector('#passkey-password') as HTMLInputElement).value = 'secret passphrase'
    dialog.querySelector('#passkey-password')!.dispatchEvent(new Event('input'))
    await flush()
    dialog.dispatchEvent(new Event('submit'))
    await vi.waitFor(() => expect(wrapper.findAll('[data-testid="passkey-item"]')).toHaveLength(2))
    expect(createPasskey).toHaveBeenCalledWith({ challenge: 'abc' })
    expect(await calls.find((c) => c.url.endsWith('/passkeys/options'))!.json()).toEqual({ password: 'secret passphrase' })
    expect(await calls.find((c) => c.method === 'POST' && c.url.endsWith('/me/passkeys'))!.json()).toEqual({ challenge_token: 't1', name: 'Phone', credential: { id: 'new-cred' } })
    wrapper.unmount()
  })

  it('explains when passkeys are unavailable', async () => {
    mockApi({ 'GET /api/v1/me/passkeys': () => json(200, { items: [] }) })
    const { wrapper } = await mountView(PasskeysCard, { path: '/profile' })
    useSessionStore().setupStatus = { setup_required: false, token_required: false, master_key: { source: 'env' }, passkeys_available: false, oidc_enabled: false, password_reset_available: false }
    await flush()
    expect(wrapper.find('[data-testid="passkeys-unavailable"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="passkey-add"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
