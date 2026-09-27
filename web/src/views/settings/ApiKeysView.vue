<script setup lang="ts">
import { KeySquare, Plus } from '@lucide/vue'
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import CopyField from '@/components/common/CopyField.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import FormField from '@/components/common/FormField.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useApiError } from '@/composables/useApiError'
import { useFormat } from '@/composables/useFormat'

type Scope = Schemas['Scope']
const SCOPES: Scope[] = ['read', 'run', 'write', 'admin']

const { t, te } = useI18n()
const fmt = useFormat()
const keys = ref<Schemas['ApiKey'][]>([])
const nextCursor = ref<string | null>(null)

async function load(more = false) {
  try {
    const page = unwrap(await api.GET('/api/v1/api-keys', { params: { query: { cursor: more ? (nextCursor.value ?? undefined) : undefined } } }))
    keys.value = more ? [...keys.value, ...page.items] : page.items
    nextCursor.value = page.next_cursor ?? null
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
onMounted(() => load())

const createOpen = ref(false)
const form = reactive({ name: '', scopes: ['read'] as Scope[], expires: '' })
const createErr = useApiError()
const busy = ref(false)
const created = ref<string | null>(null)

function toggleScope(scope: Scope, on: boolean | 'indeterminate') {
  form.scopes = on === true ? [...new Set([...form.scopes, scope])] : form.scopes.filter((s) => s !== scope)
}

function openCreate() {
  Object.assign(form, { name: '', scopes: ['read'], expires: '' })
  createErr.clear()
  createOpen.value = true
}

async function create() {
  busy.value = true
  createErr.clear()
  try {
    const expires_at = form.expires ? new Date(`${form.expires}T23:59:59`).toISOString() : undefined
    const res = unwrap(await api.POST('/api/v1/api-keys', { body: { name: form.name, scopes: form.scopes, expires_at } }))
    createOpen.value = false
    created.value = res.key
    await load()
  } catch (e) {
    createErr.error.value = e
  } finally {
    busy.value = false
  }
}

const revoking = ref<Schemas['ApiKey'] | null>(null)
async function revoke() {
  if (!revoking.value) return
  try {
    unwrap(await api.POST('/api/v1/api-keys/{apiKeyId}/revoke', { params: { path: { apiKeyId: revoking.value.id } } }))
    toast.success(t('settings.apiKeys.revoked'))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
  await load()
}
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="text-muted-foreground text-sm">{{ t('settings.apiKeys.help') }}</p>
      <Button v-if="keys.length > 0" data-testid="api-key-create" @click="openCreate">
        <Plus aria-hidden="true" />
        {{ t('settings.apiKeys.create') }}
      </Button>
    </div>

    <EmptyState v-if="keys.length === 0" :icon="KeySquare" :title="t('settings.apiKeys.emptyTitle')" :description="t('settings.apiKeys.emptyBody')">
      <Button data-testid="api-key-create" @click="openCreate">
        <Plus aria-hidden="true" />
        {{ t('settings.apiKeys.create') }}
      </Button>
    </EmptyState>
    <div v-else class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('fields.name') }}</TableHead>
            <TableHead>{{ t('settings.apiKeys.scopes') }}</TableHead>
            <TableHead>{{ t('settings.apiKeys.lastUsed') }}</TableHead>
            <TableHead>{{ t('settings.apiKeys.expires') }}</TableHead>
            <TableHead><span class="sr-only">{{ t('common.actions') }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="k in keys" :key="k.id" :data-testid="`api-key-row-${k.name}`">
            <TableCell>
              <div class="font-medium">{{ k.name }}</div>
              <code class="text-muted-foreground text-xs">rbk_{{ k.prefix }}…</code>
            </TableCell>
            <TableCell class="flex flex-wrap gap-1">
              <Badge v-for="s in k.scopes" :key="s" variant="outline">{{ t(`scopes.${s}`) }}</Badge>
            </TableCell>
            <TableCell class="text-muted-foreground text-sm">{{ k.last_used_at ? fmt.dateTime(k.last_used_at) : t('settings.users.never') }}</TableCell>
            <TableCell class="text-muted-foreground text-sm">{{ k.expires_at ? fmt.dateTime(k.expires_at) : t('settings.apiKeys.noExpiry') }}</TableCell>
            <TableCell class="text-right">
              <Badge v-if="!k.active" variant="secondary">{{ t('settings.apiKeys.inactive') }}</Badge>
              <Button v-else variant="ghost" size="sm" class="text-destructive" :data-testid="`api-key-revoke-${k.name}`" @click="revoking = k">
                {{ t('settings.apiKeys.revoke') }}
              </Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
    <div v-if="nextCursor"><Button variant="outline" @click="load(true)">{{ t('common.loadMore') }}</Button></div>

    <Dialog v-model:open="createOpen">
      <DialogContent>
        <form class="grid gap-4" @submit.prevent="create">
          <DialogHeader>
            <DialogTitle>{{ t('settings.apiKeys.create') }}</DialogTitle>
            <DialogDescription>{{ t('settings.apiKeys.createHelp') }}</DialogDescription>
          </DialogHeader>
          <FormField id="key-name" :label="t('fields.name')" :error="createErr.field('name')">
            <Input id="key-name" v-model="form.name" required />
          </FormField>
          <fieldset class="grid gap-2">
            <legend class="mb-1 text-sm font-medium">{{ t('settings.apiKeys.scopes') }}</legend>
            <div v-for="s in SCOPES" :key="s" class="flex items-start gap-2">
              <Checkbox :id="`scope-${s}`" :model-value="form.scopes.includes(s)" @update:model-value="(v) => toggleScope(s, v)" />
              <Label :for="`scope-${s}`" class="grid gap-0.5 font-normal">
                <span class="font-medium">{{ t(`scopes.${s}`) }}</span>
                <span class="text-muted-foreground text-xs">{{ t(`scopes.${s}Help`) }}</span>
              </Label>
            </div>
            <p v-if="createErr.field('scopes')" class="text-destructive text-sm">{{ createErr.field('scopes') }}</p>
          </fieldset>
          <FormField id="key-expires" :label="t('settings.apiKeys.expiresOn')" :hint="t('settings.apiKeys.expiresHint')" :error="createErr.field('expires_at')">
            <Input id="key-expires" v-model="form.expires" type="date" />
          </FormField>
          <p v-if="createErr.message.value && !createErr.hasFieldErrors.value" class="text-destructive text-sm">{{ createErr.message.value }}</p>
          <DialogFooter>
            <Button type="submit" :disabled="busy || form.scopes.length === 0" data-testid="api-key-create-submit">{{ t('settings.apiKeys.create') }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="created !== null" @update:open="(v) => { if (!v) created = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('settings.apiKeys.createdTitle') }}</DialogTitle>
          <DialogDescription>{{ t('settings.apiKeys.createdHelp') }}</DialogDescription>
        </DialogHeader>
        <Alert>
          <AlertDescription>{{ t('common.shownOnce') }}</AlertDescription>
        </Alert>
        <CopyField v-if="created" :value="created" :label="t('settings.apiKeys.key')" />
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      :open="revoking !== null"
      :title="t('settings.apiKeys.revoke')"
      :description="t('settings.apiKeys.revokeImpact', { name: revoking?.name })"
      :confirm-label="t('settings.apiKeys.revoke')"
      destructive
      :on-confirm="revoke"
      @update:open="(v: boolean) => { if (!v) revoking = null }"
    />
  </div>
</template>
