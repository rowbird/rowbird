<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'

defineProps<{ result: Schemas['PreviewResult']; hideSummary?: boolean }>()
const { t, locale } = useI18n()

const numberFormat = computed(() => new Intl.NumberFormat(locale.value, { maximumFractionDigits: 20 }))

/** Formats a cell for its column type; exact numbers stay exact (Intl formats decimal strings). */
function cell(type: string, v: unknown): string {
  if (v === null || v === undefined) return ''
  switch (type) {
    case 'integer':
    case 'decimal':
    case 'float':
      try {
        return numberFormat.value.format(v as never)
      } catch {
        return String(v)
      }
    case 'boolean':
      return v ? t('results.true') : t('results.false')
    case 'binary':
      return t('results.bytes', { n: Math.floor((String(v).length * 3) / 4) })
    default:
      return String(v)
  }
}

const numeric = (type: string) => type === 'integer' || type === 'decimal' || type === 'float'
</script>

<template>
  <div class="grid gap-2" data-testid="results">
    <div v-if="!hideSummary" class="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
      <span data-testid="results-summary">{{ t('results.summary', { rows: result.rows.length, ms: result.duration_ms }) }}</span>
      <span v-if="result.truncated" class="text-amber-700 dark:text-amber-400" data-testid="results-truncated">{{ t('results.truncated', { rows: result.rows.length }) }}</span>
      <span v-if="result.params.length">
        {{ t('results.params', { tz: result.timezone }) }}
        <span v-for="p in result.params" :key="p.name" class="ml-2 font-mono" data-testid="resolved-param">{{ p.name }} = {{ p.value }}</span>
      </span>
    </div>
    <div class="max-h-96 overflow-auto rounded-md border">
      <table class="w-full text-sm">
        <thead class="bg-muted sticky top-0">
          <tr>
            <th v-for="c in result.columns" :key="c.name" class="px-3 py-2 text-left font-medium whitespace-nowrap">
              {{ c.name }}
              <Badge variant="outline" class="ml-1 font-normal" :title="c.db_type">{{ t(`connections.types.${c.type}`) }}</Badge>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="result.rows.length === 0">
            <td :colspan="result.columns.length || 1" class="text-muted-foreground px-3 py-4 text-center">{{ t('results.empty') }}</td>
          </tr>
          <tr v-for="(row, i) in result.rows" :key="i" class="border-t">
            <td v-for="(c, j) in result.columns" :key="c.name" class="px-3 py-1.5 whitespace-nowrap" :class="numeric(c.type) ? 'text-right tabular-nums' : ''">
              <span v-if="row[j] === null" class="text-muted-foreground text-xs italic">NULL</span>
              <template v-else>{{ cell(c.type, row[j]) }}</template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
