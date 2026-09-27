import type { RouteLocationNormalized, RouteLocationRaw } from 'vue-router'

import type { Me, Role } from '@/stores/session'

export interface GuardState {
  setupRequired: boolean
  me: Pick<Me, 'role' | 'restriction'> | null
}

const roleRank: Record<Role, number> = { viewer: 1, editor: 2, admin: 3 }

/** Where a restricted session must go, and which routes it may still use there. */
const restrictionRoutes: Record<string, string> = {
  password_change_required: 'change-password',
  mfa_setup_required: 'two-factor-setup',
}

/**
 * Decides where navigation to `to` should go. Returns undefined to allow it. Kept free of Vue
 * so every rule can be tested in a table.
 */
export function resolveNavigation(state: GuardState, to: Pick<RouteLocationNormalized, 'name' | 'meta' | 'fullPath'>): RouteLocationRaw | undefined {
  if (state.setupRequired) {
    return to.name === 'setup' ? undefined : { name: 'setup' }
  }
  if (to.name === 'setup') {
    return state.me ? { name: 'home' } : { name: 'login' }
  }
  if (!state.me) {
    if (to.meta.public) return undefined
    return { name: 'login', query: to.fullPath && to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  }
  const forced = restrictionRoutes[state.me.restriction]
  if (forced) return to.name === forced ? undefined : { name: forced }
  if (to.name === 'change-password' || to.name === 'two-factor-setup' || to.meta.public) {
    return { name: 'home' }
  }
  if (to.meta.role && roleRank[state.me.role] < roleRank[to.meta.role]) {
    return { name: 'home' }
  }
  return undefined
}

/** Only same-site relative paths are accepted as a post-login redirect. */
export function safeRedirect(value: unknown): string {
  return typeof value === 'string' && value.startsWith('/') && !value.startsWith('//') ? value : '/'
}
