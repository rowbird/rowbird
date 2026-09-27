<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import type { Schemas } from '@/api/client'
import { useFormat } from '@/composables/useFormat'

/**
 * The latest runs of a report as bars, oldest on the left: height is the duration, color the
 * outcome, and each bar opens its run. A summary beside it gives the success rate over finished
 * runs, the median duration and the last failure. Plain SVG, so it follows the theme and the
 * user's language without a chart library.
 */
const props = defineProps<{ runs: Schemas['RunSummary'][] }>()
const { t } = useI18n()
const fmt = useFormat()
const router = useRouter()

const W = 600
const H = 120
const GAP = 4

const ordered = computed(() => [...props.runs].reverse())
const maxMs = computed(() => Math.max(1, ...ordered.value.map((r) => r.duration_ms ?? 0)))
const barWidth = computed(() => {
  const n = Math.max(ordered.value.length, 10)
  return (W - GAP * (n - 1)) / n
})

const color: Record<string, string> = {
  success: 'fill-emerald-500',
  partial: 'fill-amber-500',
  failed: 'fill-destructive',
  cancelled: 'fill-muted-foreground/50',
  skipped: 'fill-muted-foreground/30',
  pending: 'fill-sky-500/60',
  running: 'fill-sky-500',
}

const bars = computed(() => ordered.value.map((r, i) => {
  const h = r.duration_ms ? Math.max(3, (r.duration_ms / maxMs.value) * (H - 4)) : 3
  const when = fmt.dateTime(r.started_at ?? r.scheduled_for ?? r.finished_at)
  const parts = [when, t(`runs.status.${r.status}`)]
  if (r.duration_ms != null) parts.push(duration(r.duration_ms))
  if (r.row_count != null) parts.push(t('reports.chart.rows', { n: fmt.count(r.row_count) }, r.row_count))
  return { id: r.id, x: i * (barWidth.value + GAP), y: H - h, h, cls: color[r.status] ?? 'fill-muted-foreground', label: parts.join(' · ') }
}))

function duration(ms: number) {
  return ms < 1000 ? t('reports.chart.ms', { n: fmt.count(ms) }) : t('reports.chart.seconds', { n: fmt.count(ms / 1000, 1) })
}

const finished = computed(() => props.runs.filter((r) => ['success', 'partial', 'failed'].includes(r.status)))
const successRate = computed(() => {
  if (!finished.value.length) return null
  return Math.round((finished.value.filter((r) => r.status === 'success').length / finished.value.length) * 100)
})
const median = computed(() => {
  const d = props.runs.map((r) => r.duration_ms).filter((v): v is number => v != null).sort((a, b) => a - b)
  if (!d.length) return null
  const m = Math.floor(d.length / 2)
  return d.length % 2 ? d[m]! : Math.round((d[m - 1]! + d[m]!) / 2)
})
const lastFailure = computed(() => props.runs.find((r) => r.status === 'failed'))
const summary = computed(() => t('reports.chart.alt', {
  n: props.runs.length,
  rate: successRate.value ?? 0,
  median: median.value != null ? duration(median.value) : '',
}))
</script>

<template>
  <div class="grid gap-4 rounded-md border p-4 lg:grid-cols-[1fr_14rem]" data-testid="run-history">
    <svg :viewBox="`0 0 ${W} ${H}`" class="h-32 w-full" preserveAspectRatio="none" role="group" :aria-label="summary">
      <line :x1="0" :x2="W" :y1="H - 0.5" :y2="H - 0.5" class="stroke-border" stroke-width="1" />
      <a v-for="b in bars" :key="b.id" :href="`/runs/${b.id}`" :aria-label="b.label" data-testid="run-bar" @click.prevent="router.push(`/runs/${b.id}`)">
        <title>{{ b.label }}</title>
        <rect :x="b.x" :y="b.y" :width="barWidth" :height="b.h" rx="2" :class="[b.cls, 'hover:opacity-80']" />
      </a>
    </svg>
    <dl class="grid content-start gap-2 text-sm">
      <div>
        <dt class="text-muted-foreground">{{ t('reports.chart.successRate') }}</dt>
        <dd class="text-lg font-semibold" data-testid="run-history-rate">{{ successRate == null ? t('reports.chart.noData') : `${successRate}%` }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">{{ t('reports.chart.median') }}</dt>
        <dd data-testid="run-history-median">{{ median == null ? t('reports.chart.noData') : duration(median) }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">{{ t('reports.chart.lastFailure') }}</dt>
        <dd>
          <RouterLink v-if="lastFailure" :to="`/runs/${lastFailure.id}`" class="underline">{{ fmt.dateTime(lastFailure.started_at ?? lastFailure.scheduled_for) }}</RouterLink>
          <span v-else>{{ t('reports.chart.none') }}</span>
        </dd>
      </div>
    </dl>
  </div>
</template>
