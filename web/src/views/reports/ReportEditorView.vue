<script setup lang="ts">
import { Save } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import ConditionEditor from '@/components/report/ConditionEditor.vue'
import ScheduleBuilder from '@/components/report/ScheduleBuilder.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { useApiError } from '@/composables/useApiError'
import { toPayload } from '@/lib/schema'
import { browserTimeZone } from '@/lib/timezones'
import { usePluginsStore } from '@/stores/plugins'

/** Report editor, one page with sections (docs/spec/06-ui.md): Query, Schedule, Conditions, Advanced. */
const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()
const plugins = usePluginsStore()
const err = useApiError()

const report = ref<Schemas['Report'] | null>(null)
const queries = ref<Schemas['QuerySummary'][]>([])
const query = ref<Schemas['Query'] | null>(null)
const loaded = ref(false)
const saving = ref(false)

const title = ref('')
const slug = ref('')
const slugTouched = ref(false)
const description = ref('')
const queryId = ref('')
const overrides = ref<Record<string, string>>({})
const cron = ref('0 8 * * 1-5')
const timezone = ref(browserTimeZone())
const condition = ref<Schemas['Condition']>({ match: 'all', rules: [] })
const enabled = ref(true)
const maxRows = ref<number | ''>('')
const retryMax = ref(2)
const retryBackoff = ref(30)
const misfire = ref<Schemas['MisfirePolicy']>('run_once')
const overlap = ref<Schemas['OverlapPolicy']>('skip')
const autoPause = ref(5)
const notifyOwner = ref(true)

const queryOptions = computed(() => queries.value.map((q) => ({ value: q.id, label: q.title })))
const misfireOptions = computed(() => (['run_once', 'skip'] as const).map((v) => ({ value: v, label: t(`reports.misfire.${v}`) })))
const overlapOptions = computed(() => (['skip', 'queue'] as const).map((v) => ({ value: v, label: t(`reports.overlap.${v}`) })))
/** Field errors keyed by API path, translated. */
const fieldErrors = computed(() => {
  const out: Record<string, string | undefined> = {}
  const e = err.error.value as { fields?: Record<string, string> } | null
  for (const k of Object.keys(e?.fields ?? {})) out[k] = err.field(k)
  return out
})

function slugify(s: string) {
  return s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63)
}
watch(title, (v) => {
  if (!slugTouched.value && !report.value) slug.value = slugify(v)
})

async function loadQuery(id: string) {
  query.value = null
  if (!id) return
  try {
    query.value = unwrap(await api.GET('/api/v1/queries/{queryId}', { params: { path: { queryId: id } } }))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
watch(queryId, (id, old) => {
  if (old) overrides.value = {}
  void loadQuery(id)
})

function apply(r: Schemas['Report']) {
  report.value = r
  title.value = r.title
  slug.value = r.slug
  description.value = r.description
  queryId.value = r.query_id
  overrides.value = { ...r.param_overrides }
  cron.value = r.cron
  timezone.value = r.timezone
  condition.value = r.condition
  enabled.value = r.enabled
  maxRows.value = r.max_rows ?? ''
  retryMax.value = r.retry_max
  retryBackoff.value = r.retry_backoff_seconds
  misfire.value = r.misfire_policy
  overlap.value = r.overlap_policy
  autoPause.value = r.auto_pause_after
  notifyOwner.value = r.notify_owner_on_failure
}

onMounted(async () => {
  try {
    await plugins.load()
    queries.value = unwrap(await api.GET('/api/v1/queries')).items
    if (typeof route.params.id === 'string') {
      apply(unwrap(await api.GET('/api/v1/reports/{reportId}', { params: { path: { reportId: route.params.id } } })))
    } else {
      const settings = unwrap(await api.GET('/api/v1/settings'))
      timezone.value = settings.default_timezone || timezone.value
      queryId.value = String(route.query.query ?? queries.value[0]?.id ?? '')
    }
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
})

/** Overrides to send: empty ones fall back to the parameter's default. */
function overridesPayload() {
  const names = new Set((query.value?.params ?? []).map((p) => p.name))
  return Object.fromEntries(Object.entries(overrides.value).filter(([k, v]) => names.has(k) && v !== ''))
}

/** Rules without the parameters their schema hides (for example the upper value outside "between"). */
function conditionPayload(): Schemas['Condition'] {
  return {
    match: condition.value.match,
    rules: condition.value.rules.map((r) => {
      const p = plugins.plugins.find((x) => x.kind === 'condition' && x.id === r.type)
      const { type, ...params } = r
      return { ...(p ? toPayload(p.schema, params) : params), type }
    }),
  }
}

async function save() {
  saving.value = true
  err.clear()
  const body = {
    title: title.value, slug: slug.value || undefined, description: description.value, query_id: queryId.value,
    cron: cron.value, timezone: timezone.value, condition: conditionPayload(), param_overrides: overridesPayload(),
    max_rows: maxRows.value === '' ? 0 : Number(maxRows.value), retry_max: Number(retryMax.value),
    retry_backoff_seconds: Number(retryBackoff.value), misfire_policy: misfire.value, overlap_policy: overlap.value,
    auto_pause_after: Number(autoPause.value), notify_owner_on_failure: notifyOwner.value,
  }
  try {
    let r: Schemas['Report']
    if (report.value) {
      r = unwrap(await api.PATCH('/api/v1/reports/{reportId}', { params: { path: { reportId: report.value.id } }, body: { ...body, slug: slug.value, version: report.value.version } }))
      toast.success(t('reports.editor.saved'))
    } else {
      r = unwrap(await api.POST('/api/v1/reports', { body: { ...body, enabled: enabled.value } }))
      toast.success(t('reports.editor.created'))
    }
    await router.push(`/reports/${r.id}`)
  } catch (e) {
    err.error.value = e
    toast.error(errorMessage(e, t, te))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <form class="grid max-w-4xl gap-6" @submit.prevent="save">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h1 class="text-2xl font-semibold" data-testid="page-title">{{ report ? t('reports.editor.editTitle') : t('reports.add') }}</h1>
      <div class="flex gap-2">
        <Button type="button" variant="outline" as-child>
          <RouterLink :to="report ? `/reports/${report.id}` : '/reports'">{{ t('common.cancel') }}</RouterLink>
        </Button>
        <Button type="submit" :disabled="saving || !loaded" data-testid="report-save"><Save aria-hidden="true" />{{ t('common.save') }}</Button>
      </div>
    </div>

    <Alert v-if="loaded && queries.length === 0">
      <AlertDescription>
        {{ t('reports.editor.needQuery') }}
        <RouterLink to="/queries/new" class="underline">{{ t('queries.add') }}</RouterLink>
      </AlertDescription>
    </Alert>

    <section class="grid gap-4">
      <div class="grid gap-4 sm:grid-cols-2">
        <FormField id="report-title" :label="t('reports.fields.title')" :error="fieldErrors.title">
          <Input id="report-title" v-model="title" :placeholder="t('reports.editor.titlePlaceholder')" data-testid="report-title" />
        </FormField>
        <FormField id="report-slug" :label="t('queries.editor.slug')" :hint="t('reports.editor.slugHint')" :error="fieldErrors.slug">
          <Input id="report-slug" v-model="slug" @input="slugTouched = true" />
        </FormField>
      </div>
      <FormField id="report-description" :label="t('queries.editor.description')" :error="fieldErrors.description">
        <Input id="report-description" v-model="description" />
      </FormField>
    </section>

    <section class="grid gap-3">
      <h2 class="text-lg font-semibold">{{ t('reports.sections.query') }}</h2>
      <FormField id="report-query" :label="t('reports.fields.query')" :error="fieldErrors.query_id">
        <NativeSelect id="report-query" v-model="queryId" :options="queryOptions" />
      </FormField>
      <div v-if="query?.params.length" class="grid gap-3" data-testid="report-overrides">
        <p class="text-muted-foreground text-sm">{{ t('reports.editor.overridesHelp') }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField
            v-for="p in query.params"
            :id="`override-${p.name}`"
            :key="p.name"
            :label="`${p.name} (${t(`params.types.${p.type}`)})`"
            :hint="p.default != null ? t('reports.editor.defaultIs', { value: p.default }) : t('reports.editor.noDefault')"
            :error="fieldErrors[`param_overrides.${p.name}`]"
          >
            <Input :id="`override-${p.name}`" v-model="overrides[p.name]" :placeholder="p.default ?? ''" />
          </FormField>
        </div>
      </div>
    </section>

    <section class="grid gap-3">
      <h2 class="text-lg font-semibold">{{ t('reports.sections.schedule') }}</h2>
      <ScheduleBuilder v-model="cron" v-model:timezone="timezone" :cron-error="fieldErrors.cron" :timezone-error="fieldErrors.timezone" />
      <label v-if="!report" class="flex items-center gap-2 text-sm">
        <Switch id="report-enabled" v-model="enabled" />
        {{ t('reports.editor.enabled') }}
      </label>
    </section>

    <section class="grid gap-3">
      <h2 class="text-lg font-semibold">{{ t('reports.sections.conditions') }}</h2>
      <p class="text-muted-foreground text-sm">{{ t('conditions.help') }}</p>
      <ConditionEditor v-model="condition" :errors="fieldErrors" />
    </section>

    <details class="grid gap-3 rounded-md border p-4" data-testid="report-advanced">
      <summary class="cursor-pointer text-lg font-semibold">{{ t('reports.sections.advanced') }}</summary>
      <div class="mt-4 grid gap-4 sm:grid-cols-2">
        <FormField id="report-max-rows" :label="t('reports.fields.maxRows')" :hint="t('reports.editor.maxRowsHint')" :error="fieldErrors.max_rows">
          <Input id="report-max-rows" v-model="maxRows" type="number" min="1" />
        </FormField>
        <FormField id="report-auto-pause" :label="t('reports.fields.autoPauseAfter')" :hint="t('reports.editor.autoPauseHint')" :error="fieldErrors.auto_pause_after">
          <Input id="report-auto-pause" v-model.number="autoPause" type="number" min="0" max="100" />
        </FormField>
        <FormField id="report-retry-max" :label="t('reports.fields.retryMax')" :error="fieldErrors.retry_max">
          <Input id="report-retry-max" v-model.number="retryMax" type="number" min="0" max="10" />
        </FormField>
        <FormField id="report-retry-backoff" :label="t('reports.fields.retryBackoff')" :hint="t('reports.editor.retryBackoffHint')" :error="fieldErrors.retry_backoff_seconds">
          <Input id="report-retry-backoff" v-model.number="retryBackoff" type="number" min="1" max="3600" />
        </FormField>
        <FormField id="report-misfire" :label="t('reports.fields.misfire')" :hint="t('reports.editor.misfireHint')" :error="fieldErrors.misfire_policy">
          <NativeSelect id="report-misfire" v-model="misfire" :options="misfireOptions" />
        </FormField>
        <FormField id="report-overlap" :label="t('reports.fields.overlap')" :hint="t('reports.editor.overlapHint')" :error="fieldErrors.overlap_policy">
          <NativeSelect id="report-overlap" v-model="overlap" :options="overlapOptions" />
        </FormField>
        <label class="flex items-center gap-2 text-sm sm:col-span-2">
          <Switch id="report-notify-owner" v-model="notifyOwner" />
          {{ t('reports.fields.notifyOwner') }}
        </label>
      </div>
    </details>
  </form>
</template>
