<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useApiError } from '@/composables/useApiError'
import { type Locale, LOCALE_NAMES, SUPPORTED_LOCALES } from '@/i18n'
import { timeZones } from '@/lib/timezones'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const { t } = useI18n()
const err = useApiError()
const form = reactive({ default_locale: 'en' as Locale, default_timezone: 'UTC' })
const loaded = ref(false)
const busy = ref(false)
const canEdit = session.hasRole('admin')
const localeOptions = SUPPORTED_LOCALES.map((l) => ({ value: l, label: LOCALE_NAMES[l] }))
const zoneOptions = timeZones().map((z) => ({ value: z, label: z }))

onMounted(async () => {
  try {
    const s = unwrap(await api.GET('/api/v1/settings'))
    Object.assign(form, { default_locale: s.default_locale, default_timezone: s.default_timezone })
    loaded.value = true
  } catch (e) {
    err.error.value = e
  }
})

async function save() {
  busy.value = true
  err.clear()
  try {
    unwrap(await api.PATCH('/api/v1/settings', { body: { ...form } }))
    toast.success(t('settings.saved'))
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle as="h2">{{ t('settings.general.title') }}</CardTitle>
      <CardDescription>{{ canEdit ? t('settings.general.help') : t('settings.readOnly') }}</CardDescription>
    </CardHeader>
    <CardContent>
      <form v-if="loaded" class="grid gap-4 sm:max-w-md" @submit.prevent="save">
        <FormField id="default-locale" :label="t('settings.general.defaultLocale')" :error="err.field('default_locale')">
          <NativeSelect id="default-locale" v-model="form.default_locale" :options="localeOptions" :disabled="!canEdit" />
        </FormField>
        <FormField id="default-timezone" :label="t('settings.general.defaultTimezone')" :hint="t('settings.general.timezoneHint')" :error="err.field('default_timezone')">
          <NativeSelect id="default-timezone" v-model="form.default_timezone" :options="zoneOptions" :disabled="!canEdit" />
        </FormField>
        <div v-if="canEdit"><Button type="submit" :disabled="busy">{{ t('common.save') }}</Button></div>
      </form>
      <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm">{{ err.message.value }}</p>
    </CardContent>
  </Card>
</template>
