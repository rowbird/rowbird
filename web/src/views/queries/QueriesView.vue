<script setup lang="ts">
import { Plus, ScrollText } from '@lucide/vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import EmptyState from '@/components/common/EmptyState.vue'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'
import { useSessionStore } from '@/stores/session'

const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const items = ref<Schemas['QuerySummary'][]>([])
const loaded = ref(false)

onMounted(async () => {
  try {
    items.value = unwrap(await api.GET('/api/v1/queries')).items
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
})
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.queries') }}</h1>
        <p class="text-muted-foreground text-sm">{{ t('queries.help') }}</p>
      </div>
      <Button v-if="session.hasRole('editor') && items.length > 0" as-child>
        <RouterLink to="/queries/new" data-testid="query-new"><Plus aria-hidden="true" />{{ t('queries.add') }}</RouterLink>
      </Button>
    </div>
    <EmptyState v-if="loaded && items.length === 0" :icon="ScrollText" :title="t('queries.emptyTitle')" :description="t('queries.emptyBody')">
      <Button v-if="session.hasRole('editor')" as-child>
        <RouterLink to="/queries/new" data-testid="query-new"><Plus aria-hidden="true" />{{ t('queries.add') }}</RouterLink>
      </Button>
    </EmptyState>
    <div v-else-if="items.length" class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('queries.editor.title') }}</TableHead>
            <TableHead>{{ t('queries.editor.connection') }}</TableHead>
            <TableHead>{{ t('queries.lastChange') }}</TableHead>
            <TableHead>{{ t('queries.reports') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="q in items" :key="q.id" :data-testid="`query-row-${q.slug}`">
            <TableCell>
              <RouterLink :to="`/queries/${q.id}`" class="font-medium hover:underline">{{ q.title }}</RouterLink>
              <div class="text-muted-foreground font-mono text-xs">{{ q.slug }}</div>
            </TableCell>
            <TableCell>{{ q.connection_name }}</TableCell>
            <TableCell class="text-sm">
              {{ fmt.dateTime(q.updated_at) }}
              <div class="text-muted-foreground text-xs">{{ t('queries.byAuthor', { author: q.author_name, n: q.current_version }) }}</div>
            </TableCell>
            <TableCell>{{ q.report_count }}</TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </div>
</template>
