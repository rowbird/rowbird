import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, type Schemas } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import { getPasskey, passkeysSupported } from '@/lib/passkeys'

export type Me = Schemas['Me']
export type Role = Schemas['Role']

const roleRank: Record<Role, number> = { viewer: 1, editor: 2, admin: 3 }

export type SecondFactorMethod = 'totp' | 'passkey'
export type LoginOutcome = { kind: 'authenticated' } | { kind: 'mfa'; challengeToken: string; methods: SecondFactorMethod[] }

export const useSessionStore = defineStore('session', () => {
  const me = ref<Me | null>(null)
  const setupRequired = ref(false)
  const setupStatus = ref<Schemas['SetupStatus'] | null>(null)
  const loaded = ref(false)
  let loading: Promise<void> | null = null

  const signedIn = computed(() => me.value !== null)
  const restriction = computed(() => me.value?.restriction ?? 'none')
  /** Passkeys work on this server (it knows its public URL) and in this browser. */
  const passkeysAvailable = computed(() => !!setupStatus.value?.passkeys_available && passkeysSupported())

  function hasRole(role: Role) {
    return me.value !== null && roleRank[me.value.role] >= roleRank[role]
  }

  /** Loads setup status and the current user once; later calls reuse the result. */
  function bootstrap(): Promise<void> {
    if (loaded.value) return Promise.resolve()
    loading ??= (async () => {
      try {
        const status = unwrap(await api.GET('/api/v1/setup/status'))
        setupStatus.value = status
        setupRequired.value = status.setup_required
        if (!status.setup_required) await refreshMe()
      } finally {
        loaded.value = true
        loading = null
      }
    })()
    return loading
  }

  async function refreshMe() {
    const res = await api.GET('/api/v1/me')
    if (res.response.status === 401) {
      me.value = null
      return
    }
    me.value = unwrap(res)
  }

  async function setup(body: Schemas['SetupRequest']) {
    me.value = unwrap(await api.POST('/api/v1/setup', { body }))
    setupRequired.value = false
  }

  async function login(email: string, password: string): Promise<LoginOutcome> {
    const res = unwrap(await api.POST('/api/v1/auth/login', { body: { email, password } }))
    if (res.status === 'mfa_required' && res.challenge_token) {
      return { kind: 'mfa', challengeToken: res.challenge_token, methods: res.methods ?? ['totp'] }
    }
    me.value = res.me ?? null
    return { kind: 'authenticated' }
  }

  async function loginSecondFactor(challengeToken: string, code: string) {
    me.value = unwrap(await api.POST('/api/v1/auth/login/second-factor', { body: { challenge_token: challengeToken, code } }))
  }

  /** Finishes a password sign-in with a passkey instead of a code. */
  async function loginSecondFactorPasskey(challengeToken: string) {
    const { options } = unwrap(await api.POST('/api/v1/auth/login/second-factor/passkey', { body: { challenge_token: challengeToken } }))
    const passkey = await getPasskey(options)
    me.value = unwrap(await api.POST('/api/v1/auth/login/second-factor', { body: { challenge_token: challengeToken, passkey } }))
  }

  /** Signs in with a passkey and no password; the browser offers the passkeys it has for this site. */
  async function loginWithPasskey() {
    const ceremony = unwrap(await api.POST('/api/v1/auth/passkey/options'))
    const credential = await getPasskey(ceremony.options)
    me.value = unwrap(await api.POST('/api/v1/auth/passkey', { body: { challenge_token: ceremony.challenge_token, credential } }))
  }

  async function logout() {
    try {
      unwrap(await api.POST('/api/v1/auth/logout'))
    } catch (e) {
      // An expired session is already signed out.
      if (!(e instanceof ApiError && e.status === 401)) throw e
    } finally {
      me.value = null
    }
  }

  /** Saves profile fields; the version guards against overwriting a change made elsewhere. */
  async function updateMe(patch: Omit<Schemas['MePatch'], 'version'>) {
    if (!me.value) return
    me.value = unwrap(await api.PATCH('/api/v1/me', { body: { ...patch, version: me.value.version } }))
  }

  /** Called when any request answers 401: the session ended elsewhere. */
  function expire() {
    me.value = null
  }

  return {
    me, setupRequired, setupStatus, loaded, signedIn, restriction, passkeysAvailable,
    hasRole, bootstrap, refreshMe, setup, login, loginSecondFactor, loginSecondFactorPasskey, loginWithPasskey, logout, expire, updateMe,
  }
})
