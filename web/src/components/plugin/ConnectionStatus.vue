<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Badge } from '@/components/ui/badge'

const props = defineProps<{ status: 'unknown' | 'ok' | 'error'; error?: string }>()
const { t, te } = useI18n()
const title = computed(() => (props.status === 'error' && props.error && te(`errors.${props.error}`) ? t(`errors.${props.error}`) : undefined))
</script>

<template>
  <Badge :variant="status === 'ok' ? 'default' : status === 'error' ? 'destructive' : 'secondary'" :title="title" data-testid="connection-status">
    {{ t(`connections.statuses.${status}`) }}
  </Badge>
</template>
