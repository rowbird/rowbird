<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import { Alert, AlertDescription } from '@/components/ui/alert'

defineProps<{ warnings: string[] }>()
const { t, te } = useI18n()
</script>

<template>
  <Alert v-for="w in warnings" :key="w" data-testid="ai-warning">
    <TriangleAlert aria-hidden="true" />
    <AlertDescription>{{ te(`ai.warnings.${w.replace('ai.warning.', '')}`) ? t(`ai.warnings.${w.replace('ai.warning.', '')}`) : w }}</AlertDescription>
  </Alert>
</template>
