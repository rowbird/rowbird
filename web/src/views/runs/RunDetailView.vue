<script setup lang="ts">
import { CircleCheck, CircleDashed, CircleMinus, CircleX, Download, File, Link, LoaderCircle, RotateCw, Square } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ResultsPanel from '@/components/query/ResultsPanel.vue'
import RunStatusBadge from '@/components/run/RunStatusBadge.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useFormat } from '@/composables/useFormat'
import { useLiveRefresh } from '@/composables/useLiveRefresh'
import { isActive, runCodeMessage } from '@/lib/runs'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

/** A run as steps: Query, Condition, Formats, Deliveries (docs/spec/06-ui.md, "Runs"). */
const route = useRoute()
const { t, te, locale } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const plugins = usePluginsStore()

type StepState = 'pending' | 'running' | 'success' | 'failed' | 'skipped' | 'cancelled' | 'none'

const run = ref<Schemas['Run'] | null>(null)
const cancelling = ref(false)
const resending = ref<string | null>(null)

async function load() {
  run.value = unwrap(await api.GET('/api/v1/runs/{runId}', { params: { path: { runId: String(route.params.id) } } }))
}

onMounted(async () => {
  try {
    await plugins.load()
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

useLiveRefresh(load, {
  events: ['run.updated'],
  match: (e) => e.data.run_id === String(route.params.id),
  active: () => !!run.value && isActive(run.value.status),
})

const code = computed(() => run.value?.error_code ?? '')
const conditionFailed = computed(() => code.value.startsWith('condition.'))

const queryStep = computed<StepState>(() => {
  const r = run.value
  if (!r) return 'none'
  if (r.row_count != null) return 'success'
  if (r.status === 'pending') return 'pending'
  if (r.status === 'running') return 'running'
  if (r.status === 'failed' && !conditionFailed.value) return 'failed'
  return r.status === 'cancelled' ? 'cancelled' : 'none'
})
const conditionStep = computed<StepState>(() => {
  const r = run.value
  if (!r) return 'none'
  if (r.condition) return r.condition.passed ? 'success' : 'skipped'
  if (conditionFailed.value) return 'failed'
  return queryStep.value === 'success' && r.status === 'running' ? 'running' : 'none'
})

const icons: Record<StepState, unknown> = {
  pending: CircleDashed, running: LoaderCircle, success: CircleCheck, failed: CircleX, skipped: CircleMinus, cancelled: Square, none: CircleDashed,
}
const iconClass: Record<StepState, string> = {
  pending: 'text-muted-foreground', running: 'animate-spin text-muted-foreground', success: 'text-emerald-600', failed: 'text-destructive',
  skipped: 'text-muted-foreground', cancelled: 'text-muted-foreground', none: 'text-muted-foreground/50',
}

/** The sample in the shape the results table takes. */
const sample = computed<Schemas['PreviewResult'] | null>(() => {
  const s = run.value?.sample
  if (!s) return null
  return { columns: s.columns, rows: s.rows, truncated: false, duration_ms: run.value?.duration_ms ?? 0, params: [], timezone: '' }
})

function ruleName(type: string) {
  const p = plugins.plugins.find((x) => x.kind === 'condition' && x.id === type)
  return p ? t(p.name) : type
}

/** A short description of a rule's outcome from its detail. */
function ruleDetail(rule: Schemas['RuleResult']) {
  const d = rule.detail ?? {}
  if (d.first_result) return t('runs.detail.firstResult')
  if (d.no_rows) return t('runs.detail.noRows')
  if ('actual' in d) return t('runs.detail.actual', { value: d.actual === null ? 'NULL' : String(d.actual) })
  if ('row_count' in d) return t('runs.detail.rowCount', { n: d.row_count as number })
  return ''
}

const count = (n: number) => new Intl.NumberFormat(locale.value).format(n)

/**
 * File formats the result can be downloaded in: the files the deliveries stored (kept for weeks)
 * and, while the run keeps its result, any other file format.
 */
const spoolAvailable = computed(() => {
  const r = run.value
  return !!r?.result_expires_at && new Date(r.result_expires_at) > new Date()
})
const downloads = computed(() => {
  const r = run.value
  if (!r) return []
  const stored = new Set(r.files.map((f) => f.format))
  return plugins.fileFormatters
    .filter((p) => spoolAvailable.value || stored.has(p.id))
    .map((p) => ({ id: p.id, label: t(p.name), href: `/api/v1/runs/${r.id}/result?format=${encodeURIComponent(p.id)}` }))
})
const downloadExpired = computed(() => {
  const r = run.value
  return !!r && ['success', 'partial', 'skipped'].includes(r.status) && downloads.value.length === 0
})
const filesUntil = computed(() => {
  const times = (run.value?.files ?? []).map((f) => f.expires_at).filter((x): x is string => !!x).sort()
  return times[0] ?? null
})

const formatsStep = computed<StepState>(() => ((run.value?.files.length ?? 0) > 0 ? 'success' : 'none'))
const deliveriesStep = computed<StepState>(() => {
  const ds = run.value?.deliveries ?? []
  if (ds.length === 0) return 'none'
  if (ds.some((d) => d.status === 'pending' || d.status === 'sending')) return 'running'
  if (ds.some((d) => d.status === 'failed')) return 'failed'
  return 'success'
})
const attemptIcon: Record<Schemas['DeliveryAttempt']['status'], StepState> = {
  pending: 'pending', sending: 'running', sent: 'success', failed: 'failed', skipped: 'skipped',
}

function channelType(type: string) {
  const p = plugins.destination(type)
  return p ? t(p.name) : type
}

function formatName(id: string) {
  const p = plugins.plugins.find((x) => x.kind === 'formatter' && x.id === id)
  return p ? t(p.name) : id
}

const size = (n: number) => (n < 1024 ? t('runs.detail.bytes', { n: count(n) }) : n < 1024 * 1024 ? t('runs.detail.kb', { n: count(Math.round(n / 1024)) }) : t('runs.detail.mb', { n: count(Math.round(n / 1024 / 1024)) }))

function attemptError(a: Schemas['DeliveryAttempt']) {
  return a.error_code && te(`errors.${a.error_code}`) ? t(`errors.${a.error_code}`) : (a.error_code ?? '')
}

const metaList = (a: Schemas['DeliveryAttempt'], key: 'attachments' | 'links') => (Array.isArray(a.meta[key]) ? (a.meta[key] as string[]) : [])

async function resend(a: Schemas['DeliveryAttempt']) {
  if (!run.value) return
  resending.value = a.id
  try {
    const updated = unwrap(await api.POST('/api/v1/runs/{runId}/attempts/{attemptId}/retry', { params: { path: { runId: run.value.id, attemptId: a.id } } }))
    if (updated.status === 'sent') toast.success(t('runs.detail.resent', { channel: a.channel_name }))
    else toast.error(t('runs.detail.resendFailed', { channel: a.channel_name }))
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    resending.value = null
  }
}

async function cancel() {
  if (!run.value) return
  cancelling.value = true
  try {
    run.value = unwrap(await api.POST('/api/v1/runs/{runId}/cancel', { params: { path: { runId: run.value.id } } }))
    toast.success(t('runs.detail.cancelRequested'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    cancelling.value = false
  }
}
</script>

<template>
  <div v-if="run" class="grid gap-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="grid gap-1">
        <div class="flex flex-wrap items-center gap-2">
          <h1 class="text-2xl font-semibold" data-testid="page-title">
            <RouterLink :to="`/reports/${run.report_id}`" class="hover:underline">{{ run.report_title }}</RouterLink>
          </h1>
          <RunStatusBadge :status="run.status" />
        </div>
        <p class="text-muted-foreground text-sm" data-testid="run-meta">
          {{ t(`runs.trigger.${run.trigger}`) }}<span v-if="run.triggered_by_name"> · {{ run.triggered_by_name }}</span>
          · {{ fmt.dateTime(run.started_at ?? run.created_at) }}
          <span v-if="run.duration_ms != null"> · {{ t('runs.ms', { n: count(run.duration_ms) }) }}</span>
          <span v-if="run.attempt > 1"> · {{ t('runs.detail.attempt', { n: run.attempt }) }}</span>
        </p>
      </div>
      <Button v-if="session.hasRole('editor') && isActive(run.status)" variant="outline" :disabled="cancelling || !!run.cancel_requested_at" data-testid="run-cancel" @click="cancel">
        <Square aria-hidden="true" />{{ run.cancel_requested_at ? t('runs.detail.cancelling') : t('runs.detail.cancel') }}
      </Button>
    </div>

    <Alert v-if="code && run.status !== 'success'" :variant="run.status === 'failed' ? 'destructive' : 'default'" data-testid="run-error">
      <AlertDescription>
        <p>{{ runCodeMessage(code, t, te) }}</p>
        <pre v-if="run.error_message" class="mt-2 text-xs whitespace-pre-wrap">{{ run.error_message }}</pre>
        <p v-if="run.status === 'pending' && run.attempt > 1" class="mt-1 text-xs">{{ t('runs.detail.retrying') }}</p>
      </AlertDescription>
    </Alert>

    <ol class="grid gap-4" data-testid="run-steps">
      <li class="grid gap-3 rounded-md border p-4" data-testid="step-query">
        <div class="flex items-center gap-2">
          <component :is="icons[queryStep]" class="size-5" :class="iconClass[queryStep]" aria-hidden="true" />
          <h2 class="font-semibold">{{ t('runs.steps.query') }}</h2>
          <span v-if="run.row_count != null" class="text-muted-foreground text-sm" data-testid="run-rows">
            {{ t('runs.detail.rows', { n: count(run.row_count) }, run.row_count) }}<span v-if="run.truncated"> · {{ t('runs.detail.truncated') }}</span>
          </span>
        </div>
        <div v-if="run.query" class="grid gap-2 text-sm">
          <RouterLink :to="`/queries/${run.query.query_id}`" class="hover:underline">{{ t('runs.detail.version', { n: run.query.version }) }}</RouterLink>
          <details>
            <summary class="cursor-pointer">{{ t('runs.detail.sql') }}</summary>
            <pre class="bg-muted mt-2 overflow-x-auto rounded p-3 font-mono text-xs" data-testid="run-sql">{{ run.query.sql }}</pre>
          </details>
        </div>
        <div v-if="run.params?.values.length" class="text-sm">
          <span class="text-muted-foreground">{{ t('results.params', { tz: run.params.timezone }) }}</span>
          <span v-for="p in run.params.values" :key="p.name" class="ml-2 font-mono" data-testid="run-param">{{ p.name }} = {{ p.value }}</span>
        </div>
      </li>

      <li class="grid gap-2 rounded-md border p-4" data-testid="step-condition">
        <div class="flex items-center gap-2">
          <component :is="icons[conditionStep]" class="size-5" :class="iconClass[conditionStep]" aria-hidden="true" />
          <h2 class="font-semibold">{{ t('runs.steps.condition') }}</h2>
          <span v-if="run.condition" class="text-muted-foreground text-sm" data-testid="run-condition">
            {{ run.condition.passed ? t('runs.detail.conditionPassed') : t('runs.detail.conditionFailed') }}
          </span>
        </div>
        <ul v-if="run.condition?.rules.length" class="grid gap-1 text-sm">
          <li v-for="(r, i) in run.condition.rules" :key="i" class="flex items-center gap-2">
            <component :is="r.passed ? CircleCheck : CircleX" class="size-4" :class="r.passed ? 'text-emerald-600' : 'text-muted-foreground'" aria-hidden="true" />
            {{ ruleName(r.type) }} <span class="text-muted-foreground text-xs">{{ ruleDetail(r) }}</span>
          </li>
        </ul>
        <p v-else-if="run.condition" class="text-muted-foreground text-sm">{{ t('conditions.always') }}</p>
      </li>

      <li class="grid gap-2 rounded-md border p-4" data-testid="step-formats">
        <div class="flex items-center gap-2">
          <component :is="icons[formatsStep]" class="size-5" :class="iconClass[formatsStep]" aria-hidden="true" />
          <h2 class="font-semibold">{{ t('runs.steps.formats') }}</h2>
          <span v-if="run.files.length === 0" class="text-muted-foreground text-sm">{{ t('runs.detail.noFormats') }}</span>
        </div>
        <ul v-if="run.files.length" class="grid gap-1 text-sm" data-testid="run-files">
          <li v-for="f in run.files" :key="f.format" class="flex flex-wrap items-center gap-2">
            <File class="text-muted-foreground size-4" aria-hidden="true" />
            <a :href="`/api/v1/runs/${run.id}/result?format=${encodeURIComponent(f.format)}`" download class="hover:underline">{{ f.file_name }}</a>
            <span class="text-muted-foreground text-xs">{{ formatName(f.format) }} · {{ size(f.size_bytes) }}</span>
          </li>
        </ul>
      </li>
      <li class="grid gap-2 rounded-md border p-4" data-testid="step-deliveries">
        <div class="flex items-center gap-2">
          <component :is="icons[deliveriesStep]" class="size-5" :class="iconClass[deliveriesStep]" aria-hidden="true" />
          <h2 class="font-semibold">{{ t('runs.steps.deliveries') }}</h2>
          <span v-if="run.deliveries.length === 0" class="text-muted-foreground text-sm">{{ run.deliver ? t('runs.detail.noDeliveries') : t('runs.detail.notDelivered') }}</span>
        </div>
        <ul v-if="run.deliveries.length" class="divide-y rounded-md border" data-testid="run-attempts">
          <li v-for="a in run.deliveries" :key="a.id" class="grid gap-1 p-3 text-sm" :data-testid="`attempt-${a.channel_name}`">
            <div class="flex flex-wrap items-center gap-2">
              <component :is="icons[attemptIcon[a.status]]" class="size-4" :class="iconClass[attemptIcon[a.status]]" aria-hidden="true" />
              <RouterLink v-if="a.channel_id && a.channel_name" :to="`/channels/${a.channel_id}`" class="font-medium hover:underline">{{ a.channel_name }}</RouterLink>
              <span v-else class="font-medium">{{ t('runs.detail.deletedChannel') }}</span>
              <span class="text-muted-foreground text-xs">{{ channelType(a.channel_type) }}</span>
              <Badge :variant="a.status === 'failed' ? 'destructive' : a.status === 'sent' ? 'default' : 'secondary'">{{ t(`runs.attemptStatus.${a.status}`) }}</Badge>
              <span v-if="a.attempts > 1" class="text-muted-foreground text-xs">{{ t('runs.detail.tries', { n: a.attempts }) }}</span>
              <span v-if="a.sent_at" class="text-muted-foreground text-xs">{{ fmt.dateTime(a.sent_at) }}</span>
              <Button
                v-if="a.resendable && session.hasRole('editor')"
                variant="outline"
                size="sm"
                class="ml-auto"
                :disabled="resending !== null"
                data-testid="attempt-resend"
                @click="resend(a)"
              >
                <RotateCw aria-hidden="true" :class="resending === a.id ? 'animate-spin' : ''" />{{ t('runs.detail.resend') }}
              </Button>
            </div>
            <div v-if="a.status === 'failed'" class="text-destructive grid gap-0.5 text-xs" data-testid="attempt-error">
              <span>{{ attemptError(a) }}</span>
              <span v-if="a.error_message" class="font-mono break-all opacity-80">{{ a.error_message }}</span>
            </div>
            <p v-if="a.meta.fallback_to_link" class="text-muted-foreground text-xs" data-testid="attempt-fallback">{{ t('runs.detail.fallbackToLink') }}</p>
            <div v-if="metaList(a, 'attachments').length || metaList(a, 'links').length" class="text-muted-foreground flex flex-wrap gap-3 text-xs">
              <span v-for="n in metaList(a, 'attachments')" :key="`a-${n}`" class="flex items-center gap-1"><File class="size-3" aria-hidden="true" />{{ n }}</span>
              <span v-for="n in metaList(a, 'links')" :key="`l-${n}`" class="flex items-center gap-1"><Link class="size-3" aria-hidden="true" />{{ n }}</span>
            </div>
          </li>
        </ul>
      </li>
    </ol>

    <section v-if="downloads.length || downloadExpired" class="grid gap-2" data-testid="run-downloads">
      <h2 class="text-lg font-semibold">{{ t('runs.detail.download') }}</h2>
      <div v-if="downloads.length" class="flex flex-wrap gap-2">
        <Button v-for="d in downloads" :key="d.id" variant="outline" size="sm" as-child>
          <a :href="d.href" download :data-testid="`download-${d.id}`"><Download aria-hidden="true" />{{ d.label }}</a>
        </Button>
      </div>
      <p class="text-muted-foreground text-xs">
        <template v-if="spoolAvailable">{{ t('runs.detail.downloadUntil', { time: fmt.dateTime(run.result_expires_at) }) }}</template>
        <template v-else-if="downloads.length && filesUntil">{{ t('runs.detail.filesUntil', { time: fmt.dateTime(filesUntil) }) }}</template>
        <template v-else-if="!downloads.length">{{ t('runs.detail.downloadExpired') }}</template>
      </p>
    </section>

    <section v-if="sample" class="grid gap-2">
      <h2 class="text-lg font-semibold">{{ t('runs.detail.sample') }}</h2>
      <p class="text-muted-foreground text-sm" data-testid="run-sample-summary">
        {{ run.row_count != null && run.row_count > sample.rows.length ? t('runs.detail.sampleOf', { shown: sample.rows.length, total: count(run.row_count) }) : t('runs.detail.sampleAll') }}
      </p>
      <ResultsPanel :result="sample" hide-summary />
    </section>
  </div>
</template>
