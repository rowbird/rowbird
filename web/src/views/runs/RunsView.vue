<script setup lang="ts">
import { SearchX } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import RunsTable from '@/components/run/RunsTable.vue'
import { Button } from '@/components/ui/button'
import { useLiveRefresh } from '@/composables/useLiveRefresh'
import { isActive } from '@/lib/runs'

/** Every run of the workspace with filters (docs/spec/06-ui.md, "Runs"). */
const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()

const STATUSES: Schemas['RunStatus'][] = ['pending', 'running', 'success', 'skipped', 'failed', 'cancelled']
const TRIGGERS: Schemas['RunTrigger'][] = ['schedule', 'manual']
const PERIODS = ['all', '24h', '7d', '30d'] as const
const HOURS: Record<string, number> = { '24h': 24, '7d': 24 * 7, '30d': 24 * 30 }

const q = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const status = ref(q('status'))
const trigger = ref(q('trigger'))
const reportId = ref(q('report_id'))
const period = ref(q('period') || 'all')
const reports = ref<Schemas['ReportSummary'][]>([])
const runs = ref<Schemas['RunSummary'][]>([])
const next = ref<string | null>(null)
const loading = ref(false)
const filtered = computed(() => !!(status.value || trigger.value || reportId.value || period.value !== 'all'))

function clearFilters() {
  status.value = ''
  trigger.value = ''
  reportId.value = ''
  period.value = 'all'
}

const statusOptions = computed(() => [{ value: '', label: t('runs.filters.anyStatus') }, ...STATUSES.map((s) => ({ value: s, label: t(`runs.status.${s}`) }))])
const triggerOptions = computed(() => [{ value: '', label: t('runs.filters.anyTrigger') }, ...TRIGGERS.map((s) => ({ value: s, label: t(`runs.trigger.${s}`) }))])
const reportOptions = computed(() => [{ value: '', label: t('runs.filters.anyReport') }, ...reports.value.map((r) => ({ value: r.id, label: r.title }))])
const periodOptions = computed(() => PERIODS.map((p) => ({ value: p, label: t(`runs.filters.period.${p}`) })))

function query(cursor?: string) {
  const hours = HOURS[period.value]
  return {
    limit: 50,
    cursor,
    status: status.value ? [status.value as Schemas['RunStatus']] : undefined,
    trigger: (trigger.value || undefined) as Schemas['RunTrigger'] | undefined,
    report_id: reportId.value || undefined,
    from: hours ? new Date(Date.now() - hours * 3600_000).toISOString() : undefined,
  }
}

async function load(more = false) {
  loading.value = true
  try {
    const page = unwrap(await api.GET('/api/v1/runs', { params: { query: query(more ? (next.value ?? undefined) : undefined) } }))
    runs.value = more ? [...runs.value, ...page.items] : page.items
    next.value = page.next_cursor ?? null
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loading.value = false
  }
}

watch([status, trigger, reportId, period], () => {
  void router.replace({ query: { status: status.value || undefined, trigger: trigger.value || undefined, report_id: reportId.value || undefined, period: period.value === 'all' ? undefined : period.value } })
  void load()
})

onMounted(async () => {
  try {
    reports.value = unwrap(await api.GET('/api/v1/reports')).items
  } catch {
    reports.value = []
  }
  await load()
})

// New and changed runs refresh the first page; loaded pages keep their rows.
useLiveRefresh(
  async () => {
    if (loading.value) return
    const page = unwrap(await api.GET('/api/v1/runs', { params: { query: query() } }))
    const known = new Set(runs.value.map((r) => r.id))
    const fresh = new Map(page.items.map((r) => [r.id, r]))
    runs.value = [...page.items.filter((r) => !known.has(r.id)), ...runs.value.map((r) => fresh.get(r.id) ?? r)]
  },
  { events: ['run.updated'], active: () => runs.value.some((r) => isActive(r.status)) },
)
</script>

<template>
  <div class="grid gap-4">
    <div>
      <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.runs') }}</h1>
      <p class="text-muted-foreground text-sm">{{ t('runs.help') }}</p>
    </div>
    <div class="grid gap-3 sm:grid-cols-4">
      <FormField id="runs-status" :label="t('runs.fields.status')">
        <NativeSelect id="runs-status" v-model="status" :options="statusOptions" />
      </FormField>
      <FormField id="runs-report" :label="t('runs.fields.report')">
        <NativeSelect id="runs-report" v-model="reportId" :options="reportOptions" />
      </FormField>
      <FormField id="runs-trigger" :label="t('runs.fields.trigger')">
        <NativeSelect id="runs-trigger" v-model="trigger" :options="triggerOptions" />
      </FormField>
      <FormField id="runs-period" :label="t('runs.filters.periodLabel')">
        <NativeSelect id="runs-period" v-model="period" :options="periodOptions" />
      </FormField>
    </div>
    <EmptyState
      v-if="filtered && !loading && runs.length === 0"
      :icon="SearchX"
      :title="t('runs.filters.emptyTitle')"
      :description="t('runs.filters.emptyBody')"
      data-testid="runs-filtered-empty"
    >
      <Button variant="outline" size="sm" data-testid="runs-clear-filters" @click="clearFilters">{{ t('runs.filters.clear') }}</Button>
    </EmptyState>
    <RunsTable v-else :runs="runs" />
    <div v-if="next">
      <Button variant="outline" :disabled="loading" data-testid="runs-more" @click="load(true)">{{ t('common.loadMore') }}</Button>
    </div>
  </div>
</template>
