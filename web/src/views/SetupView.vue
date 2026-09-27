<script setup lang="ts">
import { KeyRound } from '@lucide/vue'
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useApiError } from '@/composables/useApiError'
import { type Locale, LOCALE_NAMES, SUPPORTED_LOCALES } from '@/i18n'
import { browserTimeZone, timeZones } from '@/lib/timezones'
import { usePreferencesStore } from '@/stores/preferences'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const prefs = usePreferencesStore()
const router = useRouter()
const { t } = useI18n()
const err = useApiError()

const form = reactive({
  name: '',
  email: '',
  password: '',
  confirm: '',
  locale: prefs.locale as Locale,
  timezone: browserTimeZone(),
  token: '',
})
const backedUp = ref(false)
const busy = ref(false)
const mismatch = ref(false)

const status = computed(() => session.setupStatus)
const generatedKey = computed(() => status.value?.master_key.source === 'generated')
const canSubmit = computed(() => !busy.value && (!generatedKey.value || backedUp.value))
const localeOptions = SUPPORTED_LOCALES.map((l) => ({ value: l, label: LOCALE_NAMES[l] }))
const zoneOptions = timeZones().map((z) => ({ value: z, label: z }))

async function submit() {
  mismatch.value = form.password !== form.confirm
  if (mismatch.value || !canSubmit.value) return
  busy.value = true
  err.clear()
  try {
    await session.setup({
      name: form.name,
      email: form.email,
      password: form.password,
      locale: form.locale,
      timezone: form.timezone,
      token: status.value?.token_required ? form.token : undefined,
    })
    prefs.setLocale(form.locale)
    await router.replace({ name: 'home' })
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card class="w-full max-w-lg">
    <CardHeader>
      <CardTitle as="h1" class="text-xl">{{ t('setup.title') }}</CardTitle>
      <CardDescription>{{ t('setup.intro') }}</CardDescription>
    </CardHeader>
    <CardContent>
      <form class="grid gap-4" novalidate @submit.prevent="submit">
        <Alert v-if="generatedKey" variant="destructive" data-testid="master-key-warning">
          <KeyRound aria-hidden="true" />
          <AlertTitle>{{ t('setup.masterKey.title') }}</AlertTitle>
          <AlertDescription class="grid gap-2">
            <p>{{ t('setup.masterKey.body') }}</p>
            <code v-if="status?.master_key.path" class="font-mono text-xs break-all">{{ status.master_key.path }}</code>
            <div class="mt-1 flex items-center gap-2">
              <Checkbox id="backed-up" v-model="backedUp" data-testid="master-key-ack" />
              <Label for="backed-up" class="font-normal">{{ t('setup.masterKey.ack') }}</Label>
            </div>
          </AlertDescription>
        </Alert>

        <FormField v-if="status?.token_required" id="token" :label="t('setup.token')" :hint="t('setup.tokenHint')">
          <Input id="token" v-model="form.token" autocomplete="off" required />
        </FormField>
        <FormField id="name" :label="t('fields.name')" :error="err.field('name')">
          <Input id="name" v-model="form.name" autocomplete="name" required />
        </FormField>
        <FormField id="email" :label="t('fields.email')" :error="err.field('email')">
          <Input id="email" v-model="form.email" type="email" autocomplete="email" required />
        </FormField>
        <FormField id="password" :label="t('fields.password')" :hint="t('fields.passwordHint')" :error="err.field('password')">
          <Input id="password" v-model="form.password" type="password" autocomplete="new-password" required />
        </FormField>
        <FormField id="confirm" :label="t('fields.confirmPassword')" :error="mismatch ? t('fields.passwordMismatch') : undefined">
          <Input id="confirm" v-model="form.confirm" type="password" autocomplete="new-password" required />
        </FormField>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField id="locale" :label="t('fields.language')" :error="err.field('locale')">
            <NativeSelect id="locale" v-model="form.locale" :options="localeOptions" />
          </FormField>
          <FormField id="timezone" :label="t('fields.timezone')" :error="err.field('timezone')">
            <NativeSelect id="timezone" v-model="form.timezone" :options="zoneOptions" />
          </FormField>
        </div>
        <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
        <Button type="submit" :disabled="!canSubmit" data-testid="setup-submit">{{ t('setup.submit') }}</Button>
      </form>
    </CardContent>
  </Card>
</template>
