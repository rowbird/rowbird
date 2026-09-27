<script setup lang="ts">
import { Bell, CircleCheck, CircleX, TriangleAlert } from '@lucide/vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { toast } from 'vue-sonner'

import { errorMessage } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { useFormat } from '@/composables/useFormat'
import { type AppNotification, notificationLink, useNotificationsStore } from '@/stores/notifications'

/** The bell in the top bar and the notification center it opens (docs/spec/06-ui.md, "Global"). */
const { t, te } = useI18n()
const fmt = useFormat()
const router = useRouter()
const store = useNotificationsStore()
const open = ref(false)

onMounted(() => store.start())

const label = computed(() => (store.unread > 0 ? t('notifications.bellUnread', { count: store.unread }) : t('notifications.bell')))
const badge = computed(() => (store.unread > 99 ? '99+' : String(store.unread)))

const icons = { error: CircleX, warning: TriangleAlert, info: CircleCheck }
const iconClass = { error: 'text-destructive', warning: 'text-amber-600', info: 'text-emerald-600' }

function title(n: AppNotification) {
  const key = `notifications.types.${n.type}`
  return te(key) ? t(key, n.params) : n.type
}

async function select(n: AppNotification) {
  try {
    if (!n.read_at) await store.markRead(n.id)
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
  const to = notificationLink(n)
  if (to) {
    open.value = false
    await router.push(to)
  }
}

async function markAll() {
  try {
    await store.markAllRead()
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}
</script>

<template>
  <Sheet v-model:open="open">
    <Button variant="ghost" size="icon" class="relative" :aria-label="label" :title="label" data-testid="notification-bell" @click="open = true">
      <Bell aria-hidden="true" />
      <span
        v-if="store.unread > 0"
        class="bg-destructive absolute -top-0.5 -right-0.5 min-w-4 rounded-full px-1 text-[10px] leading-4 font-medium text-white"
        data-testid="notification-count"
      >{{ badge }}</span>
    </Button>
    <SheetContent class="w-full sm:max-w-md">
      <SheetHeader>
        <SheetTitle>{{ t('notifications.title') }}</SheetTitle>
        <SheetDescription>{{ t('notifications.help') }}</SheetDescription>
      </SheetHeader>
      <div class="flex justify-end px-4">
        <Button variant="outline" size="sm" :disabled="store.unread === 0" data-testid="notifications-read-all" @click="markAll">
          {{ t('notifications.markAllRead') }}
        </Button>
      </div>
      <div class="grid gap-1 overflow-y-auto px-2 pb-4">
        <p v-if="store.loaded && store.items.length === 0" class="text-muted-foreground px-2 py-8 text-center text-sm" data-testid="notifications-empty">
          {{ t('notifications.empty') }}
        </p>
        <button
          v-for="n in store.items"
          :key="n.id"
          type="button"
          class="hover:bg-muted flex w-full items-start gap-3 rounded-md px-2 py-2 text-left"
          :class="{ 'bg-muted/50': !n.read_at }"
          data-testid="notification"
          @click="select(n)"
        >
          <component :is="icons[n.severity]" class="mt-0.5 size-4 shrink-0" :class="iconClass[n.severity]" aria-hidden="true" />
          <span class="grid min-w-0 flex-1 gap-0.5">
            <span class="text-sm" :class="{ 'font-medium': !n.read_at }">{{ title(n) }}</span>
            <span v-if="n.params.error" class="text-muted-foreground truncate text-xs">{{ n.params.error }}</span>
            <span class="text-muted-foreground text-xs">
              {{ fmt.dateTime(n.last_at) }}
              <template v-if="n.count > 1"> · <span data-testid="notification-occurrences">{{ t('notifications.occurrences', { count: n.count }) }}</span></template>
              <template v-if="n.resolved_at && n.severity !== 'info'"> · {{ t('notifications.resolved') }}</template>
            </span>
          </span>
          <span v-if="!n.read_at" class="bg-primary mt-1.5 size-2 shrink-0 rounded-full" :aria-label="t('notifications.unread')" />
        </button>
        <Button v-if="store.next" variant="ghost" size="sm" @click="store.loadMore()">{{ t('common.loadMore') }}</Button>
      </div>
    </SheetContent>
  </Sheet>
</template>
