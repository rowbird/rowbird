<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import PluginIcon from '@/components/common/PluginIcon.vue'
import ChannelTestResult from '@/components/channel/ChannelTestResult.vue'
import FormField from '@/components/common/FormField.vue'
import SchemaForm from '@/components/plugin/SchemaForm.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { useApiError } from '@/composables/useApiError'
import { applyDefaults, type ConfigValues, toPayload } from '@/lib/schema'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()
const plugins = usePluginsStore()
const session = useSessionStore()
const err = useApiError()

const editingId = computed(() => (typeof route.params.id === 'string' ? route.params.id : null))
const existing = ref<Schemas['Channel'] | null>(null)
const type = ref('')
const name = ref('')
const config = ref<ConfigValues>({})
const systemMailer = ref(false)
const testTo = ref('')
const testResult = ref<Schemas['ChannelTestResult'] | null>(null)
const busy = ref(false)
const testing = ref(false)

const plugin = computed(() => (type.value ? plugins.destination(type.value) : undefined))
const isEmail = computed(() => type.value === 'email')

/** Port and TLS mode that do not match hang until the test's timeout, so say so before testing. */
const tlsHint = computed(() => {
  if (!isEmail.value) return ''
  const port = Number(config.value.port ?? 587)
  const mode = String(config.value.tls_mode ?? 'starttls')
  if (port === 465 && mode === 'starttls') return t('channels.tlsHint465')
  if (port === 587 && mode === 'tls') return t('channels.tlsHint587')
  return ''
})

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
  testTo.value = session.me?.email ?? ''
  try {
    await plugins.load()
    if (editingId.value) {
      const c = unwrap(await api.GET('/api/v1/channels/{channelId}', { params: { path: { channelId: editingId.value } } }))
      existing.value = c
      name.value = c.name
      type.value = c.type
      systemMailer.value = c.is_system_mailer
      const p = plugins.destination(c.type)
      config.value = p ? applyDefaults(p.schema, c.config) : c.config
    }
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

function chooseType(id: string) {
  type.value = id
  const p = plugins.destination(id)
  config.value = p ? applyDefaults(p.schema) : {}
  systemMailer.value = false
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
    testResult.value = unwrap(await api.POST('/api/v1/channels/test', {
      body: { type: type.value, config: payload(), channel_id: editingId.value ?? undefined, to: isEmail.value ? testTo.value : undefined },
    }))
  } catch (e) {
    err.error.value = e
    testResult.value = null
  } finally {
    testing.value = false
  }
}

async function save() {
  busy.value = true
  err.clear()
  try {
    let c: Schemas['Channel']
    if (existing.value) {
      c = unwrap(await api.PATCH('/api/v1/channels/{channelId}', {
        params: { path: { channelId: existing.value.id } },
        body: { version: existing.value.version, name: name.value, config: payload(), is_system_mailer: isEmail.value ? systemMailer.value : undefined },
      }))
    } else {
      c = unwrap(await api.POST('/api/v1/channels', {
        body: { name: name.value, type: type.value, config: payload(), is_system_mailer: isEmail.value ? systemMailer.value : undefined },
      }))
    }
    toast.success(t('channels.saved'))
    await router.push(`/channels/${c.id}`)
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="grid max-w-3xl gap-6">
    <h1 class="text-2xl font-semibold" data-testid="page-title">{{ existing ? t('channels.editTitle', { name: existing.name }) : t('channels.add') }}</h1>

    <Card v-if="!existing && !type">
      <CardHeader>
        <CardTitle as="h2">{{ t('channels.chooseType') }}</CardTitle>
        <CardDescription>{{ t('channels.chooseTypeHelp') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-3 sm:grid-cols-2">
        <button
          v-for="p in plugins.destinations"
          :key="p.id"
          type="button"
          class="hover:bg-muted focus-visible:ring-ring/50 rounded-lg border p-4 text-left outline-none focus-visible:ring-[3px]"
          :data-testid="`type-${p.id}`"
          @click="chooseType(p.id)"
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
          <FormField id="channel-name" :label="t('channels.name')" :hint="t('channels.nameHint')" :error="err.field('name')">
            <Input id="channel-name" v-model="name" autocomplete="off" required />
          </FormField>
          <SchemaForm v-model="config" :schema="plugin.schema" :errors="configErrors" />
          <p v-if="tlsHint" role="status" class="text-sm text-amber-700 dark:text-amber-400" data-testid="channel-tls-hint">{{ tlsHint }}</p>
          <div v-if="isEmail" class="flex items-start gap-3">
            <Switch id="channel-system-mailer" v-model="systemMailer" data-testid="channel-system-mailer" />
            <div class="grid gap-1">
              <label for="channel-system-mailer" class="text-sm font-medium">{{ t('channels.systemMailer') }}</label>
              <p class="text-muted-foreground text-xs">{{ t('channels.systemMailerHelp') }}</p>
            </div>
          </div>
        </CardContent>
      </Card>

      <FormField v-if="isEmail" id="channel-test-to" :label="t('channels.test.to')" :hint="t('channels.test.toHint')" :error="err.field('to')">
        <Input id="channel-test-to" v-model="testTo" type="email" autocomplete="off" />
      </FormField>
      <ChannelTestResult v-if="testResult" :result="testResult" />
      <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>

      <div class="flex flex-wrap gap-2">
        <Button type="button" variant="outline" :disabled="testing || busy" data-testid="channel-test" @click="test">
          {{ testing ? t('channels.test.running') : t('channels.test.button') }}
        </Button>
        <Button type="submit" :disabled="busy" data-testid="channel-save">{{ t('common.save') }}</Button>
        <Button v-if="!existing" type="button" variant="ghost" @click="type = ''">{{ t('channels.changeType') }}</Button>
      </div>
    </form>
  </div>
</template>
