<script setup lang="ts">
import { Download, Pause, Pencil, Play, Send, Trash2 } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ExportDialog from '@/components/config/ExportDialog.vue'
import ManagedNotice from '@/components/config/ManagedNotice.vue'
import DeliveriesSection from '@/components/delivery/DeliveriesSection.vue'
import ReportStatusBadge from '@/components/report/ReportStatusBadge.vue'
import RunHistoryChart from '@/components/reports/RunHistoryChart.vue'
import RunsTable from '@/components/run/RunsTable.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useFormat } from '@/composables/useFormat'
import { useLiveRefresh } from '@/composables/useLiveRefresh'
import { isActive } from '@/lib/runs'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const { t, te, locale } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const plugins = usePluginsStore()

const id = computed(() => String(route.params.id))
const report = ref<Schemas['Report'] | null>(null)
const runs = ref<Schemas['RunSummary'][]>([])
const description = ref('')
const busy = ref(false)
const deleteOpen = ref(false)
const canEdit = computed(() => session.hasRole('editor'))
/** The configuration directory owns the report: its definition is read only here. */
const managed = computed(() => report.value?.managed_by === 'gitops')
const exportOpen = ref(false)

async function loadRuns() {
  runs.value = unwrap(await api.GET('/api/v1/runs', { params: { query: { report_id: id.value, limit: 30 } } })).items
}

async function load() {
  report.value = unwrap(await api.GET('/api/v1/reports/{reportId}', { params: { path: { reportId: id.value } } }))
  await loadRuns()
  try {
    description.value = unwrap(await api.POST('/api/v1/schedules/preview', {
      body: { cron: report.value.cron, timezone: report.value.timezone, count: 1, locale: locale.value },
    })).description
  } catch {
    description.value = ''
  }
}

onMounted(async () => {
  try {
    await plugins.load()
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

useLiveRefresh(
  async () => {
    await loadRuns()
    report.value = unwrap(await api.GET('/api/v1/reports/{reportId}', { params: { path: { reportId: id.value } } }))
  },
  {
    events: ['run.updated', 'report.updated'],
    match: (e) => e.data.report_id === id.value,
    active: () => runs.value.some((r) => isActive(r.status)),
  },
)

const conditionSummary = computed(() => {
  const c = report.value?.condition
  if (!c || c.rules.length === 0) return t('conditions.always')
  const names = c.rules.map((r) => {
    const p = plugins.plugins.find((x) => x.kind === 'condition' && x.id === r.type)
    return p ? t(p.name) : r.type
  })
  return t(c.match === 'any' ? 'conditions.summaryAny' : 'conditions.summaryAll', { rules: names.join(', ') })
})

async function run(deliver: boolean) {
  busy.value = true
  try {
    const r = unwrap(await api.POST('/api/v1/reports/{reportId}/run', { params: { path: { reportId: id.value } }, body: { deliver } }))
    toast.success(t('reports.detail.queued'))
    await router.push(`/runs/${r.id}`)
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}

async function toggle() {
  if (!report.value) return
  busy.value = true
  try {
    const path = { params: { path: { reportId: id.value } } }
    report.value = unwrap(report.value.enabled ? await api.POST('/api/v1/reports/{reportId}/pause', path) : await api.POST('/api/v1/reports/{reportId}/resume', path))
    toast.success(report.value.enabled ? t('reports.resumed') : t('reports.paused'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}

async function remove() {
  try {
    unwrap(await api.DELETE('/api/v1/reports/{reportId}', { params: { path: { reportId: id.value } } }))
    toast.success(t('reports.detail.deleted'))
    await router.push('/reports')
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
</script>

<template>
  <div v-if="report" class="grid gap-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="grid gap-1">
        <div class="flex flex-wrap items-center gap-2">
          <h1 class="text-2xl font-semibold" data-testid="page-title">{{ report.title }}</h1>
          <ReportStatusBadge :status="report.status" />
        </div>
        <p v-if="report.description" class="text-muted-foreground text-sm">{{ report.description }}</p>
      </div>
      <div v-if="canEdit" class="flex flex-wrap gap-2">
        <Button :disabled="busy" data-testid="report-run" @click="run(false)"><Play aria-hidden="true" />{{ t('reports.detail.runNow') }}</Button>
        <Button variant="outline" :disabled="busy" data-testid="report-run-deliver" :title="t('reports.detail.runDeliverHint')" @click="run(true)">
          <Send aria-hidden="true" />{{ t('reports.detail.runDeliver') }}
        </Button>
        <Button variant="outline" :disabled="busy" data-testid="report-toggle" @click="toggle">
          <component :is="report.enabled ? Pause : Play" aria-hidden="true" />{{ report.enabled ? t('reports.detail.pause') : t('reports.detail.resume') }}
        </Button>
        <Button variant="outline" data-testid="report-export" @click="exportOpen = true"><Download aria-hidden="true" />{{ t('config.export.button') }}</Button>
        <Button v-if="!managed" variant="outline" as-child>
          <RouterLink :to="`/reports/${report.id}/edit`" data-testid="report-edit"><Pencil aria-hidden="true" />{{ t('reports.detail.edit') }}</RouterLink>
        </Button>
      </div>
    </div>

    <ManagedNotice :id="report.id" kind="report" :managed-by="report.managed_by" @changed="load" />
    <Alert v-if="report.gitops_orphan" variant="destructive" data-testid="report-orphan">
      <AlertDescription>{{ t('config.managed.orphan') }}</AlertDescription>
    </Alert>
    <Alert v-if="report.paused_reason === 'auto_failures'" variant="destructive" data-testid="report-auto-paused">
      <AlertDescription>{{ t('reports.detail.autoPaused', { n: report.consecutive_failures }) }}</AlertDescription>
    </Alert>

    <dl class="grid gap-4 rounded-md border p-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
      <div>
        <dt class="text-muted-foreground">{{ t('reports.fields.query') }}</dt>
        <dd><RouterLink :to="`/queries/${report.query_id}`" class="font-medium hover:underline">{{ report.query_title }}</RouterLink></dd>
      </div>
      <div>
        <dt class="text-muted-foreground">{{ t('reports.sections.schedule') }}</dt>
        <dd data-testid="report-schedule">{{ description || report.cron }}</dd>
        <dd class="text-muted-foreground text-xs"><span class="font-mono">{{ report.cron }}</span> · {{ report.timezone }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">{{ t('reports.list.nextRun') }}</dt>
        <dd data-testid="report-next-run">{{ report.next_run_at ? fmt.dateTime(report.next_run_at) : t('reports.list.none') }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">{{ t('reports.sections.conditions') }}</dt>
        <dd data-testid="report-condition">{{ conditionSummary }}</dd>
      </div>
    </dl>

    <DeliveriesSection :report-id="report.id" :readonly="managed" />

    <section class="grid gap-2">
      <h2 class="text-lg font-semibold">{{ t('reports.detail.runs') }}</h2>
      <RunHistoryChart v-if="runs.length" :runs="runs" />
      <RunsTable :runs="runs" :show-report="false" />
      <RouterLink :to="`/runs?report_id=${report.id}`" class="text-sm underline">{{ t('reports.detail.allRuns') }}</RouterLink>
    </section>

    <div v-if="canEdit && !managed">
      <Button variant="ghost" class="text-destructive" data-testid="report-delete" @click="deleteOpen = true"><Trash2 aria-hidden="true" />{{ t('reports.detail.delete') }}</Button>
    </div>
    <ExportDialog v-model:open="exportOpen" :reports="[report.slug]" />
    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="t('reports.detail.delete')"
      :description="t('reports.detail.deleteImpact', { title: report.title })"
      :confirm-label="t('reports.detail.delete')"
      destructive
      :on-confirm="remove"
    />
  </div>
</template>
