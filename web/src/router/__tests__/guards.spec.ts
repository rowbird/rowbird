import { describe, expect, it } from 'vitest'

import { type GuardState, resolveNavigation, safeRedirect } from '../guards'

type To = Parameters<typeof resolveNavigation>[1]
const route = (name: string, meta: To['meta'] = {}, fullPath = `/${name}`): To => ({ name, meta, fullPath })

const anonymous: GuardState = { setupRequired: false, me: null }
const viewer: GuardState = { setupRequired: false, me: { role: 'viewer', restriction: 'none' } }
const admin: GuardState = { setupRequired: false, me: { role: 'admin', restriction: 'none' } }
const mustChange: GuardState = { setupRequired: false, me: { role: 'editor', restriction: 'password_change_required' } }
const mustEnroll: GuardState = { setupRequired: false, me: { role: 'admin', restriction: 'mfa_setup_required' } }

describe('resolveNavigation', () => {
  it.each([
    ['setup pending sends everything to setup', { setupRequired: true, me: null }, route('login', { public: true }), { name: 'setup' }],
    ['setup pending allows setup', { setupRequired: true, me: null }, route('setup', { public: true }), undefined],
    ['setup done hides setup from visitors', anonymous, route('setup', { public: true }), { name: 'login' }],
    ['setup done hides setup from users', viewer, route('setup', { public: true }), { name: 'home' }],
    ['visitors may open login', anonymous, route('login', { public: true }), undefined],
    ['visitors go to login with a redirect', anonymous, route('reports', {}, '/reports?x=1'), { name: 'login', query: { redirect: '/reports?x=1' } }],
    ['visitors from home get no redirect', anonymous, route('home', {}, '/'), { name: 'login', query: {} }],
    ['signed-in users skip login', viewer, route('login', { public: true }), { name: 'home' }],
    ['users reach normal pages', viewer, route('reports'), undefined],
    ['temporary password forces the change page', mustChange, route('reports'), { name: 'change-password' }],
    ['temporary password allows the change page', mustChange, route('change-password'), undefined],
    ['pending 2FA forces enrollment', mustEnroll, route('settings-users'), { name: 'two-factor-setup' }],
    ['pending 2FA allows enrollment', mustEnroll, route('two-factor-setup'), undefined],
    ['forced pages are closed once done', viewer, route('change-password'), { name: 'home' }],
    ['admin pages reject viewers', viewer, route('settings-api-keys', { role: 'admin' }), { name: 'home' }],
    ['admin pages admit admins', admin, route('settings-api-keys', { role: 'admin' }), undefined],
  ] as const)('%s', (_, state, to, want) => {
    expect(resolveNavigation(state as GuardState, to as To)).toEqual(want)
  })
})

describe('safeRedirect', () => {
  it.each([
    ['/reports', '/reports'],
    ['//evil.example', '/'],
    ['https://evil.example', '/'],
    [undefined, '/'],
    [['/a'], '/'],
  ])('%j -> %s', (input, want) => {
    expect(safeRedirect(input)).toBe(want)
  })
})
