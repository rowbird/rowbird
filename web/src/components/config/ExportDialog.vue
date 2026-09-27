<script setup lang="ts">
import { Download } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import { downloadText } from '@/components/config/download'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

/**
 * Exports reports (each with its query) as YAML; no reports means the whole workspace. Secrets
 * never leave: the file has ${env:...} placeholders for them.
 */
const props = defineProps<{ reports?: string[] }>()
const open = defineModel<boolean>('open', { required: true })
const { t, te } = useI18n()
const withConnections = ref(true)
const withChannels = ref(true)
const busy = ref(false)

async function download() {
  busy.value = true
  try {
    const text = unwrap(await api.POST('/api/v1/export', {
      body: { reports: props.reports?.length ? props.reports : undefined, include_connections: withConnections.value, include_channels: withChannels.value },
      parseAs: 'text',
    })) as unknown as string
    const name = props.reports?.length === 1 ? `${props.reports[0]}.yaml` : 'rowbird-export.yaml'
    downloadText(name, text)
    open.value = false
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent data-testid="export-dialog">
      <DialogHeader>
        <DialogTitle>{{ reports?.length ? t('config.export.titleSome', { n: reports.length }, reports.length) : t('config.export.titleAll') }}</DialogTitle>
        <DialogDescription>{{ t('config.export.help') }}</DialogDescription>
      </DialogHeader>
      <div class="grid gap-3 text-sm">
        <label class="flex items-center gap-2"><Checkbox v-model="withConnections" data-testid="export-connections" />{{ t('config.export.withConnections') }}</label>
        <label class="flex items-center gap-2"><Checkbox v-model="withChannels" data-testid="export-channels" />{{ t('config.export.withChannels') }}</label>
        <p class="text-muted-foreground text-xs">{{ t('config.export.secrets') }}</p>
      </div>
      <DialogFooter>
        <Button :disabled="busy" data-testid="export-download" @click="download"><Download aria-hidden="true" />{{ t('config.export.download') }}</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
