<script setup lang="ts">
import { ChevronRight, Pencil, RefreshCw, Search, TriangleAlert } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import PluginIcon from '@/components/common/PluginIcon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ManagedNotice from '@/components/config/ManagedNotice.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ConnectionStatus from '@/components/plugin/ConnectionStatus.vue'
import TestResult from '@/components/plugin/TestResult.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { useFormat } from '@/composables/useFormat'
import { usePluginsStore } from '@/stores/plugins'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const router = useRouter()
const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const plugins = usePluginsStore()

const id = computed(() => String(route.params.id))
const conn = ref<Schemas['Connection'] | null>(null)
const schema = ref<Schemas['DatabaseSchema'] | null>(null)
const testResult = ref<Schemas['ConnectionTestResult'] | null>(null)
const search = ref('')
const open = ref<Set<string>>(new Set())
const busy = ref(false)
const deleteOpen = ref(false)
const isAdmin = computed(() => session.hasRole('admin'))

const qualified = (tb: Schemas['DatabaseTable']) => (tb.schema ? `${tb.schema}.${tb.name}` : tb.name)
const tables = computed(() => {
  const q = search.value.trim().toLowerCase()
  const all = schema.value?.tables ?? []
  if (!q) return all
  return all.filter((tb) => qualified(tb).toLowerCase().includes(q) || tb.columns.some((c) => c.name.toLowerCase().includes(q)))
})

async function load() {
  try {
    await plugins.load()
    const path = { params: { path: { connectionId: id.value } } }
    conn.value = unwrap(await api.GET('/api/v1/connections/{connectionId}', path))
    schema.value = unwrap(await api.GET('/api/v1/connections/{connectionId}/schema', path))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
onMounted(load)

async function runTest() {
  busy.value = true
  try {
    testResult.value = unwrap(await api.POST('/api/v1/connections/{connectionId}/test', { params: { path: { connectionId: id.value } } }))
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}

async function refresh() {
  busy.value = true
  try {
    schema.value = unwrap(await api.POST('/api/v1/connections/{connectionId}/schema/refresh', { params: { path: { connectionId: id.value } } }))
    toast.success(t('connections.schema.refreshed'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}

async function toggleExcluded(table: string, excluded: boolean) {
  if (!conn.value) return
  const set = new Set(conn.value.ai_excluded_tables)
  if (excluded) set.add(table)
  else set.delete(table)
  try {
    conn.value = unwrap(await api.PATCH('/api/v1/connections/{connectionId}', {
      params: { path: { connectionId: id.value } },
      body: { version: conn.value.version, ai_excluded_tables: [...set] },
    }))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function remove() {
  try {
    unwrap(await api.DELETE('/api/v1/connections/{connectionId}', { params: { path: { connectionId: id.value } } }))
    toast.success(t('connections.deleted'))
    await router.push('/connections')
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

function toggle(name: string) {
  const next = new Set(open.value)
  if (next.has(name)) next.delete(name)
  else next.add(name)
  open.value = next
}
</script>

<template>
  <div v-if="conn" class="grid gap-6">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="grid gap-1">
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ conn.name }}</h1>
        <div class="text-muted-foreground flex flex-wrap items-center gap-2 text-sm">
          <span class="inline-flex items-center gap-1.5"><PluginIcon :icon="plugins.connector(conn.driver)?.icon" :size="16" />{{ plugins.connector(conn.driver) ? t(plugins.connector(conn.driver)!.name) : conn.driver }}</span>
          <span v-if="conn.server_version">· {{ conn.server_version }}</span>
          <ConnectionStatus :status="conn.status" :error="conn.last_error" />
        </div>
      </div>
      <div class="flex flex-wrap gap-2">
        <Button v-if="isAdmin" variant="outline" :disabled="busy" data-testid="connection-test" @click="runTest">{{ t('connections.test.button') }}</Button>
        <Button v-if="isAdmin && conn.managed_by !== 'gitops'" variant="outline" as-child>
          <RouterLink :to="`/connections/${conn.id}/edit`" data-testid="connection-edit"><Pencil aria-hidden="true" />{{ t('connections.edit') }}</RouterLink>
        </Button>
        <Button v-if="isAdmin && conn.managed_by !== 'gitops'" variant="destructive" data-testid="connection-delete" @click="deleteOpen = true">{{ t('connections.delete') }}</Button>
      </div>
    </div>

    <ManagedNotice :id="conn.id" kind="connection" :managed-by="conn.managed_by" @changed="load" />
    <Alert v-if="conn.status === 'error'" variant="destructive">
      <AlertTitle>{{ t('connections.lastCheckFailed') }}</AlertTitle>
      <AlertDescription>{{ te(`errors.${conn.last_error}`) ? t(`errors.${conn.last_error}`) : conn.last_error }}</AlertDescription>
    </Alert>
    <Alert v-if="conn.has_write_permission" data-testid="write-warning">
      <TriangleAlert aria-hidden="true" />
      <AlertTitle>{{ t('connections.writeWarning.title') }}</AlertTitle>
      <AlertDescription>{{ t('connections.writeWarning.body') }}</AlertDescription>
    </Alert>
    <Alert v-if="conn.allow_multi_statement" variant="destructive">
      <AlertDescription>{{ t('connections.limits.multiWarning') }}</AlertDescription>
    </Alert>
    <TestResult v-if="testResult" :result="testResult" />

    <Card>
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-2">
        <div class="grid gap-1">
          <CardTitle as="h2">{{ t('connections.schema.title') }}</CardTitle>
          <CardDescription>
            {{ schema?.cached_at ? t('connections.schema.cachedAt', { when: fmt.dateTime(schema.cached_at) }) : t('connections.schema.never') }}
            {{ isAdmin ? t('connections.schema.aiHelp') : '' }}
          </CardDescription>
        </div>
        <Button v-if="session.hasRole('editor')" variant="outline" size="sm" :disabled="busy" data-testid="schema-refresh" @click="refresh">
          <RefreshCw aria-hidden="true" />{{ t('connections.schema.refresh') }}
        </Button>
      </CardHeader>
      <CardContent class="grid gap-3">
        <div class="relative sm:max-w-sm">
          <Search class="text-muted-foreground absolute top-2.5 left-2.5 size-4" aria-hidden="true" />
          <Input v-model="search" class="pl-8" :placeholder="t('connections.schema.search')" :aria-label="t('connections.schema.search')" data-testid="schema-search" />
        </div>
        <EmptyState v-if="(schema?.tables.length ?? 0) === 0" :icon="Search" :title="t('connections.schema.emptyTitle')" :description="t('connections.schema.emptyBody')" />
        <ul v-else class="divide-y rounded-md border" data-testid="schema-tables">
          <li v-for="tb in tables" :key="qualified(tb)" :data-testid="`table-${qualified(tb)}`">
            <div class="flex items-center gap-2 p-2">
              <button type="button" class="flex flex-1 items-center gap-2 text-left text-sm" :aria-expanded="open.has(qualified(tb))" @click="toggle(qualified(tb))">
                <ChevronRight class="size-4 transition-transform" :class="open.has(qualified(tb)) ? 'rotate-90' : ''" aria-hidden="true" />
                <span class="font-mono">{{ qualified(tb) }}</span>
                <Badge v-if="tb.kind === 'view'" variant="outline">{{ t('connections.schema.view') }}</Badge>
                <span class="text-muted-foreground text-xs">{{ t('connections.schema.columns', { n: tb.columns.length }) }}</span>
              </button>
              <label v-if="isAdmin" class="flex items-center gap-2 text-xs">
                <Checkbox
                  :model-value="conn.ai_excluded_tables.includes(qualified(tb))"
                  :data-testid="`exclude-${qualified(tb)}`"
                  @update:model-value="(v) => toggleExcluded(qualified(tb), v === true)"
                />
                {{ t('connections.schema.excludeFromAI') }}
              </label>
              <Badge v-else-if="conn.ai_excluded_tables.includes(qualified(tb))" variant="secondary">{{ t('connections.schema.excluded') }}</Badge>
            </div>
            <table v-if="open.has(qualified(tb))" class="mb-2 ml-8 text-xs">
              <tbody>
                <tr v-for="c in tb.columns" :key="c.name">
                  <td class="py-0.5 pr-4 font-mono">{{ c.name }}</td>
                  <td class="pr-4"><Badge variant="outline">{{ t(`connections.types.${c.type}`) }}</Badge></td>
                  <td class="text-muted-foreground pr-4 font-mono">{{ c.db_type }}{{ c.nullable ? '' : ' not null' }}</td>
                  <td class="text-muted-foreground">{{ c.comment }}</td>
                </tr>
              </tbody>
            </table>
          </li>
        </ul>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="t('connections.delete')"
      :description="t('connections.deleteImpact', { name: conn.name })"
      :confirm-label="t('connections.delete')"
      destructive
      :on-confirm="remove"
    />
  </div>
</template>
