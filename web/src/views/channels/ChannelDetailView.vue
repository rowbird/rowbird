<script setup lang="ts">
import { Mail, Pencil, RotateCw, Send } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import PluginIcon from '@/components/common/PluginIcon.vue'
import ChannelHealth from '@/components/channel/ChannelHealth.vue'
import ChannelTestResult from '@/components/channel/ChannelTestResult.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ManagedNotice from '@/components/config/ManagedNotice.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useFormat } from '@/composables/useFormat'
import type { DestinationCapabilities } from '@/stores/plugins'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const plugins = usePluginsStore()

const id = computed(() => String(route.params.id))
const channel = ref<Schemas['Channel'] | null>(null)
const testResult = ref<Schemas['ChannelTestResult'] | null>(null)
const busy = ref(false)
const deleteOpen = ref(false)
const isAdmin = computed(() => session.hasRole('admin'))
const canRun = computed(() => session.hasRole('editor'))
const caps = computed(() => channel.value?.capabilities as DestinationCapabilities | undefined)

async function load() {
  channel.value = unwrap(await api.GET('/api/v1/channels/{channelId}', { params: { path: { channelId: id.value } } }))
}

onMounted(async () => {
  try {
    await plugins.load()
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

async function runTest() {
  busy.value = true
  try {
    testResult.value = unwrap(await api.POST('/api/v1/channels/{channelId}/test', { params: { path: { channelId: id.value } }, body: {} }))
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}

async function retryFailed() {
  busy.value = true
  try {
    const r = unwrap(await api.POST('/api/v1/channels/{channelId}/retry-failed', { params: { path: { channelId: id.value } }, body: {} }))
    if (r.queued > 0) toast.success(t('channels.retryQueued', { n: r.queued }, r.queued))
    else toast.info(t('channels.retryNothing'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}

async function remove() {
  try {
    unwrap(await api.DELETE('/api/v1/channels/{channelId}', { params: { path: { channelId: id.value } } }))
    toast.success(t('channels.deleted'))
    await router.push('/channels')
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

const typeName = computed(() => {
  const p = channel.value ? plugins.destination(channel.value.type) : undefined
  return p ? t(p.name) : channel.value?.type
})
const megabytes = (n: number) => Math.round(n / 1024 / 1024)
</script>

<template>
  <div v-if="channel" class="grid gap-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="grid gap-1">
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ channel.name }}</h1>
        <div class="text-muted-foreground flex flex-wrap items-center gap-2 text-sm">
          <span class="inline-flex items-center gap-1.5"><PluginIcon :icon="channel ? plugins.destination(channel.type)?.icon : ''" :size="16" />{{ typeName }}</span>
          <ChannelHealth :status="channel.status" :error="channel.last_error" />
          <Badge v-if="channel.is_system_mailer" variant="outline"><Mail class="size-3" aria-hidden="true" />{{ t('channels.systemMailer') }}</Badge>
        </div>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button v-if="canRun" variant="outline" :disabled="busy" data-testid="channel-test" @click="runTest"><Send aria-hidden="true" />{{ t('channels.test.button') }}</Button>
        <Button v-if="isAdmin && channel.managed_by !== 'gitops'" variant="outline" as-child>
          <RouterLink :to="`/channels/${channel.id}/edit`" data-testid="channel-edit"><Pencil aria-hidden="true" />{{ t('channels.edit') }}</RouterLink>
        </Button>
        <Button v-if="isAdmin && channel.managed_by !== 'gitops'" variant="destructive" data-testid="channel-delete" @click="deleteOpen = true">{{ t('channels.delete') }}</Button>
      </div>
    </div>

    <ManagedNotice :id="channel.id" kind="channel" :managed-by="channel.managed_by" @changed="load" />
    <Alert v-if="channel.status === 'failing'" variant="destructive" data-testid="channel-failing">
      <AlertTitle>{{ t('channels.failingTitle', { when: channel.last_failure_at ? fmt.dateTime(channel.last_failure_at) : '' }) }}</AlertTitle>
      <AlertDescription class="grid gap-2">
        <p class="font-mono text-xs break-all">{{ channel.last_error }}</p>
        <div v-if="canRun">
          <Button size="sm" variant="outline" :disabled="busy" data-testid="channel-retry-failed" @click="retryFailed">
            <RotateCw aria-hidden="true" />{{ t('channels.retryFailed') }}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
    <ChannelTestResult v-if="testResult" :result="testResult" />

    <Card>
      <CardHeader><CardTitle as="h2">{{ t('channels.health') }}</CardTitle></CardHeader>
      <CardContent>
        <dl class="grid gap-4 text-sm sm:grid-cols-3">
          <div>
            <dt class="text-muted-foreground">{{ t('channels.lastSuccess') }}</dt>
            <dd data-testid="channel-last-success">{{ channel.last_success_at ? fmt.dateTime(channel.last_success_at) : t('channels.neverUsed') }}</dd>
          </div>
          <div>
            <dt class="text-muted-foreground">{{ t('channels.lastFailure') }}</dt>
            <dd>{{ channel.last_failure_at ? fmt.dateTime(channel.last_failure_at) : t('channels.noFailures') }}</dd>
          </div>
          <div v-if="caps">
            <dt class="text-muted-foreground">{{ t('channels.modes') }}</dt>
            <dd>
              {{ caps.modes.map((m) => t(`deliveries.modes.${m}`)).join(', ') }}
              <span v-if="caps.max_attachment_bytes" class="text-muted-foreground text-xs">
                · {{ t('channels.attachmentLimit', { mb: megabytes(caps.max_attachment_bytes) }) }}
              </span>
            </dd>
          </div>
        </dl>
      </CardContent>
    </Card>

    <Card>
      <CardHeader><CardTitle as="h2">{{ t('channels.usedBy') }}</CardTitle></CardHeader>
      <CardContent>
        <ul v-if="channel.used_by.length" class="grid gap-1 text-sm" data-testid="channel-used-by">
          <li v-for="d in channel.used_by" :key="`${d.type}-${d.id}-${d.name}`">
            <RouterLink v-if="d.type === 'system_alert'" to="/settings/alerts" class="hover:underline" data-testid="channel-used-by-alert">{{ t(`channels.usedByAlert.${d.name}`) }}</RouterLink>
            <RouterLink v-else :to="`/reports/${d.id}`" class="hover:underline">{{ d.name }}</RouterLink>
          </li>
        </ul>
        <p v-else class="text-muted-foreground text-sm">{{ t('channels.unused') }}</p>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="t('channels.delete')"
      :description="channel.used_by.length ? t('channels.deleteInUse', { n: channel.used_by.length }, channel.used_by.length) : t('channels.deleteImpact', { name: channel.name })"
      :confirm-label="t('channels.delete')"
      destructive
      :on-confirm="remove"
    />
  </div>
</template>
