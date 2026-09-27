<script setup lang="ts">
import { Link } from '@lucide/vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LinkDetailSheet from '@/components/links/LinkDetailSheet.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'
import { useSessionStore } from '@/stores/session'

/** Shared links: what was shared, until when, how often it was opened; revocation. */
const { t, te } = useI18n()
const fmt = useFormat()
const session = useSessionStore()

const items = ref<Schemas['SharedLink'][]>([])
const cursor = ref<string | null>(null)
const activeOnly = ref(true)
const loaded = ref(false)
const revoking = ref<Schemas['SharedLink'] | null>(null)
const revokeOpen = ref(false)
const canRevoke = computed(() => session.hasRole('editor'))
const detailId = ref<string | null>(null)
const detailOpen = ref(false)

function showDetail(l: Schemas['SharedLink']) {
  detailId.value = l.id
  detailOpen.value = true
}

async function load(more = false) {
  try {
    const page = unwrap(await api.GET('/api/v1/links', {
      params: { query: { active: activeOnly.value || undefined, cursor: more ? (cursor.value ?? undefined) : undefined, limit: 50 } },
    }))
    items.value = more ? [...items.value, ...page.items] : page.items
    cursor.value = page.next_cursor ?? null
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  } finally {
    loaded.value = true
  }
}
onMounted(() => load())
watch(activeOnly, () => load())

function confirmRevoke(l: Schemas['SharedLink']) {
  revoking.value = l
  revokeOpen.value = true
}

async function revoke() {
  const l = revoking.value
  if (!l) return
  try {
    unwrap(await api.POST('/api/v1/links/{linkId}/revoke', { params: { path: { linkId: l.id } } }))
    toast.success(t('links.revoked'))
    detailOpen.value = false
    await load()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

const statusVariant = (s: Schemas['SharedLink']['status']) => (s === 'active' ? 'default' : s === 'revoked' ? 'destructive' : 'secondary')
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <div>
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.links') }}</h1>
        <p class="text-muted-foreground text-sm">{{ t('links.help') }}</p>
      </div>
      <label class="flex items-center gap-2 text-sm">
        <Switch v-model="activeOnly" data-testid="links-active-only" />{{ t('links.activeOnly') }}
      </label>
    </div>

    <EmptyState v-if="loaded && items.length === 0" :icon="Link" :title="t('links.emptyTitle')" :description="t('links.emptyBody')" />

    <div v-else-if="items.length > 0" class="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{{ t('links.file') }}</TableHead>
            <TableHead>{{ t('links.status') }}</TableHead>
            <TableHead>{{ t('links.expires') }}</TableHead>
            <TableHead>{{ t('links.downloads') }}</TableHead>
            <TableHead><span class="sr-only">{{ t('links.actions') }}</span></TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="l in items" :key="l.id" :data-testid="`link-row-${l.file_name}`">
            <TableCell>
              <button type="button" class="font-medium hover:underline" :data-testid="`link-open-${l.file_name}`" @click="showDetail(l)">{{ l.file_name }}</button>
              <div class="text-muted-foreground text-xs">
                <RouterLink :to="`/reports/${l.report_id}`" class="hover:underline">{{ l.report_title }}</RouterLink>
                · <RouterLink :to="`/runs/${l.run_id}`" class="hover:underline">{{ t('links.run') }}</RouterLink>
                <span v-if="l.require_login"> · {{ t('links.loginRequired') }}</span>
              </div>
            </TableCell>
            <TableCell>
              <Badge :variant="statusVariant(l.status)" data-testid="link-status">{{ t(`links.statuses.${l.status}`) }}</Badge>
              <div v-if="l.revoked_by_name" class="text-muted-foreground text-xs">{{ t('links.revokedBy', { name: l.revoked_by_name }) }}</div>
            </TableCell>
            <TableCell class="text-sm">{{ fmt.dateTime(l.expires_at) }}</TableCell>
            <TableCell class="text-sm">
              {{ l.download_count }}
              <div v-if="l.last_download_at" class="text-muted-foreground text-xs">{{ t('links.lastDownload', { time: fmt.dateTime(l.last_download_at) }) }}</div>
            </TableCell>
            <TableCell class="text-right">
              <Button v-if="canRevoke && l.status === 'active'" variant="outline" size="sm" data-testid="link-revoke" @click="confirmRevoke(l)">{{ t('links.revoke') }}</Button>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
    <div v-if="cursor">
      <Button variant="outline" size="sm" @click="load(true)">{{ t('common.loadMore') }}</Button>
    </div>

    <LinkDetailSheet v-model:open="detailOpen" :link-id="detailId" :can-revoke="canRevoke" @revoke="confirmRevoke" />
    <ConfirmDialog
      v-model:open="revokeOpen"
      :title="t('links.revoke')"
      :description="t('links.revokeImpact', { file: revoking?.file_name ?? '' })"
      :confirm-label="t('links.revoke')"
      destructive
      :on-confirm="revoke"
    />
  </div>
</template>
