<script setup lang="ts">
import { History, Play, Save, Sparkles } from '@lucide/vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { ApiError, errorMessage, unwrap } from '@/api/errors'
import QueryAssistant from '@/components/ai/QueryAssistant.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ManagedNotice from '@/components/config/ManagedNotice.vue'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import ParamsPanel from '@/components/query/ParamsPanel.vue'
import ResultsPanel from '@/components/query/ResultsPanel.vue'
import SchemaPanel from '@/components/query/SchemaPanel.vue'
import SqlEditor from '@/components/query/SqlEditor.vue'
import VersionsSheet from '@/components/query/VersionsSheet.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useApiError } from '@/composables/useApiError'
import { type Dialect, findParams } from '@/lib/params'
import { browserTimeZone } from '@/lib/timezones'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()
const session = useSessionStore()
const plugins = usePluginsStore()
const err = useApiError()
const previewErr = useApiError()

const canEdit = computed(() => session.hasRole('editor') && query.value?.managed_by !== 'gitops')
const query = ref<Schemas['Query'] | null>(null)
const connections = ref<Schemas['Connection'][]>([])
const tables = ref<Schemas['DatabaseTable'][]>([])
const title = ref('')
const slug = ref('')
const slugTouched = ref(false)
const description = ref('')
const connectionId = ref('')
const sql = ref('')
const params = ref<Schemas['QueryParam'][]>([])
const values = ref<Record<string, string>>({})
const result = ref<Schemas['PreviewResult'] | null>(null)
const running = ref(false)
const saving = ref(false)
const saveOpen = ref(false)
const note = ref('')
const versionsOpen = ref(false)
const deleteOpen = ref(false)
const assistantOpen = ref(false)
const editor = ref<InstanceType<typeof SqlEditor>>()

const connection = computed(() => connections.value.find((c) => c.id === connectionId.value))
const dialect = computed<Dialect>(() => {
  const caps = connection.value ? plugins.connector(connection.value.driver)?.capabilities : undefined
  return ((caps?.dialect as string) ?? 'postgres') as Dialect
})
const completion = computed(() => Object.fromEntries(tables.value.map((tb) => [tb.name, tb.columns.map((c) => c.name)])))
const connectionOptions = computed(() => connections.value.map((c) => ({ value: c.id, label: c.name })))
const fieldErrors = computed(() => {
  const out: Record<string, string | undefined> = {}
  for (const e of [err.error.value, previewErr.error.value]) {
    if (e instanceof ApiError) for (const k of Object.keys(e.fields)) out[k] = err.error.value === e ? err.field(k) : previewErr.field(k)
  }
  return out
})
const undefinedInSQL = computed(() => Object.keys(fieldErrors.value).filter((k) => k.startsWith('sql.')).map((k) => k.slice(4)))

/** Slug suggestion while the user has not edited it (accents removed, words joined by dashes). */
function slugify(s: string) {
  return s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 63)
}
watch(title, (v) => {
  if (!slugTouched.value && !query.value) slug.value = slugify(v)
})

async function loadSchema() {
  tables.value = []
  if (!connectionId.value) return
  try {
    tables.value = unwrap(await api.GET('/api/v1/connections/{connectionId}/schema', { params: { path: { connectionId: connectionId.value } } })).tables
  } catch {
    tables.value = []
  }
}
watch(connectionId, loadSchema)

function applyQuery(q: Schemas['Query']) {
  query.value = q
  title.value = q.title
  slug.value = q.slug
  description.value = q.description
  connectionId.value = q.connection_id
  sql.value = q.sql
  params.value = q.params
}

onMounted(async () => {
  try {
    await plugins.load()
    connections.value = unwrap(await api.GET('/api/v1/connections')).items
    if (typeof route.params.id === 'string') {
      applyQuery(unwrap(await api.GET('/api/v1/queries/{queryId}', { params: { path: { queryId: route.params.id } } })))
    } else if (connections.value.length) {
      connectionId.value = String(route.query.connection ?? connections.value[0]!.id)
    }
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

async function run() {
  if (!canEdit.value || !connectionId.value || running.value) return
  running.value = true
  previewErr.clear()
  try {
    result.value = unwrap(await api.POST('/api/v1/queries/preview', {
      body: { connection_id: connectionId.value, sql: sql.value, params: params.value, values: values.value, timezone: browserTimeZone() },
    }))
  } catch (e) {
    previewErr.error.value = e
    result.value = null
  } finally {
    running.value = false
  }
}

// Only definitions of parameters still used in the SQL are saved.
const savedParams = computed(() => {
  const used = findParams(dialect.value, sql.value)
  return params.value.filter((p) => used.includes(p.name))
})

async function save() {
  saving.value = true
  err.clear()
  try {
    if (query.value) {
      const before = query.value.current_version
      const q = unwrap(await api.PATCH('/api/v1/queries/{queryId}', {
        params: { path: { queryId: query.value.id } },
        body: { version: query.value.version, title: title.value, slug: slug.value, description: description.value, connection_id: connectionId.value, sql: sql.value, params: savedParams.value, note: note.value },
      }))
      applyQuery(q)
      toast.success(q.current_version > before ? t('queries.editor.savedVersion', { n: q.current_version }) : t('queries.editor.saved'))
    } else {
      const q = unwrap(await api.POST('/api/v1/queries', {
        body: { title: title.value, slug: slug.value || undefined, description: description.value, connection_id: connectionId.value, sql: sql.value, params: savedParams.value, note: note.value },
      }))
      toast.success(t('queries.editor.created'))
      await router.replace(`/queries/${q.id}`)
      applyQuery(q)
    }
    saveOpen.value = false
    note.value = ''
  } catch (e) {
    err.error.value = e
    saveOpen.value = false
  } finally {
    saving.value = false
  }
}

async function remove() {
  if (!query.value) return
  try {
    unwrap(await api.DELETE('/api/v1/queries/{queryId}', { params: { path: { queryId: query.value.id } } }))
    toast.success(t('queries.editor.deleted'))
    await router.push('/queries')
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

/** Ctrl/Cmd+I opens the AI panel anywhere on the page (the SQL editor emits it too). */
function onShortcut(e: KeyboardEvent) {
  if (canEdit.value && (e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 'i') {
    e.preventDefault()
    assistantOpen.value = true
  }
}
onMounted(() => window.addEventListener('keydown', onShortcut))
onBeforeUnmount(() => window.removeEventListener('keydown', onShortcut))

/** Applies a proposal: the SQL, the parameters it adds and, for a new query, the title. */
function applyProposal(p: Schemas['AIProposal']) {
  sql.value = p.sql
  const known = new Set(params.value.map((x) => x.name))
  params.value = [...params.value, ...p.params.filter((x) => !known.has(x.name))]
  if (!title.value.trim() && p.suggested_name) title.value = p.suggested_name
  result.value = null
  toast.success(t('ai.panel.applied'))
}

async function reload() {
  if (!query.value) return
  try {
    applyQuery(unwrap(await api.GET('/api/v1/queries/{queryId}', { params: { path: { queryId: query.value.id } } })))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

function onRestored(q: Schemas['Query']) {
  applyQuery(q)
  versionsOpen.value = false
}
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="grid min-w-0 flex-1 gap-2">
        <Input
          id="query-title"
          v-model="title"
          class="h-10 text-lg font-semibold"
          :placeholder="t('queries.editor.titlePlaceholder')"
          :aria-label="t('queries.editor.title')"
          :readonly="!canEdit"
          data-testid="query-title"
        />
        <p v-if="err.field('title')" class="text-destructive text-sm">{{ err.field('title') }}</p>
        <p v-if="query" class="text-muted-foreground text-xs">
          {{ t('queries.editor.meta', { n: query.current_version, author: query.author_name }) }}
          <span v-if="query.report_count"> · {{ t('queries.usedBy', { n: query.report_count }, query.report_count) }}</span>
        </p>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button v-if="query" variant="outline" data-testid="versions-open" @click="versionsOpen = true"><History aria-hidden="true" />{{ t('queries.versions.title') }}</Button>
        <Button v-if="canEdit" variant="outline" :title="t('ai.panel.shortcut')" data-testid="ai-open" @click="assistantOpen = true">
          <Sparkles aria-hidden="true" />{{ t('ai.panel.title') }}
        </Button>
        <Button v-if="canEdit" variant="outline" :disabled="running || !connectionId" data-testid="query-run" :title="t('queries.editor.runShortcut')" @click="run">
          <Play aria-hidden="true" />{{ running ? t('queries.editor.running') : t('queries.editor.run') }}
        </Button>
        <Button v-if="canEdit" :disabled="saving" data-testid="query-save" @click="saveOpen = true"><Save aria-hidden="true" />{{ t('common.save') }}</Button>
      </div>
    </div>

    <ManagedNotice v-if="query" :id="query.id" kind="query" :managed-by="query.managed_by" @changed="reload" />
    <Alert v-if="connections.length === 0 && !query">
      <AlertDescription>
        {{ t('queries.editor.needConnection') }}
        <RouterLink to="/connections" class="underline">{{ t('nav.connections') }}</RouterLink>
      </AlertDescription>
    </Alert>

    <div class="grid items-start gap-4 lg:grid-cols-[16rem_1fr]">
      <aside class="order-2 h-96 lg:sticky lg:top-4 lg:order-1 lg:h-[calc(100vh-8rem)]">
        <SchemaPanel :tables="tables" @insert="(text) => editor?.insert(text)" />
      </aside>
      <div class="order-1 grid min-w-0 content-start gap-4 lg:order-2">
        <div class="grid items-start gap-4 sm:grid-cols-3">
          <FormField id="query-connection" :label="t('queries.editor.connection')" :error="err.field('connection_id')">
            <NativeSelect id="query-connection" v-model="connectionId" :options="connectionOptions" :disabled="!canEdit" />
          </FormField>
          <FormField id="query-slug" :label="t('queries.editor.slug')" :hint="t('queries.editor.slugHint')" :error="err.field('slug')">
            <Input id="query-slug" v-model="slug" :readonly="!canEdit" @input="slugTouched = true" />
          </FormField>
          <FormField id="query-description" :label="t('queries.editor.description')" :error="err.field('description')">
            <Input id="query-description" v-model="description" :readonly="!canEdit" />
          </FormField>
        </div>
        <div class="h-72">
          <SqlEditor
            ref="editor"
            v-model="sql"
            :dialect="dialect"
            :schema="completion"
            :readonly="!canEdit"
            :label="t('queries.editor.sql')"
            :placeholder-text="t('queries.editor.sqlPlaceholder')"
            @run="run"
            @assistant="assistantOpen = true"
          />
        </div>
        <p v-if="err.field('sql')" class="text-destructive text-sm">{{ err.field('sql') }}</p>
        <p v-if="undefinedInSQL.length" class="text-destructive text-sm">{{ t('queries.editor.undefinedParams', { names: undefinedInSQL.join(', ') }) }}</p>

        <section class="grid gap-2">
          <h2 class="text-sm font-semibold">{{ t('params.title') }}</h2>
          <ParamsPanel v-model:params="params" v-model:values="values" :dialect="dialect" :sql="sql" :readonly="!canEdit" :errors="fieldErrors" />
        </section>

        <section class="grid gap-2">
          <h2 class="text-sm font-semibold">{{ t('results.title') }}</h2>
          <p v-if="previewErr.message.value" class="text-destructive text-sm" role="alert" data-testid="preview-error">{{ previewErr.message.value }}</p>
          <ResultsPanel v-if="result" :result="result" />
          <p v-else-if="!previewErr.message.value" class="text-muted-foreground text-sm">{{ canEdit ? t('results.hint') : t('results.viewerHint') }}</p>
        </section>

        <div v-if="query && canEdit">
          <Button variant="ghost" class="text-destructive" data-testid="query-delete" @click="deleteOpen = true">{{ t('queries.editor.delete') }}</Button>
        </div>
      </div>
    </div>

    <Dialog v-model:open="saveOpen">
      <DialogContent>
        <form class="grid gap-4" @submit.prevent="save">
          <DialogHeader>
            <DialogTitle>{{ t('queries.editor.saveTitle') }}</DialogTitle>
            <DialogDescription>{{ query ? t('queries.editor.saveHelp') : t('queries.editor.saveFirstHelp') }}</DialogDescription>
          </DialogHeader>
          <Alert v-if="query?.report_count">
            <AlertDescription>{{ t('queries.editor.saveImpact', { n: query.report_count }, query.report_count) }}</AlertDescription>
          </Alert>
          <FormField id="version-note" :label="t('queries.editor.note')" :hint="t('queries.editor.noteHint')">
            <Input id="version-note" v-model="note" />
          </FormField>
          <DialogFooter>
            <Button type="submit" :disabled="saving" data-testid="query-save-confirm">{{ t('common.save') }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Sheet v-model:open="versionsOpen">
      <SheetContent side="right" class="w-full overflow-y-auto sm:max-w-3xl">
        <SheetHeader>
          <SheetTitle>{{ t('queries.versions.title') }}</SheetTitle>
          <SheetDescription>{{ t('queries.versions.help') }}</SheetDescription>
        </SheetHeader>
        <div class="px-4 pb-4">
          <VersionsSheet v-if="query && versionsOpen" :query="query" :can-restore="canEdit" @restored="onRestored" />
        </div>
      </SheetContent>
    </Sheet>

    <QueryAssistant v-if="canEdit" v-model:open="assistantOpen" :connection-id="connectionId" :sql="sql" @apply="applyProposal" />

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="t('queries.editor.delete')"
      :description="t('queries.editor.deleteImpact', { title: query?.title ?? '', n: query?.current_version ?? 0 })"
      :confirm-label="t('queries.editor.delete')"
      destructive
      :on-confirm="remove"
    />
  </div>
</template>
