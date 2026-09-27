<script setup lang="ts">
import { Mail, Megaphone, Plus, Siren } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import PluginIcon from '@/components/common/PluginIcon.vue'
import ChannelHealth from '@/components/channel/ChannelHealth.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const plugins = usePluginsStore()
const items = ref<Schemas['Channel'][]>([])
const loaded = ref(false)

onMounted(async () => {
  try {
    await plugins.load()
    items.value = unwrap(await api.GET('/api/v1/channels')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
})

function typeName(id: string) {
  const p = plugins.destination(id)
  return p ? t(p.name) : id
}

function lastActivity(c: Schemas['Channel']) {
  const times = [c.last_success_at, c.last_failure_at].filter((x): x is string => !!x).sort()
  return times.length ? fmt.dateTime(times[times.length - 1]) : t('channels.neverUsed')
}

const reportsUsing = (c: { used_by: { type: string }[] }) => c.used_by.filter((d) => d.type === 'report').length
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.channels') }}</h1>
        <p class="text-muted-foreground text-sm">{{ t('channels.help') }}</p>
      </div>
      <Button v-if="session.hasRole('admin') && items.length > 0" as-child>
        <RouterLink to="/channels/new" data-testid="channel-new"><Plus aria-hidden="true" />{{ t('channels.add') }}</RouterLink>
      </Button>
    </div>

    <EmptyState v-if="loaded && items.length === 0" :icon="Megaphone" :title="t('channels.emptyTitle')" :description="t('channels.emptyBody')">
      <Button v-if="session.hasRole('admin')" as-child>
        <RouterLink to="/channels/new" data-testid="channel-new"><Plus aria-hidden="true" />{{ t('channels.add') }}</RouterLink>
      </Button>
      <p v-else class="text-muted-foreground text-sm">{{ t('channels.askAdmin') }}</p>
    </EmptyState>

    <div v-else-if="items.length > 0" class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('fields.name') }}</TableHead>
            <TableHead>{{ t('channels.type') }}</TableHead>
            <TableHead>{{ t('channels.health') }}</TableHead>
            <TableHead>{{ t('channels.usedBy') }}</TableHead>
            <TableHead>{{ t('channels.lastActivity') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="c in items" :key="c.id" :data-testid="`channel-row-${c.name}`">
            <TableCell>
              <RouterLink :to="`/channels/${c.id}`" class="font-medium hover:underline">{{ c.name }}</RouterLink>
              <Badge v-if="c.is_system_mailer" variant="outline" class="ml-2"><Mail class="size-3" aria-hidden="true" />{{ t('channels.systemMailer') }}</Badge>
              <Badge v-if="c.used_by.some((d) => d.type === 'system_alert')" variant="outline" class="ml-2" data-testid="channel-system-alert"><Siren class="size-3" aria-hidden="true" />{{ t('channels.systemAlert') }}</Badge>
            </TableCell>
            <TableCell><span class="inline-flex items-center gap-2"><PluginIcon :icon="plugins.destination(c.type)?.icon" :size="16" />{{ typeName(c.type) }}</span></TableCell>
            <TableCell><ChannelHealth :status="c.status" :error="c.last_error" /></TableCell>
            <TableCell class="text-sm">{{ t('channels.reportCount', { n: reportsUsing(c) }, reportsUsing(c)) }}</TableCell>
            <TableCell class="text-muted-foreground text-sm">{{ lastActivity(c) }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </div>
</template>
