<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'
import { useFormat } from '@/composables/useFormat'

/**
 * Storage & retention (docs/spec/06-ui.md, "Settings"): how long runs and their files are kept, how
 * much the files take, and the scheduled backups configured on the server (ROWBIRD_BACKUP_*).
 */
const { t } = useI18n()
const fmt = useFormat()
const err = useApiError()
const form = reactive({ retention_runs_days: 90, retention_artifacts_days: 30 })
const status = ref<Schemas['StorageStatus'] | null>(null)
const loaded = ref(false)
const busy = ref(false)

onMounted(async () => {
  try {
    const [s, st] = await Promise.all([api.GET('/api/v1/settings'), api.GET('/api/v1/system/storage')])
    const settings = unwrap(s)
    Object.assign(form, { retention_runs_days: settings.retention_runs_days, retention_artifacts_days: settings.retention_artifacts_days })
    status.value = unwrap(st)
    loaded.value = true
  } catch (e) {
    err.error.value = e
  }
})

async function save() {
  busy.value = true
  err.clear()
  try {
    unwrap(await api.PATCH('/api/v1/settings', { body: { retention_runs_days: Number(form.retention_runs_days), retention_artifacts_days: Number(form.retention_artifacts_days) } }))
    toast.success(t('settings.saved'))
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="grid gap-6" data-testid="storage-settings">
    <Card>
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.storage.retentionTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.storage.retentionHelp') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <form v-if="loaded" class="grid gap-4 sm:max-w-md" @submit.prevent="save">
          <FormField id="retention-runs" :label="t('settings.storage.runsDays')" :hint="t('settings.storage.runsDaysHint')" :error="err.field('retention_runs_days')">
            <Input id="retention-runs" v-model.number="form.retention_runs_days" type="number" min="1" max="3650" data-testid="retention-runs" />
          </FormField>
          <FormField id="retention-artifacts" :label="t('settings.storage.artifactsDays')" :hint="t('settings.storage.artifactsDaysHint')" :error="err.field('retention_artifacts_days')">
            <Input id="retention-artifacts" v-model.number="form.retention_artifacts_days" type="number" min="1" max="3650" data-testid="retention-artifacts" />
          </FormField>
          <p class="text-muted-foreground text-sm">{{ t('settings.storage.fixed') }}</p>
          <div><Button type="submit" :disabled="busy" data-testid="retention-save">{{ t('common.save') }}</Button></div>
        </form>
        <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm">{{ err.message.value }}</p>
      </CardContent>
    </Card>

    <Card v-if="status">
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.storage.usageTitle') }}</CardTitle>
      </CardHeader>
      <CardContent>
        <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
          <dt class="text-muted-foreground">{{ t('settings.storage.backend') }}</dt>
          <dd data-testid="storage-backend">{{ t(`settings.storage.backends.${status.backend}`) }}</dd>
          <dt class="text-muted-foreground">{{ t('settings.storage.used') }}</dt>
          <dd data-testid="storage-used">{{ fmt.size(status.artifact_bytes) }}</dd>
          <dt class="text-muted-foreground">{{ t('settings.storage.lastCleanup') }}</dt>
          <dd>{{ status.retention_last_run_at ? fmt.dateTime(status.retention_last_run_at) : t('settings.storage.never') }}</dd>
        </dl>
      </CardContent>
    </Card>

    <Card v-if="status" data-testid="backup-status">
      <CardHeader>
        <CardTitle as="h2">{{ t('settings.storage.backupTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.storage.backupHelp') }}</CardDescription>
      </CardHeader>
      <CardContent class="grid gap-4">
        <Alert v-if="!status.backup.available">
          <AlertDescription>{{ t('settings.storage.backupPostgres') }}</AlertDescription>
        </Alert>
        <p v-else-if="!status.backup.enabled" class="text-sm" data-testid="backup-off">{{ t('settings.storage.backupOff') }}</p>
        <template v-else>
          <Alert v-if="status.backup.last_error" variant="destructive" data-testid="backup-error">
            <AlertDescription>{{ t('settings.storage.backupFailed', { error: status.backup.last_error }) }}</AlertDescription>
          </Alert>
          <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
            <dt class="text-muted-foreground">{{ t('settings.storage.schedule') }}</dt>
            <dd class="font-mono">{{ status.backup.schedule }} (UTC)</dd>
            <dt class="text-muted-foreground">{{ t('settings.storage.destination') }}</dt>
            <dd>{{ t(`settings.storage.destinations.${status.backup.destination ?? 'local'}`, { keep: status.backup.keep ?? 0 }) }}</dd>
            <dt class="text-muted-foreground">{{ t('settings.storage.lastBackup') }}</dt>
            <dd data-testid="backup-last">
              <template v-if="status.backup.last_at">{{ fmt.dateTime(status.backup.last_at) }} · {{ status.backup.last_file }} · {{ fmt.size(status.backup.last_size_bytes ?? 0) }}</template>
              <template v-else>{{ t('settings.storage.never') }}</template>
            </dd>
            <dt class="text-muted-foreground">{{ t('settings.storage.nextBackup') }}</dt>
            <dd>{{ status.backup.next_at ? fmt.dateTime(status.backup.next_at) : '' }}</dd>
          </dl>
        </template>
        <p class="text-muted-foreground text-sm">{{ t('settings.storage.masterKeyReminder') }}</p>
      </CardContent>
    </Card>
  </div>
</template>
