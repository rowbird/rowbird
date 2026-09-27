<script setup lang="ts">
import { Download } from '@lucide/vue'
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import EmptyState from '@/components/common/EmptyState.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useFormat } from '@/composables/useFormat'
import { describeUserAgent } from '@/lib/userAgent'

/** One shared link with its latest 100 downloads: when, who (or anonymous), from where. */
const props = defineProps<{ linkId: string | null; canRevoke: boolean }>()
const emit = defineEmits<{ revoke: [Schemas['SharedLink']] }>()
const open = defineModel<boolean>('open', { required: true })
const { t, te } = useI18n()
const fmt = useFormat()
const link = ref<Schemas['SharedLinkDetail'] | null>(null)

watch([open, () => props.linkId], async ([isOpen, id]) => {
  if (!isOpen || !id) return
  link.value = null
  try {
    link.value = unwrap(await api.GET('/api/v1/links/{linkId}', { params: { path: { linkId: id } } }))
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}, { immediate: true })

function agent(ua: string) {
  const { browser, os } = describeUserAgent(ua)
  if (browser && os) return t('links.detail.agent', { browser, os })
  return browser || os || ua || t('links.detail.unknownAgent')
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent side="right" class="w-full overflow-y-auto sm:max-w-2xl" data-testid="link-detail">
      <SheetHeader>
        <SheetTitle class="break-all">{{ link?.file_name ?? t('links.detail.title') }}</SheetTitle>
        <SheetDescription>{{ t('links.detail.help') }}</SheetDescription>
      </SheetHeader>
      <div v-if="link" class="grid gap-6 px-4 pb-6">
        <dl class="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
          <dt class="text-muted-foreground">{{ t('links.status') }}</dt>
          <dd><Badge :variant="link.status === 'active' ? 'default' : link.status === 'revoked' ? 'destructive' : 'secondary'">{{ t(`links.statuses.${link.status}`) }}</Badge></dd>
          <dt class="text-muted-foreground">{{ t('links.detail.report') }}</dt>
          <dd>
            <RouterLink :to="`/reports/${link.report_id}`" class="underline">{{ link.report_title }}</RouterLink>
            · <RouterLink :to="`/runs/${link.run_id}`" class="underline">{{ t('links.run') }}</RouterLink>
          </dd>
          <dt class="text-muted-foreground">{{ t('links.detail.created') }}</dt>
          <dd>{{ fmt.dateTime(link.created_at) }}</dd>
          <dt class="text-muted-foreground">{{ t('links.expires') }}</dt>
          <dd>{{ fmt.dateTime(link.expires_at) }}</dd>
          <dt class="text-muted-foreground">{{ t('links.detail.access') }}</dt>
          <dd>{{ link.require_login ? t('links.loginRequired') : t('links.detail.anyone') }}</dd>
          <template v-if="link.revoked_at">
            <dt class="text-muted-foreground">{{ t('links.detail.revoked') }}</dt>
            <dd>{{ fmt.dateTime(link.revoked_at) }}<span v-if="link.revoked_by_name"> · {{ link.revoked_by_name }}</span></dd>
          </template>
        </dl>
        <div v-if="canRevoke && link.status === 'active'">
          <Button variant="outline" size="sm" data-testid="link-detail-revoke" @click="emit('revoke', link)">{{ t('links.revoke') }}</Button>
        </div>

        <section class="grid gap-2">
          <h3 class="text-sm font-semibold">{{ t('links.detail.downloads', { n: link.download_count }, link.download_count) }}</h3>
          <EmptyState v-if="link.downloads.length === 0" :icon="Download" :title="t('links.detail.noDownloadsTitle')" :description="t('links.detail.noDownloadsBody')" />
          <div v-else class="overflow-x-auto rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{{ t('links.detail.when') }}</TableHead>
                  <TableHead>{{ t('links.detail.who') }}</TableHead>
                  <TableHead>{{ t('links.detail.ip') }}</TableHead>
                  <TableHead>{{ t('links.detail.browser') }}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="(d, i) in link.downloads" :key="i" data-testid="link-download">
                  <TableCell class="whitespace-nowrap">{{ fmt.dateTime(d.at) }}</TableCell>
                  <TableCell>{{ d.user_name ?? t('links.detail.anonymous') }}</TableCell>
                  <TableCell class="font-mono text-xs">{{ d.ip }}</TableCell>
                  <TableCell class="text-xs" :title="d.user_agent">{{ agent(d.user_agent) }}</TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
          <p v-if="link.download_count > link.downloads.length" class="text-muted-foreground text-xs">{{ t('links.detail.latestOnly') }}</p>
        </section>
      </div>
    </SheetContent>
  </Sheet>
</template>
