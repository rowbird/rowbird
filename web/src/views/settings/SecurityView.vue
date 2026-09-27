<script setup lang="ts">
import { ShieldCheck } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import OIDCSettingsCard from '@/components/settings/OIDCSettingsCard.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'

const { t, te } = useI18n()
const fmt = useFormat()

const require2fa = ref(false)
const confirmRequire = ref(false)

const EVENT_TYPES = [
  'login_success', 'login_failed', 'login_locked', 'logout', 'password_changed', 'password_reset',
  '2fa_enabled', '2fa_disabled', 'recovery_codes_regenerated', 'recovery_code_used', 'session_revoked',
  'user_created', 'user_updated', 'user_role_changed', 'user_disabled', 'user_enabled',
  'api_key_created', 'api_key_revoked', 'settings_changed', 'setup_completed',
  'connection_created', 'connection_updated', 'connection_deleted', 'keys_rotated',
  'passkey_added', 'passkey_removed', 'oidc_linked', 'user_provisioned', 'password_reset_requested',
]
const typeFilter = ref('')
const typeOptions = [{ value: '', label: t('settings.security.allEvents') }, ...EVENT_TYPES.map((e) => ({ value: e, label: eventLabel(e) }))]
const events = ref<Schemas['SecurityEvent'][]>([])
const nextCursor = ref<string | null>(null)

function eventLabel(type: string) {
  return te(`events.${type}`) ? t(`events.${type}`) : type
}

async function loadSettings() {
  require2fa.value = unwrap(await api.GET('/api/v1/settings')).require_2fa
}

async function loadEvents(more = false) {
  try {
    const page = unwrap(await api.GET('/api/v1/security-events', {
      params: { query: { type: typeFilter.value || undefined, cursor: more ? (nextCursor.value ?? undefined) : undefined } },
    }))
    events.value = more ? [...events.value, ...page.items] : page.items
    nextCursor.value = page.next_cursor ?? null
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function setRequire2fa(value: boolean) {
  try {
    require2fa.value = unwrap(await api.PATCH('/api/v1/settings', { body: { require_2fa: value } })).require_2fa
    toast.success(t('settings.saved'))
    await loadEvents()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

function onSwitch(value: boolean) {
  // Turning the requirement on affects everyone without 2FA, so it is confirmed first.
  if (value) confirmRequire.value = true
  else void setRequire2fa(false)
}

function summary(e: Schemas['SecurityEvent']) {
  return Object.entries(e.meta)
    .map(([k, v]) => `${k}: ${Array.isArray(v) ? v.join(', ') : String(v)}`)
    .join(' · ')
}

onMounted(async () => {
  try {
    await loadSettings()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
  await loadEvents()
})
</script>

<template>
  <div class="grid gap-6">
    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.security.require2faTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.security.require2faHelp') }}</CardDescription>
      </CardHeader>
      <CardContent class="flex items-center gap-3">
        <Switch id="require-2fa" :model-value="require2fa" data-testid="require-2fa" @update:model-value="onSwitch" />
        <Label for="require-2fa">{{ t('settings.security.require2fa') }}</Label>
      </CardContent>
    </Card>

    <OIDCSettingsCard />

    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.security.eventsTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.security.eventsHelp') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-3">
        <div class="sm:max-w-xs">
          <NativeSelect id="event-type" v-model="typeFilter" :options="typeOptions" :aria-label="t('settings.security.filter')" @update:model-value="loadEvents()" />
        </div>
        <EmptyState v-if="events.length === 0" :icon="ShieldCheck" :title="t('settings.security.emptyTitle')" :description="t('settings.security.emptyBody')" />
        <div v-else class="overflow-x-auto rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{{ t('settings.security.when') }}</TableHead>
                <TableHead>{{ t('settings.security.event') }}</TableHead>
                <TableHead>{{ t('settings.security.ip') }}</TableHead>
                <TableHead>{{ t('settings.security.details') }}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="e in events" :key="e.id" data-testid="security-event">
                <TableCell class="text-sm whitespace-nowrap">{{ fmt.dateTime(e.created_at) }}</TableCell>
                <TableCell class="text-sm">{{ eventLabel(e.type) }}</TableCell>
                <TableCell class="text-muted-foreground font-mono text-xs">{{ e.ip }}</TableCell>
                <TableCell class="text-muted-foreground text-xs">{{ summary(e) }}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
        <div v-if="nextCursor"><Button variant="outline" @click="loadEvents(true)">{{ t('common.loadMore') }}</Button></div>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="confirmRequire"
      :title="t('settings.security.require2fa')"
      :description="t('settings.security.require2faImpact')"
      :confirm-label="t('settings.security.require2faConfirm')"
      :on-confirm="() => setRequire2fa(true)"
    />
  </div>
</template>
