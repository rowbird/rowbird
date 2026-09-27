import { afterEach, describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'

import { api, onUnauthorized, readCookie } from '../client'

describe('api client', () => {
  afterEach(() => {
    document.cookie = 'rowbird_csrf=; max-age=0'
    onUnauthorized(() => {})
  })

  it('reads cookies', () => {
    expect(readCookie('b', 'a=1; b=x%20y; c=3')).toBe('x y')
    expect(readCookie('z', 'a=1')).toBeUndefined()
  })

  it('sends the CSRF token on state-changing requests only', async () => {
    document.cookie = 'rowbird_csrf=token-123'
    const calls = mockApi({
      'GET /api/v1/me': () => json(401, { code: 'auth.unauthenticated' }),
      'POST /api/v1/auth/logout': () => new Response(null, { status: 204 }),
    })
    await api.GET('/api/v1/me')
    await api.POST('/api/v1/auth/logout')
    expect(calls[0]!.headers.get('X-CSRF-Token')).toBeNull()
    expect(calls[1]!.headers.get('X-CSRF-Token')).toBe('token-123')
  })

  it('reports expired sessions but not failed logins', async () => {
    const handler = vi.fn()
    onUnauthorized(handler)
    mockApi({
      'GET /api/v1/me': () => json(401, { code: 'auth.unauthenticated' }),
      'POST /api/v1/auth/login': () => json(401, { code: 'auth.invalid_credentials' }),
    })
    await api.POST('/api/v1/auth/login', { body: { email: 'a', password: 'b' } })
    expect(handler).not.toHaveBeenCalled()
    await api.GET('/api/v1/me')
    expect(handler).toHaveBeenCalledOnce()
  })
})
