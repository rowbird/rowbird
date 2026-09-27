import { describe, expect, it } from 'vitest'

import { createAppI18n } from '@/i18n'

import { ApiError, errorMessage, fieldMessage, unwrap } from '../errors'

const { t, te } = createAppI18n('en').global

describe('unwrap', () => {
  it('returns data on success', () => {
    expect(unwrap({ data: { ok: 1 }, response: new Response(null, { status: 200 }) })).toEqual({ ok: 1 })
  })

  it('throws an ApiError built from the problem', () => {
    const problem = { code: 'validation.failed', title: 'Some fields are invalid', status: 400, request_id: 'r1', errors: [{ field: 'email', code: 'validation.email' }] }
    try {
      unwrap({ error: problem, response: new Response(null, { status: 400 }) })
      expect.unreachable()
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError)
      const err = e as ApiError
      expect(err.status).toBe(400)
      expect(err.fields).toEqual({ email: 'validation.email' })
      expect(err.requestId).toBe('r1')
    }
  })

  it('reads Retry-After', () => {
    const res = new Response(null, { status: 429, headers: { 'Retry-After': '42' } })
    try {
      unwrap({ error: { code: 'auth.locked' }, response: res })
    } catch (e) {
      expect((e as ApiError).retryAfter).toBe(42)
      expect(errorMessage(e, t, te)).toBe('Too many failed attempts. Try again in 42 seconds.')
    }
  })
})

describe('messages', () => {
  it('translates known codes, falls back to the server title, then to a generic message', () => {
    expect(errorMessage(new ApiError(401, { code: 'auth.invalid_credentials' }), t, te)).toBe('Incorrect email or password.')
    expect(errorMessage(new ApiError(418, { code: 'teapot.brewing', title: 'Server says hi' }), t, te)).toBe('Server says hi')
    expect(errorMessage(new TypeError('network'), t, te)).toBe(t('errors.internal'))
  })

  it('translates field codes', () => {
    const err = new ApiError(400, { errors: [{ field: 'password', code: 'validation.too_short' }, { field: 'x', code: 'validation.unknown' }] })
    expect(fieldMessage(err, 'password', t, te)).toBe('Too short. Use at least 10 characters.')
    expect(fieldMessage(err, 'x', t, te)).toBe('validation.unknown')
    expect(fieldMessage(err, 'email', t, te)).toBeUndefined()
  })
})
