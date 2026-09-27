<script setup lang="ts">
import { Megaphone, Pencil, Plus, Send, Trash2 } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ChannelHealth from '@/components/channel/ChannelHealth.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import DeliveryEditor from '@/components/delivery/DeliveryEditor.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

/** A report's deliveries as cards, with the editor and the test sends. */
const props = defineProps<{ reportId: string; readonly?: boolean }>()
const { t, te } = useI18n()
const router = useRouter()
const plugins = usePluginsStore()
const session = useSessionStore()

const deliveries = ref<Schemas['Delivery'][]>([])
const channels = ref<Schemas['Channel'][]>([])
const linksEnabled = ref(true)
const loaded = ref(false)
const editorOpen = ref(false)
const editing = ref<Schemas['Delivery'] | null>(null)
const removing = ref<Schemas['Delivery'] | null>(null)
const deleteOpen = ref(false)
const sending = ref(false)
const canEdit = computed(() => session.hasRole('editor') && !props.readonly)

const channelOf = (d: Schemas['Delivery']) => channels.value.find((c) => c.id === d.channel_id)

async function load() {
  deliveries.value = unwrap(await api.GET('/api/v1/reports/{reportId}/deliveries', { params: { path: { reportId: props.reportId } } })).items
}

onMounted(async () => {
  try {
    await plugins.load()
    const [ch, settings] = await Promise.all([api.GET('/api/v1/channels'), api.GET('/api/v1/settings')])
    channels.value = unwrap(ch).items
    linksEnabled.value = unwrap(settings).links_enabled
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
})

function edit(d: Schemas['Delivery'] | null) {
  editing.value = d
  editorOpen.value = true
}

function formatName(id: string) {
  const p = plugins.plugins.find((x) => x.kind === 'formatter' && x.id === id)
  return p ? t(p.name) : id
}

function summary(d: Schemas['Delivery']) {
  const mode = t(`deliveries.modes.${d.mode}`)
  return d.mode === 'inline' ? mode : `${mode}: ${d.formats.map(formatName).join(', ')}`
}

function confirmRemove(d: Schemas['Delivery']) {
  removing.value = d
  deleteOpen.value = true
}

async function remove() {
  const d = removing.value
  if (!d) return
  try {
    unwrap(await api.DELETE('/api/v1/reports/{reportId}/deliveries/{deliveryId}', { params: { path: { reportId: props.reportId, deliveryId: d.id } } }))
    toast.success(t('deliveries.deleted'))
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

/** Runs the report now and sends it only to the caller (no channel) or to one channel. */
async function sendTest(channelId?: string) {
  sending.value = true
  try {
    const run = unwrap(await api.POST('/api/v1/reports/{reportId}/test-delivery', {
      params: { path: { reportId: props.reportId } },
      body: channelId ? { channel_id: channelId } : {},
    }))
    toast.success(channelId ? t('deliveries.testQueuedChannel') : t('deliveries.testQueued', { email: session.me?.email ?? '' }))
    await router.push(`/runs/${run.id}`)
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <section class="grid gap-3" data-testid="report-deliveries">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h2 class="text-lg font-semibold">{{ t('deliveries.title') }}</h2>
        <p class="text-muted-foreground text-sm">{{ t('deliveries.help') }}</p>
      </div>
      <div v-if="canEdit && deliveries.length > 0" class="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" :disabled="sending" data-testid="delivery-test-me" @click="sendTest()">
          <Send aria-hidden="true" />{{ t('deliveries.testMe') }}
        </Button>
        <Button size="sm" :disabled="channels.length === 0" data-testid="delivery-add" @click="edit(null)"><Plus aria-hidden="true" />{{ t('deliveries.add') }}</Button>
      </div>
    </div>

    <EmptyState v-if="loaded && deliveries.length === 0" :icon="Megaphone" :title="t('deliveries.emptyTitle')" :description="t('deliveries.emptyBody')">
      <template v-if="canEdit">
        <Button v-if="channels.length > 0" data-testid="delivery-add" @click="edit(null)"><Plus aria-hidden="true" />{{ t('deliveries.add') }}</Button>
        <p v-else class="text-muted-foreground text-sm">
          {{ t('deliveries.needChannel') }}
          <RouterLink to="/channels/new" class="underline">{{ t('channels.add') }}</RouterLink>
        </p>
      </template>
    </EmptyState>

    <ul v-else-if="deliveries.length > 0" class="grid gap-3 md:grid-cols-2">
      <li v-for="d in deliveries" :key="d.id" class="grid gap-2 rounded-md border p-4" :data-testid="`delivery-${d.channel_name}`">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex flex-wrap items-center gap-2">
            <RouterLink :to="`/channels/${d.channel_id}`" class="font-medium hover:underline">{{ d.channel_name }}</RouterLink>
            <span class="text-muted-foreground text-xs">{{ plugins.destination(d.channel_type) ? t(plugins.destination(d.channel_type)!.name) : d.channel_type }}</span>
            <ChannelHealth v-if="channelOf(d)" :status="channelOf(d)!.status" :error="channelOf(d)!.last_error" />
            <Badge v-if="!d.enabled" variant="secondary">{{ t('deliveries.disabled') }}</Badge>
          </div>
          <div v-if="canEdit" class="flex gap-1">
            <Button variant="ghost" size="icon" :title="t('deliveries.testChannel')" :aria-label="t('deliveries.testChannel')" :disabled="sending" data-testid="delivery-test" @click="sendTest(d.channel_id)">
              <Send aria-hidden="true" />
            </Button>
            <Button variant="ghost" size="icon" :title="t('deliveries.edit')" :aria-label="t('deliveries.edit')" data-testid="delivery-edit" @click="edit(d)"><Pencil aria-hidden="true" /></Button>
            <Button variant="ghost" size="icon" :title="t('deliveries.delete')" :aria-label="t('deliveries.delete')" data-testid="delivery-delete" @click="confirmRemove(d)">
              <Trash2 aria-hidden="true" />
            </Button>
          </div>
        </div>
        <p class="text-sm" data-testid="delivery-summary">{{ summary(d) }}</p>
      </li>
    </ul>

    <DeliveryEditor v-model:open="editorOpen" :report-id="reportId" :channels="channels" :delivery="editing" :links-enabled="linksEnabled" @saved="load" />
    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="t('deliveries.delete')"
      :description="t('deliveries.deleteImpact', { channel: removing?.channel_name ?? '' })"
      :confirm-label="t('deliveries.delete')"
      destructive
      :on-confirm="remove"
    />
  </section>
</template>
