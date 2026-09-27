import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { getPasskey } from '@/lib/passkeys'
import { json, mockApi, sampleMe } from '@/test/api'

import { useSessionStore } from '../session'

const status = (required: boolean) => ({ setup_required: required, token_required: false, master_key: { source: 'env' }, passkeys_available: false, oidc_enabled: false, password_reset_available: false })

vi.mock('@/lib/passkeys', () => ({
  getPasskey: vi.fn(async () => ({ id: 'cred' })),
  createPasskey: vi.fn(),
  passkeysSupported: () => true,
  passkeyCancelled: () => false,
}))

describe('session store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('stops at setup when it is pending', async () => {
    const calls = mockApi({ 'GET /api/v1/setup/status': () => json(200, status(true)) })
    const s = useSessionStore()
    await s.bootstrap()
    expect(s.setupRequired).toBe(true)
    expect(s.me).toBeNull()
    expect(calls).toHaveLength(1)
  })

  it('loads the current user once', async () => {
    const calls = mockApi({
      'GET /api/v1/setup/status': () => json(200, status(false)),
      'GET /api/v1/me': () => json(200, sampleMe),
    })
    const s = useSessionStore()
    await Promise.all([s.bootstrap(), s.bootstrap()])
    await s.bootstrap()
    expect(s.me?.email).toBe('ana@example.com')
    expect(s.hasRole('editor')).toBe(true)
    expect(calls).toHaveLength(2)
  })

  it('treats 401 on /me as signed out', async () => {
    mockApi({
      'GET /api/v1/setup/status': () => json(200, status(false)),
      'GET /api/v1/me': () => json(401, { code: 'auth.unauthenticated' }),
    })
    const s = useSessionStore()
    await s.bootstrap()
    expect(s.signedIn).toBe(false)
  })

  it('returns the challenge when a second factor is needed', async () => {
    mockApi({ 'POST /api/v1/auth/login': () => json(200, { status: 'mfa_required', challenge_token: 'c1', methods: ['passkey'] }) })
    const s = useSessionStore()
    expect(await s.login('a@example.com', 'pw')).toEqual({ kind: 'mfa', challengeToken: 'c1', methods: ['passkey'] })
    expect(s.me).toBeNull()
  })

  it('signs in with a passkey, and finishes a password sign-in with one', async () => {
    const calls = mockApi({
      'POST /api/v1/auth/passkey/options': () => json(200, { challenge_token: 't1', options: { challenge: 'abc' } }),
      'POST /api/v1/auth/passkey': () => json(200, sampleMe),
      'POST /api/v1/auth/login/second-factor/passkey': () => json(200, { options: { challenge: 'def' } }),
      'POST /api/v1/auth/login/second-factor': () => json(200, sampleMe),
    })
    const s = useSessionStore()
    await s.loginWithPasskey()
    expect(getPasskey).toHaveBeenCalledWith({ challenge: 'abc' })
    expect(await calls.find((c) => c.url.endsWith('/auth/passkey'))!.json()).toEqual({ challenge_token: 't1', credential: { id: 'cred' } })
    expect(s.me?.email).toBe(sampleMe.email)

    s.me = null
    await s.loginSecondFactorPasskey('c1')
    expect(await calls.find((c) => c.url.endsWith('/second-factor'))!.json()).toEqual({ challenge_token: 'c1', passkey: { id: 'cred' } })
    expect(s.me).not.toBeNull()
  })

  it('signs out locally even when the session already expired', async () => {
    mockApi({ 'POST /api/v1/auth/logout': () => json(401, { code: 'auth.unauthenticated' }) })
    const s = useSessionStore()
    s.me = { ...sampleMe }
    await s.logout()
    expect(s.me).toBeNull()
  })

  it('sends the version when updating the profile', async () => {
    const calls = mockApi({ 'PATCH /api/v1/me': () => json(200, { ...sampleMe, name: 'Bia', version: 2 }) })
    const s = useSessionStore()
    s.me = { ...sampleMe }
    await s.updateMe({ name: 'Bia' })
    expect(await calls[0]!.json()).toEqual({ name: 'Bia', version: 1 })
    expect(s.me?.version).toBe(2)
  })
})
