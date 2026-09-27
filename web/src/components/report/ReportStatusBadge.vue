<script setup lang="ts">
import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'

defineProps<{ status: Schemas['ReportStatus'] }>()
</script>

<template>
  <Badge
    :variant="status === 'failing' ? 'destructive' : status === 'paused' ? 'secondary' : 'outline'"
    :class="status === 'active' ? 'border-emerald-600/40 text-emerald-700 dark:text-emerald-400' : ''"
    :data-testid="`report-status-${status}`"
  >
    {{ $t(`reports.status.${status}`) }}
  </Badge>
</template>
