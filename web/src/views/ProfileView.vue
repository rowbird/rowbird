<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ChangePasswordForm from '@/components/account/ChangePasswordForm.vue'
import PasskeysCard from '@/components/account/PasskeysCard.vue'
import PasswordConfirmDialog from '@/components/account/PasswordConfirmDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import RecoveryCodes from '@/components/common/RecoveryCodes.vue'
import TotpEnrollment from '@/components/common/TotpEnrollment.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'
import { useFormat } from '@/composables/useFormat'
import { usePreferenceActions } from '@/composables/usePreferenceActions'
import { type Locale, LOCALE_NAMES, SUPPORTED_LOCALES } from '@/i18n'
import { type Theme, THEMES } from '@/stores/preferences'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const router = useRouter()
const { t, te } = useI18n()
const fmt = useFormat()
const prefActions = usePreferenceActions()

// Profile
const profile = reactive({ name: '', locale: 'en' as Locale, theme: 'system' as Theme })
const profileErr = useApiError()
const savingProfile = ref(false)
watch(
  () => session.me,
  (me) => {
    if (me) Object.assign(profile, { name: me.name, locale: me.locale, theme: me.theme })
  },
  { immediate: true },
)
const localeOptions = SUPPORTED_LOCALES.map((l) => ({ value: l, label: LOCALE_NAMES[l] }))
const themeOptions = THEMES.map((th) => ({ value: th, label: t(`theme.${th}`) }))

async function saveProfile() {
  savingProfile.value = true
  profileErr.clear()
  try {
    await session.updateMe({ name: profile.name, locale: profile.locale, theme: profile.theme })
    prefActions.setLocale(profile.locale)
    prefActions.setTheme(profile.theme)
    toast.success(t('profile.saved'))
  } catch (e) {
    profileErr.error.value = e
  } finally {
    savingProfile.value = false
  }
}

// Two-factor
const enrolling = ref(false)
const disableOpen = ref(false)
const regenerateOpen = ref(false)
const newCodes = ref<string[] | null>(null)

async function enrolled() {
  enrolling.value = false
  await session.refreshMe()
  toast.success(t('twoFactor.enabled'))
}

async function disable2FA(password: string) {
  unwrap(await api.POST('/api/v1/me/2fa/totp/disable', { body: { password } }))
  await session.refreshMe()
  toast.success(t('twoFactor.disabled'))
}

async function regenerate(password: string) {
  newCodes.value = unwrap(await api.POST('/api/v1/me/2fa/recovery-codes', { body: { password } })).codes
}

// Sessions
const sessions = ref<Schemas['Session'][]>([])
const signOutAllOpen = ref(false)

async function loadSessions() {
  try {
    sessions.value = unwrap(await api.GET('/api/v1/me/sessions')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function revoke(id: string) {
  try {
    unwrap(await api.POST('/api/v1/me/sessions/{sessionId}/revoke', { params: { path: { sessionId: id } } }))
    await loadSessions()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function signOutEverywhere() {
  unwrap(await api.POST('/api/v1/me/sessions/revoke-all'))
  session.expire()
  await router.replace({ name: 'login' })
}

onMounted(loadSessions)
</script>

<template>
  <!-- grid-cols-1 keeps long unbroken text (a session's browser) truncated instead of widening the page. -->
  <div class="grid max-w-3xl grid-cols-1 gap-6">
    <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('profile.title') }}</h1>

    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('profile.details') }}</CardTitle>
        <CardDescription>{{ session.me?.email }}</CardDescription>
      </CardHeader>
      <CardContent>
        <form class="grid gap-4" @submit.prevent="saveProfile">
          <FormField id="profile-name" :label="t('fields.name')" :error="profileErr.field('name')">
            <Input id="profile-name" v-model="profile.name" autocomplete="name" required />
          </FormField>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField id="profile-locale" :label="t('fields.language')">
              <NativeSelect id="profile-locale" v-model="profile.locale" :options="localeOptions" />
            </FormField>
            <FormField id="profile-theme" :label="t('theme.label')">
              <NativeSelect id="profile-theme" v-model="profile.theme" :options="themeOptions" />
            </FormField>
          </div>
          <p v-if="profileErr.message.value && !profileErr.hasFieldErrors.value" class="text-destructive text-sm">{{ profileErr.message.value }}</p>
          <div><Button type="submit" :disabled="savingProfile">{{ t('common.save') }}</Button></div>
        </form>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('changePassword.title') }}</CardTitle>
        <CardDescription>{{ t('profile.passwordHelp') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <ChangePasswordForm @changed="toast.success(t('changePassword.done'))" />
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle as="h2" class="flex items-center gap-2">
          {{ t('twoFactor.title') }}
          <Badge :variant="session.me?.totp_enabled ? 'default' : 'secondary'" data-testid="totp-status">
            {{ session.me?.totp_enabled ? t('twoFactor.on') : t('twoFactor.off') }}
          </Badge>
        </CardTitle>
        <CardDescription>{{ t('twoFactor.help') }}</CardDescription>
      </CardHeader>
      <CardContent class="flex flex-wrap gap-2">
        <template v-if="session.me?.totp_enabled">
          <Button variant="outline" @click="regenerateOpen = true">{{ t('twoFactor.regenerate') }}</Button>
          <Button variant="destructive" @click="disableOpen = true">{{ t('twoFactor.disable') }}</Button>
        </template>
        <Button v-else data-testid="totp-start" @click="enrolling = true">{{ t('twoFactor.enable') }}</Button>
      </CardContent>
    </Card>

    <PasskeysCard />

    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('sessions.title') }}</CardTitle>
        <CardDescription>{{ t('sessions.help') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid grid-cols-1 gap-3">
        <ul class="divide-y rounded-md border" data-testid="sessions">
          <li v-for="s in sessions" :key="s.id" class="flex flex-wrap items-center gap-2 p-3 text-sm">
            <div class="min-w-0 flex-1">
              <p class="truncate font-medium">{{ s.user_agent || t('sessions.unknownDevice') }}</p>
              <p class="text-muted-foreground">{{ s.ip }} · {{ t('sessions.lastSeen', { when: fmt.dateTime(s.last_seen_at) }) }}</p>
            </div>
            <Badge v-if="s.current" variant="secondary">{{ t('sessions.current') }}</Badge>
            <Button v-else variant="ghost" size="sm" @click="revoke(s.id)">{{ t('sessions.revoke') }}</Button>
          </li>
        </ul>
        <div><Button variant="outline" @click="signOutAllOpen = true">{{ t('sessions.signOutEverywhere') }}</Button></div>
      </CardContent>
    </Card>

    <Dialog v-model:open="enrolling">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('twoFactor.enable') }}</DialogTitle>
          <DialogDescription>{{ t('twoFactor.help') }}</DialogDescription>
        </DialogHeader>
        <TotpEnrollment v-if="enrolling" @done="enrolled" />
      </DialogContent>
    </Dialog>
    <Dialog :open="newCodes !== null" @update:open="(v) => { if (!v) newCodes = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('twoFactor.regenerate') }}</DialogTitle>
          <DialogDescription>{{ t('twoFactor.codesHelp') }}</DialogDescription>
        </DialogHeader>
        <RecoveryCodes v-if="newCodes" :codes="newCodes" />
      </DialogContent>
    </Dialog>
    <PasswordConfirmDialog
      v-model:open="disableOpen"
      :title="t('twoFactor.disable')"
      :description="t('twoFactor.disableHelp')"
      :confirm-label="t('twoFactor.disable')"
      destructive
      :on-confirm="disable2FA"
    />
    <PasswordConfirmDialog
      v-model:open="regenerateOpen"
      :title="t('twoFactor.regenerate')"
      :description="t('twoFactor.regenerateHelp')"
      :confirm-label="t('twoFactor.regenerate')"
      :on-confirm="regenerate"
    />
    <ConfirmDialog
      v-model:open="signOutAllOpen"
      :title="t('sessions.signOutEverywhere')"
      :description="t('sessions.signOutEverywhereHelp', { count: sessions.length })"
      :confirm-label="t('sessions.signOutEverywhere')"
      destructive
      :on-confirm="signOutEverywhere"
    />
  </div>
</template>
