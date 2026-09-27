<script setup lang="ts">
import { GitBranch } from '@lucide/vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useSessionStore } from '@/stores/session'

/**
 * Tells that the configuration directory manages a resource, so it is read only here, and lets an
 * admin detach it (or give a detached one back).
 */
const props = defineProps<{ kind: Schemas['ManagedResource']['kind']; id: string; managedBy: Schemas['ManagedBy'] }>()
const emit = defineEmits<{ changed: [] }>()
const { t, te } = useI18n()
const session = useSessionStore()
const confirmOpen = ref(false)

async function change(attach: boolean) {
  try {
    unwrap(await api.POST(attach ? '/api/v1/gitops/attach' : '/api/v1/gitops/detach', { body: { kind: props.kind, id: props.id } }))
    toast.success(attach ? t('config.managed.attached') : t('config.managed.detached'))
    emit('changed')
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
</script>

<template>
  <Alert v-if="managedBy === 'gitops'" data-testid="managed-notice">
    <GitBranch aria-hidden="true" />
    <AlertTitle>{{ t('config.managed.title') }}</AlertTitle>
    <AlertDescription class="grid gap-2">
      <p>{{ t('config.managed.body') }}</p>
      <div v-if="session.hasRole('admin')">
        <Button size="sm" variant="outline" data-testid="managed-detach" @click="confirmOpen = true">{{ t('config.managed.detach') }}</Button>
      </div>
    </AlertDescription>
  </Alert>
  <Alert v-else-if="managedBy === 'gitops_detached'" data-testid="managed-detached">
    <GitBranch aria-hidden="true" />
    <AlertDescription class="flex flex-wrap items-center gap-2">
      <span>{{ t('config.managed.detachedBody') }}</span>
      <Button v-if="session.hasRole('admin')" size="sm" variant="ghost" data-testid="managed-attach" @click="change(true)">{{ t('config.managed.attach') }}</Button>
    </AlertDescription>
  </Alert>
  <ConfirmDialog
    v-model:open="confirmOpen"
    :title="t('config.managed.detach')"
    :description="t('config.managed.detachImpact')"
    :confirm-label="t('config.managed.detach')"
    :on-confirm="() => change(false)"
  />
</template>
