<script setup lang="ts">
import { CircleCheck, CircleX, ShieldAlert, TriangleAlert } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

defineProps<{ result: Schemas['ConnectionTestResult']; canTrust?: boolean }>()
const emit = defineEmits<{ trust: [fingerprint: string] }>()
const { t, te } = useI18n()
const message = (code?: string) => (code && te(`errors.${code}`) ? t(`errors.${code}`) : t('errors.connection.failed'))
</script>

<template>
  <div class="grid gap-2" data-testid="test-result">
    <Alert v-if="result.ok">
      <CircleCheck aria-hidden="true" />
      <AlertTitle>{{ t('connections.test.ok') }}</AlertTitle>
      <AlertDescription>
        {{ t('connections.test.okDetail', { version: result.server_version, ms: result.latency_ms, tables: result.tables ?? 0 }) }}
      </AlertDescription>
    </Alert>
    <Alert v-if="result.ok && result.can_write" data-testid="write-warning">
      <TriangleAlert aria-hidden="true" />
      <AlertTitle>{{ t('connections.writeWarning.title') }}</AlertTitle>
      <AlertDescription>{{ t('connections.writeWarning.body') }}</AlertDescription>
    </Alert>
    <Alert v-if="!result.ok && result.error_code === 'connection.ssh_host_key_unknown'" data-testid="fingerprint-confirm">
      <ShieldAlert aria-hidden="true" />
      <AlertTitle>{{ t('connections.fingerprint.title') }}</AlertTitle>
      <AlertDescription class="grid gap-2">
        <p>{{ t('connections.fingerprint.body') }}</p>
        <code class="bg-muted rounded px-2 py-1 font-mono text-xs break-all">{{ result.error_detail?.fingerprint }}</code>
        <div v-if="canTrust">
          <Button type="button" size="sm" data-testid="fingerprint-trust" @click="emit('trust', result.error_detail?.fingerprint ?? '')">
            {{ t('connections.fingerprint.trust') }}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
    <Alert v-else-if="!result.ok" variant="destructive">
      <CircleX aria-hidden="true" />
      <AlertTitle>{{ t('connections.test.failed') }}</AlertTitle>
      <AlertDescription class="grid gap-1">
        <p>{{ message(result.error_code) }}</p>
        <code v-if="result.error_detail?.fingerprint" class="font-mono text-xs break-all">{{ result.error_detail.fingerprint }}</code>
        <p class="text-xs opacity-80">{{ result.error_code }}</p>
      </AlertDescription>
    </Alert>
  </div>
</template>
