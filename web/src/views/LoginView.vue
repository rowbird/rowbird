<script setup lang="ts">
import { KeyRound, LogIn } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { ApiError } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'
import { passkeyCancelled } from '@/lib/passkeys'
import { safeRedirect } from '@/router/guards'
import { type SecondFactorMethod, useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const err = useApiError()

const email = ref('')
const password = ref('')
const code = ref('')
const challenge = ref<string | null>(null)
const methods = ref<SecondFactorMethod[]>([])
const useRecovery = ref(false)
const busy = ref(false)
const hasTotp = computed(() => methods.value.includes('totp'))
const hasPasskey = computed(() => methods.value.includes('passkey') && session.passkeysAvailable)
const ssoEnabled = computed(() => !!session.setupStatus?.oidc_enabled)
const ssoLabel = computed(() => session.setupStatus?.oidc_label || t('login.sso'))
/** The server starts single sign-on; the browser leaves for the provider and comes back signed in. */
const ssoHref = computed(() => `/api/v1/auth/oidc/login?redirect=${encodeURIComponent(safeRedirect(route.query.redirect))}`)

onMounted(() => {
  // The provider's answer came back with a problem: show it in the user's language.
  const code = route.query.oidc_error
  if (typeof code === 'string' && /^auth\.oidc_[a-z_]+$/.test(code)) err.error.value = new ApiError(401, { code })
})

async function finish() {
  const target = safeRedirect(route.query.redirect)
  // Shared links are served by the backend, not by the app's router.
  if (target.startsWith('/r/')) {
    window.location.assign(target)
    return
  }
  await router.replace(target)
}

async function submitPassword() {
  busy.value = true
  err.clear()
  try {
    const outcome = await session.login(email.value, password.value)
    if (outcome.kind === 'mfa') {
      challenge.value = outcome.challengeToken
      methods.value = outcome.methods
      // Without an authenticator app, recovery codes are the only way to type a code.
      useRecovery.value = !outcome.methods.includes('totp')
      password.value = ''
    } else {
      await finish()
    }
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}

/** Runs a passkey step; closing the browser's prompt is not an error. */
async function withPasskey(step: () => Promise<void>) {
  busy.value = true
  err.clear()
  try {
    await step()
    await finish()
  } catch (e) {
    if (!passkeyCancelled(e)) err.error.value = e
    if ((e as { code?: string }).code === 'auth.challenge_expired') challenge.value = null
  } finally {
    busy.value = false
  }
}

const signInWithPasskey = () => withPasskey(() => session.loginWithPasskey())
const secondFactorPasskey = () => withPasskey(() => session.loginSecondFactorPasskey(challenge.value!))

async function submitCode() {
  if (!challenge.value) return
  busy.value = true
  err.clear()
  try {
    await session.loginSecondFactor(challenge.value, code.value)
    await finish()
  } catch (e) {
    err.error.value = e
    // An expired challenge cannot be retried; start over with the password.
    if ((e as { code?: string }).code === 'auth.challenge_expired') challenge.value = null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="w-full max-w-sm">
    <CardHeader>
      <CardTitle as="h1" class="text-xl">{{ challenge ? t('login.secondFactorTitle') : t('login.title') }}</CardTitle>
      <CardDescription>{{ challenge ? (hasPasskey && !hasTotp ? t('login.passkeyHelp') : useRecovery ? t('login.recoveryHelp') : t('login.codeHelp')) : t('app.tagline') }}</CardDescription>
    </CardHeader>
    <CardContent>
      <form v-if="!challenge" class="grid gap-4" @submit.prevent="submitPassword">
        <FormField id="email" :label="t('fields.email')">
          <Input id="email" v-model="email" type="email" autocomplete="username" required autofocus />
        </FormField>
        <FormField id="password" :label="t('fields.password')">
          <Input id="password" v-model="password" type="password" autocomplete="current-password" required />
        </FormField>
        <RouterLink v-if="session.setupStatus?.password_reset_available" to="/forgot-password" class="text-muted-foreground -mt-2 justify-self-end text-sm underline" data-testid="forgot-link">
          {{ t('login.forgot') }}
        </RouterLink>
        <p v-if="err.message.value" class="text-destructive text-sm" role="alert" data-testid="login-error">{{ err.message.value }}</p>
        <Button type="submit" :disabled="busy" data-testid="login-submit">{{ t('login.submit') }}</Button>
        <template v-if="session.passkeysAvailable || ssoEnabled">
          <div class="text-muted-foreground flex items-center gap-2 text-xs"><span class="h-px flex-1 bg-border" />{{ t('login.or') }}<span class="h-px flex-1 bg-border" /></div>
          <Button v-if="session.passkeysAvailable" type="button" variant="outline" :disabled="busy" data-testid="passkey-login" @click="signInWithPasskey">
            <KeyRound aria-hidden="true" />{{ t('login.withPasskey') }}
          </Button>
          <Button v-if="ssoEnabled" as="a" variant="outline" :href="ssoHref" data-testid="sso-login">
            <LogIn aria-hidden="true" />{{ ssoLabel }}
          </Button>
        </template>
      </form>
      <form v-else class="grid gap-4" @submit.prevent="submitCode">
        <Button v-if="hasPasskey" type="button" :variant="hasTotp ? 'outline' : 'default'" :disabled="busy" data-testid="passkey-second-factor" @click="secondFactorPasskey">
          <KeyRound aria-hidden="true" />{{ t('login.usePasskey') }}
        </Button>
        <FormField id="code" :label="useRecovery ? t('login.recoveryCode') : t('twoFactor.code')">
          <Input
            id="code"
            v-model="code"
            :inputmode="useRecovery ? 'text' : 'numeric'"
            autocomplete="one-time-code"
            required
            autofocus
          />
        </FormField>
        <p v-if="err.message.value" class="text-destructive text-sm" role="alert" data-testid="login-error">{{ err.message.value }}</p>
        <Button type="submit" :disabled="busy" data-testid="code-submit">{{ t('login.verify') }}</Button>
        <Button v-if="hasTotp" type="button" variant="link" class="px-0" @click="useRecovery = !useRecovery; code = ''">
          {{ useRecovery ? t('login.useAuthenticator') : t('login.useRecovery') }}
        </Button>
      </form>
    </CardContent>
  </Card>
</template>
