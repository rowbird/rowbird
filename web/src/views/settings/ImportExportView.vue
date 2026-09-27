<script setup lang="ts">
import { Download, FileUp, RefreshCw } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ExportDialog from '@/components/config/ExportDialog.vue'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

/**
 * Import wizard (docs/spec/06-ui.md): the YAML, then what blocks it (errors with their lines,
 * missing connections or channels to map, secrets to fill, conflicts to decide), then the plan.
 * Every change runs a dry run, which applies and rolls back, so the plan shows every error a real
 * apply would. Apply is one transaction.
 */
type Policy = Schemas['ImportPolicy']

const { t, te } = useI18n()
const yaml = ref('')
const fileName = ref('')
const result = ref<Schemas['ImportResult'] | null>(null)
const policies = ref<Record<string, Policy>>({})
const mapping = ref<Record<string, string>>({})
const secrets = ref<Record<string, string>>({})
const busy = ref(false)
const applied = ref(false)
const exportOpen = ref(false)
const connections = ref<Schemas['Connection'][]>([])
const channels = ref<Schemas['Channel'][]>([])

onMounted(async () => {
  try {
    connections.value = unwrap(await api.GET('/api/v1/connections')).items
    channels.value = unwrap(await api.GET('/api/v1/channels')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
})

async function readFile(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  fileName.value = file.name
  yaml.value = await file.text()
  await analyze()
}

const key = (it: { kind: string; name: string }) => `${it.kind}/${it.name}`

async function run(dryRun: boolean) {
  busy.value = true
  try {
    const res = unwrap(await api.POST('/api/v1/import', {
      body: { yaml: yaml.value, dry_run: dryRun, items: policies.value, map: mapping.value, secrets: secrets.value },
    }))
    result.value = res
    return res
  } catch (e) {
    toast.error(errorMessage(e, t, te))
    return null
  } finally {
    busy.value = false
  }
}

async function analyze() {
  applied.value = false
  if (yaml.value.trim()) await run(true)
}

async function apply() {
  const res = await run(false)
  if (res?.applied) {
    applied.value = true
    toast.success(t('config.import.applied'))
  }
}

function setPolicy(item: Schemas['ImportItem'], policy: string) {
  policies.value = { ...policies.value, [key(item)]: policy as Policy }
  void analyze()
}

function setMapping(name: string, to: string) {
  mapping.value = { ...mapping.value, [name]: to }
  void analyze()
}

const pendingSecrets = computed(() => (result.value?.secrets ?? []).filter((s) => s.required && !s.provided))
const conflicts = computed(() => (result.value?.items ?? []).filter((i) => i.action === 'conflict'))
const blocked = computed(() => {
  const r = result.value
  return !r || r.errors.length > 0 || r.missing.length > 0 || pendingSecrets.value.length > 0 || conflicts.value.length > 0
})
const counts = computed(() => {
  const out: Record<string, number> = {}
  for (const i of result.value?.items ?? []) out[i.action] = (out[i.action] ?? 0) + 1
  return out
})
/** Items the user can decide on: those that exist with differences. */
const decidable = (i: Schemas['ImportItem']) => i.changes.length > 0 || i.action === 'skip' || i.target !== i.name || !!i.policy
const policyOptions = computed(() => (['overwrite', 'copy', 'skip'] as const).map((p) => ({ value: p, label: t(`config.import.policies.${p}`) })))
function mapOptions(kind: string) {
  const names = kind === 'Connection' ? connections.value.map((c) => c.name) : channels.value.map((c) => c.name)
  return [{ value: '', label: t('config.import.choose') }, ...names.map((n) => ({ value: n, label: n }))]
}
const actionVariant = (a: string) => (a === 'conflict' ? 'destructive' : a === 'unchanged' || a === 'skip' ? 'secondary' : 'default')
const show = (v: unknown) => (v === undefined || v === null ? t('config.import.none') : JSON.stringify(v))
</script>

<template>
  <div class="grid gap-6" data-testid="import-export">
    <Card>
      <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-2">
        <div class="grid gap-1">
          <CardTitle as="h2">{{ t('config.title') }}</CardTitle>
          <CardDescription>{{ t('config.help') }}</CardDescription>
        </div>
        <Button variant="outline" data-testid="export-all" @click="exportOpen = true"><Download aria-hidden="true" />{{ t('config.export.all') }}</Button>
      </CardHeader>
      <CardContent class="grid gap-4">
        <div class="flex flex-wrap items-center gap-2">
          <label class="border-input hover:bg-muted inline-flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm">
            <FileUp class="size-4" aria-hidden="true" />{{ t('config.import.file') }}
            <input type="file" accept=".yaml,.yml,text/yaml" class="sr-only" data-testid="import-file" @change="readFile">
          </label>
          <span v-if="fileName" class="text-muted-foreground text-sm">{{ fileName }}</span>
        </div>
        <FormField id="import-yaml" :label="t('config.import.paste')">
          <textarea
            id="import-yaml"
            v-model="yaml"
            rows="10"
            spellcheck="false"
            class="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:ring-[3px]"
            data-testid="import-yaml"
          />
        </FormField>
        <div>
          <Button :disabled="busy || !yaml.trim()" data-testid="import-analyze" @click="analyze"><RefreshCw aria-hidden="true" />{{ t('config.import.analyze') }}</Button>
        </div>
      </CardContent>
    </Card>

    <template v-if="result">
      <Alert v-if="result.errors.length" variant="destructive" data-testid="import-errors">
        <AlertDescription>
          <p class="mb-1 font-medium">{{ t('config.import.errors') }}</p>
          <ul class="grid gap-1 font-mono text-xs">
            <li v-for="(e, i) in result.errors" :key="i">{{ t('config.import.line', { line: e.pos.line }) }}: {{ e.message }}</li>
          </ul>
        </AlertDescription>
      </Alert>

      <Card v-if="result.missing.length" data-testid="import-missing">
        <CardHeader>
          <CardTitle as="h3">{{ t('config.import.missingTitle') }}</CardTitle>
          <CardDescription>{{ t('config.import.missingHelp') }}</CardDescription>
        </CardHeader>
        <CardContent class="grid gap-3 sm:grid-cols-2">
          <FormField v-for="m in result.missing" :id="`map-${m.name}`" :key="key(m)" :label="t(`config.import.kinds.${m.kind}`) + ' ' + m.name">
            <NativeSelect :id="`map-${m.name}`" :model-value="mapping[m.name] ?? ''" :options="mapOptions(m.kind)" @update:model-value="(v) => setMapping(m.name, v)" />
          </FormField>
        </CardContent>
      </Card>

      <Card v-if="result.secrets.length" data-testid="import-secrets">
        <CardHeader>
          <CardTitle as="h3">{{ t('config.import.secretsTitle') }}</CardTitle>
          <CardDescription>{{ t('config.import.secretsHelp') }}</CardDescription>
        </CardHeader>
        <CardContent class="grid gap-3 sm:grid-cols-2">
          <FormField
            v-for="s in result.secrets"
            :id="`secret-${s.env}`"
            :key="s.env"
            :label="`${t(`config.import.kinds.${s.kind}`)} ${s.name}: ${s.field}`"
            :hint="s.required ? t('config.import.secretRequired', { env: s.env }) : t('config.import.secretOptional', { env: s.env })"
          >
            <Input :id="`secret-${s.env}`" v-model="secrets[s.env]" type="password" autocomplete="off" @change="analyze" />
          </FormField>
        </CardContent>
      </Card>

      <Card v-if="result.items.length" data-testid="import-plan">
        <CardHeader>
          <CardTitle as="h3">{{ t('config.import.planTitle') }}</CardTitle>
          <CardDescription>
            {{ t('config.import.summary', { create: counts.create ?? 0, update: counts.update ?? 0, unchanged: counts.unchanged ?? 0, skip: counts.skip ?? 0, conflict: counts.conflict ?? 0 }) }}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ul class="divide-y rounded-md border">
            <li v-for="it in result.items" :key="key(it)" class="grid gap-2 p-3 text-sm" :data-testid="`import-item-${it.kind}-${it.name}`">
              <div class="flex flex-wrap items-center gap-2">
                <Badge :variant="actionVariant(it.action)">{{ t(`config.import.actions.${it.action}`) }}</Badge>
                <span class="text-muted-foreground text-xs">{{ t(`config.import.kinds.${it.kind}`) }}</span>
                <span class="font-medium">{{ it.name }}</span>
                <span v-if="it.target !== it.name" class="text-muted-foreground text-xs">{{ t('config.import.as', { name: it.target }) }}</span>
                <NativeSelect
                  v-if="decidable(it)"
                  class="ml-auto w-44"
                  :model-value="policies[key(it)] ?? ''"
                  :options="[{ value: '', label: t('config.import.decide') }, ...policyOptions]"
                  :data-testid="`import-policy-${it.kind}-${it.name}`"
                  @update:model-value="(v) => setPolicy(it, v)"
                />
              </div>
              <ul v-if="it.changes.length" class="text-muted-foreground grid gap-0.5 font-mono text-xs">
                <li v-for="c in it.changes" :key="c.field">
                  {{ c.field }}: <template v-if="c.secret">{{ t('config.import.secretChanges') }}</template>
                  <template v-else>{{ show(c.from) }} → {{ show(c.to) }}</template>
                </li>
              </ul>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Alert v-if="applied" data-testid="import-done">
        <AlertDescription>{{ t('config.import.done') }}</AlertDescription>
      </Alert>
      <div v-else-if="result.items.length" class="flex flex-wrap items-center gap-3">
        <Button :disabled="busy || blocked" data-testid="import-apply" @click="apply">{{ t('config.import.apply') }}</Button>
        <span v-if="blocked" class="text-muted-foreground text-sm">{{ t('config.import.blocked') }}</span>
      </div>
    </template>

    <ExportDialog v-model:open="exportOpen" />
  </div>
</template>
