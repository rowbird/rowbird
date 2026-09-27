<script setup lang="ts">
import { ChevronRight, Search } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { Input } from '@/components/ui/input'

/** Tables and columns of the connection; clicking a name inserts it into the editor. */
const props = defineProps<{ tables: Schemas['DatabaseTable'][] }>()
const emit = defineEmits<{ insert: [text: string] }>()
const { t } = useI18n()
const search = ref('')
const open = ref(new Set<string>())

const qualified = (tb: Schemas['DatabaseTable']) => (tb.schema && tb.schema !== 'public' && tb.schema !== 'dbo' ? `${tb.schema}.${tb.name}` : tb.name)
const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return props.tables
  return props.tables.filter((tb) => qualified(tb).toLowerCase().includes(q) || tb.columns.some((c) => c.name.toLowerCase().includes(q)))
})

function toggle(name: string) {
  const s = new Set(open.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  open.value = s
}
</script>

<template>
  <!-- The list scrolls on its own, so a large schema never stretches the editor next to it. -->
  <div class="flex h-full min-h-0 flex-col gap-2" data-testid="schema-panel">
    <div class="relative">
      <Search class="text-muted-foreground absolute top-2.5 left-2.5 size-4" aria-hidden="true" />
      <Input v-model="search" class="pl-8" :placeholder="t('connections.schema.search')" :aria-label="t('connections.schema.search')" />
    </div>
    <p v-if="tables.length === 0" class="text-muted-foreground text-sm">{{ t('queries.editor.noSchema') }}</p>
    <ul class="grid min-h-0 flex-1 content-start gap-0.5 overflow-y-auto pr-1 text-sm">
      <li v-for="tb in filtered" :key="qualified(tb)">
        <div class="flex items-center gap-1">
          <button type="button" class="hover:bg-muted rounded p-0.5" :aria-label="t('queries.editor.toggleColumns', { table: qualified(tb) })" :aria-expanded="open.has(qualified(tb))" @click="toggle(qualified(tb))">
            <ChevronRight class="size-3.5 transition-transform" :class="open.has(qualified(tb)) ? 'rotate-90' : ''" aria-hidden="true" />
          </button>
          <button type="button" class="hover:underline truncate font-mono" :data-testid="`schema-insert-${qualified(tb)}`" @click="emit('insert', qualified(tb))">{{ qualified(tb) }}</button>
        </div>
        <ul v-if="open.has(qualified(tb))" class="ml-6 grid gap-0.5">
          <li v-for="c in tb.columns" :key="c.name" class="flex items-center justify-between gap-2">
            <button type="button" class="truncate font-mono text-xs hover:underline" @click="emit('insert', c.name)">{{ c.name }}</button>
            <span class="text-muted-foreground shrink-0 text-xs">{{ t(`connections.types.${c.type}`) }}</span>
          </li>
        </ul>
      </li>
    </ul>
  </div>
</template>
