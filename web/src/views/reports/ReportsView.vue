<script setup lang="ts">
import { Download, FileClock, GitBranch, Plus } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import EmptyState from '@/components/common/EmptyState.vue'
import ExportDialog from '@/components/config/ExportDialog.vue'
import ReportStatusBadge from '@/components/report/ReportStatusBadge.vue'
import RunStatusBadge from '@/components/run/RunStatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'
import { useSessionStore } from '@/stores/session'

const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const items = ref<Schemas['ReportSummary'][]>([])
const loaded = ref(false)
const busy = ref<string | null>(null)
/** Slugs picked for export; none exports every report. */
const selected = ref<string[]>([])
const exportOpen = ref(false)

function pick(slug: string, on: boolean) {
  selected.value = on ? [...selected.value, slug] : selected.value.filter((s) => s !== slug)
}

onMounted(async () => {
  try {
    items.value = unwrap(await api.GET('/api/v1/reports')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
})

async function toggle(r: Schemas['ReportSummary'], on: boolean) {
  busy.value = r.id
  try {
    const path = { params: { path: { reportId: r.id } } }
    const updated = unwrap(on ? await api.POST('/api/v1/reports/{reportId}/resume', path) : await api.POST('/api/v1/reports/{reportId}/pause', path))
    items.value = items.value.map((x) => (x.id === r.id ? updated : x))
    toast.success(on ? t('reports.resumed') : t('reports.paused'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.reports') }}</h1>
        <p class="text-muted-foreground text-sm">{{ t('reports.help') }}</p>
      </div>
      <div v-if="items.length > 0" class="flex flex-wrap gap-2">
        <Button variant="outline" data-testid="reports-export" @click="exportOpen = true">
          <Download aria-hidden="true" />{{ selected.length ? t('config.export.selected', { n: selected.length }) : t('config.export.button') }}
        </Button>
        <Button v-if="session.hasRole('editor')" as-child>
          <RouterLink to="/reports/new" data-testid="report-new"><Plus aria-hidden="true" />{{ t('reports.add') }}</RouterLink>
        </Button>
      </div>
    </div>
    <EmptyState v-if="loaded && items.length === 0" :icon="FileClock" :title="t('reports.emptyTitle')" :description="t('reports.emptyBody')">
      <Button v-if="session.hasRole('editor')" as-child>
        <RouterLink to="/reports/new" data-testid="report-new"><Plus aria-hidden="true" />{{ t('reports.add') }}</RouterLink>
      </Button>
    </EmptyState>
    <div v-else-if="items.length" class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead class="w-8"><span class="sr-only">{{ t('config.export.pick') }}</span></TableHead>
            <TableHead>{{ t('reports.fields.title') }}</TableHead>
            <TableHead>{{ t('reports.list.status') }}</TableHead>
            <TableHead>{{ t('reports.list.nextRun') }}</TableHead>
            <TableHead>{{ t('reports.list.lastRun') }}</TableHead>
            <TableHead v-if="session.hasRole('editor')">{{ t('reports.list.enabled') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in items" :key="r.id" :data-testid="`report-row-${r.slug}`">
            <TableCell>
              <Checkbox
                :model-value="selected.includes(r.slug)"
                :aria-label="t('config.export.pickOne', { title: r.title })"
                :data-testid="`report-pick-${r.slug}`"
                @update:model-value="(v) => pick(r.slug, v === true)"
              />
            </TableCell>
            <TableCell>
              <RouterLink :to="`/reports/${r.id}`" class="font-medium hover:underline">{{ r.title }}</RouterLink>
              <Badge v-if="r.managed_by === 'gitops'" variant="outline" class="ml-2" :title="t('config.managed.title')"><GitBranch class="size-3" aria-hidden="true" />GitOps</Badge>
              <Badge v-if="r.gitops_orphan" variant="destructive" class="ml-2">{{ t('config.managed.orphanBadge') }}</Badge>
              <div class="text-muted-foreground text-xs">{{ r.query_title }} · <span class="font-mono">{{ r.cron }}</span></div>
            </TableCell>
            <TableCell><ReportStatusBadge :status="r.status" /></TableCell>
            <TableCell class="text-sm">{{ r.next_run_at ? fmt.dateTime(r.next_run_at) : t('reports.list.none') }}</TableCell>
            <TableCell class="text-sm">
              <RouterLink v-if="r.last_run" :to="`/runs/${r.last_run.id}`" class="flex flex-wrap items-center gap-2 hover:underline">
                <RunStatusBadge :status="r.last_run.status" />
                <span class="text-muted-foreground text-xs">{{ fmt.dateTime(r.last_run.finished_at ?? r.last_run.created_at) }}</span>
              </RouterLink>
              <span v-else class="text-muted-foreground">{{ t('reports.list.never') }}</span>
            </TableCell>
            <TableCell v-if="session.hasRole('editor')">
              <Switch
                :model-value="r.enabled"
                :disabled="busy === r.id"
                :aria-label="t('reports.list.toggle', { title: r.title })"
                :data-testid="`report-toggle-${r.slug}`"
                @update:model-value="(v: boolean) => toggle(r, v)"
              />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
    <ExportDialog v-model:open="exportOpen" :reports="selected" />
  </div>
</template>
