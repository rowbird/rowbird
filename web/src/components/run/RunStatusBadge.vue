<script setup lang="ts">
import { CircleCheck, CircleDashed, CircleMinus, CircleX, LoaderCircle, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'

import { Badge } from '@/components/ui/badge'
import type { RunStatus } from '@/lib/runs'

const props = defineProps<{ status: RunStatus }>()

const look = computed(() => {
  switch (props.status) {
    case 'success':
      return { icon: CircleCheck, variant: 'outline' as const, cls: 'border-emerald-600/40 text-emerald-700 dark:text-emerald-400' }
    case 'failed':
      return { icon: CircleX, variant: 'destructive' as const, cls: '' }
    case 'partial':
      return { icon: TriangleAlert, variant: 'outline' as const, cls: 'border-amber-600/40 text-amber-700 dark:text-amber-400' }
    case 'running':
      return { icon: LoaderCircle, variant: 'secondary' as const, cls: '[&>svg]:animate-spin' }
    case 'pending':
      return { icon: CircleDashed, variant: 'secondary' as const, cls: '' }
    default:
      return { icon: CircleMinus, variant: 'outline' as const, cls: 'text-muted-foreground' }
  }
})
</script>

<template>
  <Badge :variant="look.variant" :class="look.cls" :data-testid="`run-status-${status}`">
    <component :is="look.icon" aria-hidden="true" />{{ $t(`runs.status.${status}`) }}
  </Badge>
</template>
