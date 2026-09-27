<script setup lang="ts">
import { Cable, Plus, TriangleAlert } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import PluginIcon from '@/components/common/PluginIcon.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ConnectionStatus from '@/components/plugin/ConnectionStatus.vue'
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
const items = ref<Schemas['Connection'][]>([])
const loaded = ref(false)

onMounted(async () => {
  try {
    await plugins.load()
    items.value = unwrap(await api.GET('/api/v1/connections')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
})

function driverName(id: string) {
  const p = plugins.connector(id)
  return p ? t(p.name) : id
}
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.connections') }}</h1>
        <p class="text-muted-foreground text-sm">{{ t('connections.help') }}</p>
      </div>
      <Button v-if="session.hasRole('admin') && items.length > 0" as-child>
        <RouterLink to="/connections/new" data-testid="connection-new"><Plus aria-hidden="true" />{{ t('connections.add') }}</RouterLink>
      </Button>
    </div>

    <EmptyState v-if="loaded && items.length === 0" :icon="Cable" :title="t('connections.emptyTitle')" :description="t('connections.emptyBody')">
      <Button v-if="session.hasRole('admin')" as-child>
        <RouterLink to="/connections/new" data-testid="connection-new"><Plus aria-hidden="true" />{{ t('connections.add') }}</RouterLink>
      </Button>
      <p v-else class="text-muted-foreground text-sm">{{ t('connections.askAdmin') }}</p>
    </EmptyState>

    <div v-else-if="items.length > 0" class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('fields.name') }}</TableHead>
            <TableHead>{{ t('connections.driver') }}</TableHead>
            <TableHead>{{ t('connections.status') }}</TableHead>
            <TableHead>{{ t('connections.lastChecked') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="c in items" :key="c.id" :data-testid="`connection-row-${c.name}`">
            <TableCell>
              <RouterLink :to="`/connections/${c.id}`" class="font-medium hover:underline">{{ c.name }}</RouterLink>
              <div v-if="c.server_version" class="text-muted-foreground text-xs">{{ c.server_version }}</div>
            </TableCell>
            <TableCell><span class="inline-flex items-center gap-2"><PluginIcon :icon="plugins.connector(c.driver)?.icon" :size="16" />{{ driverName(c.driver) }}</span></TableCell>
            <TableCell class="flex flex-wrap gap-1">
              <ConnectionStatus :status="c.status" :error="c.last_error" />
              <Badge v-if="c.has_write_permission" variant="outline" class="text-amber-700 dark:text-amber-400">
                <TriangleAlert class="size-3" aria-hidden="true" />{{ t('connections.canWrite') }}
              </Badge>
            </TableCell>
            <TableCell class="text-muted-foreground text-sm">{{ c.last_checked_at ? fmt.dateTime(c.last_checked_at) : t('settings.users.never') }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </div>
</template>
