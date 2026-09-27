<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import AlertTargetFields from '@/components/settings/AlertTargetFields.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'
import { alertOptionSchema, applyDefaults, type ConfigValues, toPayload } from '@/lib/schema'
import { usePluginsStore } from '@/stores/plugins'

/** System alert channels with a fallback, and the outbound heartbeat (docs/spec/03-flows.md §8). */
const { t } = useI18n()
const plugins = usePluginsStore()
const err = useApiError()
const channels = ref<Schemas['Channel'][]>([])
const loaded = ref(false)
const busy = ref(false)
const settings = ref<Schemas['AlertSettings'] | null>(null)

const primaryId = ref('')
const primaryOptions = ref<ConfigValues>({})
const fallbackId = ref('')
const fallbackOptions = ref<ConfigValues>({})
const heartbeatUrl = ref('')
const replacingUrl = ref(false)
const removeUrl = ref(false)
const interval = ref(60)

/** Only channels whose destination can carry alerts (a bucket cannot). */
const alertChannels = computed(() => channels.value.filter((c) => (c.capabilities as { supports_alerts?: boolean }).supports_alerts))

function schemaFor(id: string) {
  const c = channels.value.find((x) => x.id === id)
  const s = c ? plugins.destination(c.type)?.delivery_schema : undefined
  return s ? alertOptionSchema(s) : undefined
}

function fill(s: Schemas['AlertSettings']) {
  settings.value = s
  primaryId.value = s.primary?.channel_id ?? ''
  fallbackId.value = s.fallback?.channel_id ?? ''
  const ps = schemaFor(primaryId.value)
  const fs = schemaFor(fallbackId.value)
  primaryOptions.value = ps ? applyDefaults(ps, s.primary?.options ?? {}) : {}
  fallbackOptions.value = fs ? applyDefaults(fs, s.fallback?.options ?? {}) : {}
  interval.value = s.heartbeat_interval_seconds
  heartbeatUrl.value = ''
  replacingUrl.value = !s.heartbeat_url_configured
  removeUrl.value = false
}

onMounted(async () => {
  try {
    await plugins.load()
    channels.value = unwrap(await api.GET('/api/v1/channels')).items
    fill(unwrap(await api.GET('/api/v1/settings/alerts')))
    loaded.value = true
  } catch (e) {
    err.error.value = e
  }
})

function target(id: string, opts: ConfigValues): Schemas['AlertTarget'] | null {
  if (!id) return null
  const s = schemaFor(id)
  return { channel_id: id, options: s ? toPayload(s, opts) : opts }
}

async function save() {
  busy.value = true
  err.clear()
  const body: Schemas['AlertSettingsInput'] = {
    primary: target(primaryId.value, primaryOptions.value),
    fallback: target(fallbackId.value, fallbackOptions.value),
    heartbeat_interval_seconds: Number(interval.value),
  }
  if (removeUrl.value) body.heartbeat_url = ''
  else if (replacingUrl.value && heartbeatUrl.value.trim() !== '') body.heartbeat_url = heartbeatUrl.value.trim()
  try {
    fill(unwrap(await api.PUT('/api/v1/settings/alerts', { body })))
    toast.success(t('settings.saved'))
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form v-if="loaded" class="grid gap-6" data-testid="alert-settings" @submit.prevent="save">
    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.alerts.title') }}</CardTitle>
        <CardDescription>{{ t('settings.alerts.help') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-6 sm:max-w-xl">
        <p v-if="alertChannels.length === 0" class="text-muted-foreground text-sm">
          {{ t('settings.alerts.noChannels') }}
          <RouterLink to="/channels/new" class="underline underline-offset-2">{{ t('channels.add') }}</RouterLink>
        </p>
        <AlertTargetFields v-model:channel-id="primaryId" v-model:options="primaryOptions" name="primary" :channels="alertChannels" :error-for="err.field" />
        <AlertTargetFields v-model:channel-id="fallbackId" v-model:options="fallbackOptions" name="fallback" :channels="alertChannels" :error-for="err.field" />
        <Alert v-if="settings?.same_destination" data-testid="alert-same-destination">
          <AlertDescription>{{ t('settings.alerts.sameDestination') }}</AlertDescription>
        </Alert>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.heartbeat.title') }}</CardTitle>
        <CardDescription>{{ t('settings.heartbeat.help') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-4 sm:max-w-xl">
        <FormField id="heartbeat-url" :label="t('settings.heartbeat.url')" :hint="t('settings.heartbeat.urlHint')" :error="err.field('heartbeat_url')">
          <div v-if="settings?.heartbeat_url_configured && !replacingUrl" class="flex flex-wrap items-center gap-2 text-sm">
            <span :class="removeUrl ? 'text-muted-foreground line-through' : ''" data-testid="heartbeat-configured">{{ t('settings.heartbeat.configured') }}</span>
            <Button type="button" size="sm" variant="outline" @click="replacingUrl = true; removeUrl = false">{{ t('settings.heartbeat.replace') }}</Button>
            <Button type="button" size="sm" variant="ghost" @click="removeUrl = !removeUrl">{{ removeUrl ? t('common.cancel') : t('settings.heartbeat.remove') }}</Button>
          </div>
          <Input v-else id="heartbeat-url" v-model="heartbeatUrl" type="url" autocomplete="off" placeholder="https://" data-testid="heartbeat-url" />
        </FormField>
        <FormField id="heartbeat-interval" :label="t('settings.heartbeat.interval')" :hint="t('settings.heartbeat.intervalHint')" :error="err.field('heartbeat_interval_seconds')">
          <Input id="heartbeat-interval" v-model.number="interval" type="number" min="30" max="86400" class="max-w-40" />
        </FormField>
      </CardContent>
    </Card>

    <div class="flex items-center gap-3">
      <Button type="submit" :disabled="busy" data-testid="alert-settings-save">{{ t('common.save') }}</Button>
      <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm">{{ err.message.value }}</p>
    </div>
  </form>
  <p v-else-if="err.message.value" class="text-destructive text-sm">{{ err.message.value }}</p>
</template>
