<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import PluginIcon from '@/components/common/PluginIcon.vue'
import FormField from '@/components/common/FormField.vue'
import SchemaForm from '@/components/plugin/SchemaForm.vue'
import TestResult from '@/components/plugin/TestResult.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { useApiError } from '@/composables/useApiError'
import { type ConfigValues, toPayload, applyDefaults } from '@/lib/schema'
import { usePluginsStore } from '@/stores/plugins'

const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()
const plugins = usePluginsStore()
const err = useApiError()

const editingId = computed(() => (typeof route.params.id === 'string' ? route.params.id : null))
const existing = ref<Schemas['Connection'] | null>(null)
const driver = ref<string>('')
const name = ref('')
const config = ref<ConfigValues>({})
const limits = reactive({ query_timeout_seconds: 60, max_rows: 100000, allow_multi_statement: false })
const testResult = ref<Schemas['ConnectionTestResult'] | null>(null)
const busy = ref(false)
const testing = ref(false)

const plugin = computed(() => (driver.value ? plugins.connector(driver.value) : undefined))

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

onMounted(async () => {
  try {
    await plugins.load()
    if (editingId.value) {
      const c = unwrap(await api.GET('/api/v1/connections/{connectionId}', { params: { path: { connectionId: editingId.value } } }))
      existing.value = c
      name.value = c.name
      driver.value = c.driver
      Object.assign(limits, { query_timeout_seconds: c.query_timeout_seconds, max_rows: c.max_rows, allow_multi_statement: c.allow_multi_statement })
      const p = plugins.connector(c.driver)
      config.value = p ? applyDefaults(p.schema, c.config) : c.config
    }
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

function chooseDriver(id: string) {
  driver.value = id
  const p = plugins.connector(id)
  config.value = p ? applyDefaults(p.schema) : {}
  testResult.value = null
  err.clear()
}

function payload() {
  return plugin.value ? toPayload(plugin.value.schema, config.value) : {}
}

async function test() {
  testing.value = true
  err.clear()
  try {
    testResult.value = unwrap(await api.POST('/api/v1/connections/test', {
      body: { driver: driver.value, config: payload(), connection_id: editingId.value ?? undefined },
    }))
  } catch (e) {
    err.error.value = e
    testResult.value = null
  } finally {
    testing.value = false
  }
}

async function trust(fingerprint: string) {
  config.value = { ...config.value, ssh_host_key: fingerprint }
  await test()
}

async function save() {
  busy.value = true
  err.clear()
  try {
    let c: Schemas['Connection']
    if (existing.value) {
      c = unwrap(await api.PATCH('/api/v1/connections/{connectionId}', {
        params: { path: { connectionId: existing.value.id } },
        body: { version: existing.value.version, name: name.value, config: payload(), ...limits },
      }))
    } else {
      c = unwrap(await api.POST('/api/v1/connections', { body: { name: name.value, driver: driver.value, config: payload(), ...limits } }))
    }
    if (c.status === 'error') {
      toast.warning(t('connections.savedWithError', { error: te(`errors.${c.last_error}`) ? t(`errors.${c.last_error}`) : c.last_error }))
    } else {
      toast.success(t('connections.saved'))
    }
    await router.push(`/connections/${c.id}`)
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="grid max-w-3xl gap-6">
    <h1 class="text-2xl font-semibold" data-testid="page-title">{{ existing ? t('connections.editTitle', { name: existing.name }) : t('connections.add') }}</h1>

    <Card v-if="!existing && !driver">
      <CardHeader>
        <CardTitle as="h2">{{ t('connections.chooseDriver') }}</CardTitle>
        <CardDescription>{{ t('connections.chooseDriverHelp') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-3 sm:grid-cols-2">
        <button
          v-for="p in plugins.connectors"
          :key="p.id"
          type="button"
          class="hover:bg-muted focus-visible:ring-ring/50 rounded-lg border p-4 text-left outline-none focus-visible:ring-[3px]"
          :data-testid="`driver-${p.id}`"
          @click="chooseDriver(p.id)"
        >
          <div class="flex items-center gap-2 font-medium"><PluginIcon :icon="p.icon" :size="22" />{{ t(p.name) }}</div>
          <div class="text-muted-foreground mt-1 text-sm">{{ t(p.description) }}</div>
        </button>
      </CardContent>
    </Card>

    <form v-else-if="plugin" class="grid gap-6" novalidate @submit.prevent="save">
      <Card>
        <CardHeader>
          <CardTitle as="h2" class="flex items-center gap-2"><PluginIcon :icon="plugin.icon" />{{ t(plugin.name) }}</CardTitle>
          <CardDescription>{{ t(plugin.description) }}</CardDescription>
        </CardHeader>
        <CardContent class="grid gap-6">
          <FormField id="connection-name" :label="t('connections.name')" :hint="t('connections.nameHint')" :error="err.field('name')">
            <Input id="connection-name" v-model="name" autocomplete="off" required />
          </FormField>
          <SchemaForm v-model="config" :schema="plugin.schema" :errors="configErrors" />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle as="h2">{{ t('connections.limits.title') }}</CardTitle>
          <CardDescription>{{ t('connections.limits.help') }}</CardDescription>
        </CardHeader>
        <CardContent class="grid gap-4 sm:grid-cols-2">
          <FormField id="query-timeout" :label="t('connections.limits.timeout')" :error="err.field('query_timeout_seconds')">
            <Input id="query-timeout" v-model.number="limits.query_timeout_seconds" type="number" min="1" max="3600" />
          </FormField>
          <FormField id="max-rows" :label="t('connections.limits.maxRows')" :error="err.field('max_rows')">
            <Input id="max-rows" v-model.number="limits.max_rows" type="number" min="1" />
          </FormField>
          <div class="grid gap-2 sm:col-span-2">
            <div class="flex items-center gap-3">
              <Switch id="multi" v-model="limits.allow_multi_statement" />
              <label for="multi" class="text-sm font-medium">{{ t('connections.limits.multi') }}</label>
            </div>
            <Alert v-if="limits.allow_multi_statement" variant="destructive">
              <AlertDescription>{{ t('connections.limits.multiWarning') }}</AlertDescription>
            </Alert>
          </div>
        </CardContent>
      </Card>

      <TestResult v-if="testResult" :result="testResult" can-trust @trust="trust" />
      <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>

      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="testing || busy" data-testid="connection-test" @click="test">
          {{ testing ? t('connections.test.running') : t('connections.test.button') }}
        </Button>
        <Button type="submit" :disabled="busy" data-testid="connection-save">{{ t('common.save') }}</Button>
        <Button v-if="!existing" type="button" variant="ghost" @click="driver = ''">{{ t('connections.changeDriver') }}</Button>
      </div>
    </form>
  </div>
</template>
