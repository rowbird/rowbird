<script setup lang="ts">
import { CircleCheck, CircleX } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'

defineProps<{ result: Schemas['ChannelTestResult'] }>()
const { t, te } = useI18n()
const message = (code?: string) => (code && te(`errors.${code}`) ? t(`errors.${code}`) : t('errors.delivery.failed'))
</script>

<template>
  <Alert v-if="result.ok" data-testid="channel-test-result">
    <CircleCheck aria-hidden="true" />
    <AlertTitle>{{ t('channels.test.ok') }}</AlertTitle>
    <AlertDescription>{{ t('channels.test.okDetail') }}</AlertDescription>
  </Alert>
  <Alert v-else variant="destructive" data-testid="channel-test-result">
    <CircleX aria-hidden="true" />
    <AlertTitle>{{ t('channels.test.failed') }}</AlertTitle>
    <AlertDescription class="grid gap-1">
      <p>{{ message(result.error_code) }}</p>
      <p v-if="result.error_message" class="font-mono text-xs break-all">{{ result.error_message }}</p>
    </AlertDescription>
  </Alert>
</template>
