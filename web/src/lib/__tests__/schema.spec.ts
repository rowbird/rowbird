import { describe, expect, it } from 'vitest'

import { pgSchema } from '@/test/schema'

import { applyDefaults, groups, isVisible, toPayload, unflatten } from '../schema'

describe('schema helpers', () => {
  it('groups fields in first-appearance order', () => {
    expect(groups(pgSchema).map((g) => [g.name, g.fields.length])).toEqual([['connection', 4], ['tls', 2], ['ssh', 3]])
  })

  it('evaluates x-show-if with defaults and chains', () => {
    expect(isVisible(pgSchema, {}, 'tls_ca')).toBe(false)
    expect(isVisible(pgSchema, { tls_mode: 'verify-full' }, 'tls_ca')).toBe(true)
    expect(isVisible(pgSchema, { ssh_enabled: true }, 'ssh_host')).toBe(true)
  })

  it('applies defaults without overwriting values', () => {
    expect(applyDefaults(pgSchema, { port: 6543 })).toMatchObject({ port: 6543, tls_mode: 'disable', ssh_enabled: false })
  })

  it('builds the payload from visible fields and keeps stored-secret placeholders', () => {
    const payload = toPayload(pgSchema, {
      host: 'db', port: 5432, user: '', password: { configured: true }, tls_mode: 'disable', tls_ca: 'hidden', ssh_enabled: false, ssh_host: 'x',
    })
    expect(payload).toEqual({ host: 'db', port: 5432, password: { configured: true }, tls_mode: 'disable', ssh_enabled: false })
    // null clears a stored secret and must reach the server.
    expect(toPayload(pgSchema, { password: null })).toEqual({ password: null })
  })

  it('unflattens translation keys', () => {
    expect(unflatten({ 'a.b.c': '1', 'a.b.d': '2', 'x': '3' })).toEqual({ a: { b: { c: '1', d: '2' } }, x: '3' })
  })
})
