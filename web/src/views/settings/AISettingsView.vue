<script setup lang="ts">
import { CircleCheck, CircleX, ShieldCheck } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import SchemaForm from '@/components/plugin/SchemaForm.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useApiError } from '@/composables/useApiError'
import { applyDefaults, type ConfigValues, toPayload } from '@/lib/schema'
import { usePluginsStore } from '@/stores/plugins'

/** The AI assistant's provider (docs/spec/06-ui.md, Settings > AI). Off until an admin picks one. */
const { t, te } = useI18n()
const plugins = usePluginsStore()
const err = useApiError()
const loaded = ref(false)
const busy = ref(false)
const testing = ref(false)
const saved = ref<Schemas['AISettings'] | null>(null)
const provider = ref('')
const config = ref<ConfigValues>({})
const result = ref<Schemas['AITestResult'] | null>(null)

const plugin = computed(() => (provider.value ? plugins.aiProvider(provider.value) : undefined))
const providerOptions = computed(() => [
  { value: '', label: t('settings.ai.off') },
  ...plugins.aiProviders.map((p) => ({ value: p.id, label: t(p.name) })),
])

/** Server field errors for config arrive as "config.<key>". */
const configErrors = computed(() => {
  const out: Record<string, string | undefined> = {}
  if (err.error.value instanceof ApiError) {
    for (const key of Object.keys(err.error.value.fields)) {
      if (key.startsWith('config.')) out[key.slice(7)] = err.field(key)
    }
  }
  return out
})

function fill(s: Schemas['AISettings']) {
  saved.value = s
  provider.value = s.provider
  const p = s.provider ? plugins.aiProvider(s.provider) : undefined
  config.value = p ? applyDefaults(p.schema, s.config) : {}
}

onMounted(async () => {
  try {
    await plugins.load()
    fill(unwrap(await api.GET('/api/v1/settings/ai')))
    loaded.value = true
  } catch (e) {
    err.error.value = e
  }
})

function chooseProvider(id: string) {
  provider.value = id
  result.value = null
  err.clear()
  const p = id ? plugins.aiProvider(id) : undefined
  // Returning to the saved provider keeps its stored key.
  config.value = p ? applyDefaults(p.schema, id === saved.value?.provider ? saved.value.config : {}) : {}
}

function body(): Schemas['AISettingsInput'] {
  return { provider: provider.value, config: plugin.value ? toPayload(plugin.value.schema, config.value) : {} }
}

async function test() {
  testing.value = true
  err.clear()
  try {
    result.value = unwrap(await api.POST('/api/v1/settings/ai/test', { body: body() }))
  } catch (e) {
    err.error.value = e
    result.value = null
  } finally {
    testing.value = false
  }
}

async function save() {
  busy.value = true
  err.clear()
  try {
    fill(unwrap(await api.PUT('/api/v1/settings/ai', { body: body() })))
    toast.success(provider.value ? t('settings.ai.saved') : t('settings.ai.turnedOff'))
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}

const testMessage = (code?: string) => (code && te(`errors.${code}`) ? t(`errors.${code}`) : t('errors.ai.failed'))
</script>

<template>
  <form v-if="loaded" class="grid gap-6" data-testid="ai-settings" novalidate @submit.prevent="save">
    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.ai.title') }}</CardTitle>
        <CardDescription>{{ t('settings.ai.help') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-6 sm:max-w-xl">
        <Alert>
          <ShieldCheck aria-hidden="true" />
          <AlertDescription>{{ t('settings.ai.privacy') }}</AlertDescription>
        </Alert>
        <FormField id="ai-provider" :label="t('settings.ai.provider')" :error="err.field('provider')">
          <NativeSelect id="ai-provider" :model-value="provider" :options="providerOptions" data-testid="ai-provider" @update:model-value="chooseProvider" />
        </FormField>
        <template v-if="plugin">
          <p class="text-muted-foreground text-sm">{{ t(plugin.description) }}</p>
          <SchemaForm v-model="config" :schema="plugin.schema" :errors="configErrors" id-prefix="ai" />
        </template>
      </CardContent>
    </Card>

    <Alert v-if="result?.ok" data-testid="ai-test-result">
      <CircleCheck aria-hidden="true" />
      <AlertTitle>{{ t('settings.ai.testOk') }}</AlertTitle>
    </Alert>
    <Alert v-else-if="result" variant="destructive" data-testid="ai-test-result">
      <CircleX aria-hidden="true" />
      <AlertTitle>{{ t('settings.ai.testFailed') }}</AlertTitle>
      <AlertDescription class="grid gap-1">
        <p>{{ testMessage(result.error_code) }}</p>
        <p v-if="result.error_message" class="font-mono text-xs break-all">{{ result.error_message }}</p>
      </AlertDescription>
    </Alert>
    <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>

    <div class="flex flex-wrap gap-2">
      <Button v-if="plugin" type="button" variant="outline" :disabled="testing || busy" data-testid="ai-test" @click="test">
        {{ testing ? t('settings.ai.testing') : t('settings.ai.test') }}
      </Button>
      <Button type="submit" :disabled="busy" data-testid="ai-save">{{ t('common.save') }}</Button>
    </div>
  </form>
  <p v-else-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
</template>
