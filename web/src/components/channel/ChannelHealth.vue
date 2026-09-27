<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'

const props = defineProps<{ status: Schemas['ChannelStatus']; error?: string | null }>()
const { t } = useI18n()
const title = computed(() => (props.status === 'failing' && props.error ? props.error : undefined))
</script>

<template>
  <Badge :variant="status === 'ok' ? 'default' : status === 'failing' ? 'destructive' : 'secondary'" :title="title" data-testid="channel-status">
    {{ t(`channels.statuses.${status}`) }}
  </Badge>
</template>
