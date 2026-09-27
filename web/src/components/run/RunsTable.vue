<script setup lang="ts">
import { History } from '@lucide/vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import EmptyState from '@/components/common/EmptyState.vue'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'
import { runCodeMessage } from '@/lib/runs'

import RunStatusBadge from './RunStatusBadge.vue'

withDefaults(defineProps<{ runs: Schemas['RunSummary'][]; showReport?: boolean }>(), { showReport: true })
const { t, te, locale } = useI18n()
const fmt = useFormat()

const count = (n: number) => new Intl.NumberFormat(locale.value).format(n)

function duration(ms: number | null) {
  if (ms == null) return ''
  if (ms < 1000) return t('runs.ms', { n: ms })
  return t('runs.seconds', { n: new Intl.NumberFormat(locale.value, { maximumFractionDigits: 1 }).format(ms / 1000) })
}
</script>

<template>
  <EmptyState v-if="runs.length === 0" :icon="History" :title="t('runs.emptyTitle')" :description="t('runs.emptyBody')" />
  <div v-else class="overflow-x-auto rounded-md border">
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{{ t('runs.fields.status') }}</TableHead>
          <TableHead v-if="showReport">{{ t('runs.fields.report') }}</TableHead>
          <TableHead>{{ t('runs.fields.trigger') }}</TableHead>
          <TableHead>{{ t('runs.fields.started') }}</TableHead>
          <TableHead class="text-right">{{ t('runs.fields.duration') }}</TableHead>
          <TableHead class="text-right">{{ t('runs.fields.rows') }}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="r in runs" :key="r.id" :data-testid="`run-row-${r.id}`">
          <TableCell>
            <RouterLink :to="`/runs/${r.id}`" class="grid gap-1 hover:underline">
              <RunStatusBadge :status="r.status" />
              <span v-if="r.error_code" class="text-muted-foreground text-xs">{{ runCodeMessage(r.error_code, t, te) }}</span>
            </RouterLink>
          </TableCell>
          <TableCell v-if="showReport">
            <RouterLink :to="`/reports/${r.report_id}`" class="hover:underline">{{ r.report_title }}</RouterLink>
          </TableCell>
          <TableCell class="text-sm">
            {{ t(`runs.trigger.${r.trigger}`) }}
            <span v-if="r.trigger === 'manual' && !r.deliver" class="text-muted-foreground text-xs"> · {{ t('runs.noDelivery') }}</span>
          </TableCell>
          <TableCell class="text-sm">{{ fmt.dateTime(r.started_at ?? r.created_at) }}</TableCell>
          <TableCell class="text-right text-sm tabular-nums">{{ duration(r.duration_ms) }}</TableCell>
          <TableCell class="text-right text-sm tabular-nums">{{ r.row_count == null ? '' : count(r.row_count) }}</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  </div>
</template>
