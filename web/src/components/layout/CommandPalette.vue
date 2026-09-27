<script setup lang="ts">
import { Cable, FileClock, Megaphone, Plus, ScrollText, Search, Settings, User } from '@lucide/vue'
import { type Component, computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { api } from '@/api/client'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { NAV_ITEMS } from '@/router/nav'
import { useSessionStore } from '@/stores/session'

/**
 * The command palette (Ctrl/Cmd+K): go to a page, create something, or open a report, query,
 * connection or channel by name, all from the keyboard (docs/spec/06-ui.md).
 */
interface Entry {
  id: string
  group: 'actions' | 'pages' | 'reports' | 'queries' | 'connections' | 'channels'
  label: string
  hint?: string
  icon: Component
  to: string
}

const open = defineModel<boolean>('open', { required: true })
const { t } = useI18n()
const router = useRouter()
const session = useSessionStore()
const term = ref('')
const active = ref(0)
const records = ref<Entry[]>([])
const input = ref<HTMLInputElement | null>(null)

const fold = (s: string) => s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase()

const staticEntries = computed<Entry[]>(() => {
  const out: Entry[] = []
  const can = (role?: 'viewer' | 'editor' | 'admin') => !role || session.hasRole(role)
  const actions: [string, string, 'editor' | 'admin', Component][] = [
    ['queries.add', '/queries/new', 'editor', ScrollText],
    ['reports.add', '/reports/new', 'editor', FileClock],
    ['connections.add', '/connections/new', 'admin', Cable],
    ['channels.add', '/channels/new', 'admin', Megaphone],
  ]
  for (const [key, to, role, icon] of actions) {
    if (can(role)) out.push({ id: `action:${to}`, group: 'actions', label: t(key), icon, to, hint: t('palette.create') })
  }
  for (const item of NAV_ITEMS) out.push({ id: `page:${item.path}`, group: 'pages', label: t(item.labelKey), icon: item.icon, to: item.path })
  for (const r of router.getRoutes()) {
    const name = String(r.name ?? '')
    if (!name.startsWith('settings-') || !r.meta.titleKey || !can(r.meta.role)) continue
    out.push({ id: `page:${r.path}`, group: 'pages', label: `${t('nav.settings')} › ${t(r.meta.titleKey)}`, icon: Settings, to: r.path })
  }
  out.push({ id: 'page:/profile', group: 'pages', label: t('profile.title'), icon: User, to: '/profile' })
  return out
})

const entries = computed(() => {
  const q = fold(term.value.trim())
  const all = [...staticEntries.value, ...records.value]
  if (!q) return all.filter((e) => e.group !== 'queries' && e.group !== 'connections' && e.group !== 'channels' && e.group !== 'reports')
  return all.filter((e) => fold(e.label).includes(q) || (e.hint && fold(e.hint).includes(q))).slice(0, 50)
})

const groups = computed(() => {
  const order: Entry['group'][] = ['actions', 'pages', 'reports', 'queries', 'connections', 'channels']
  return order.map((g) => ({ g, items: entries.value.filter((e) => e.group === g) })).filter((x) => x.items.length)
})
const flat = computed(() => groups.value.flatMap((x) => x.items))

async function loadRecords() {
  const [reports, queries, connections, channels] = await Promise.all([
    api.GET('/api/v1/reports'), api.GET('/api/v1/queries'), api.GET('/api/v1/connections'), api.GET('/api/v1/channels'),
  ])
  const out: Entry[] = []
  for (const r of reports.data?.items ?? []) out.push({ id: `report:${r.id}`, group: 'reports', label: r.title, hint: r.slug, icon: FileClock, to: `/reports/${r.id}` })
  for (const q of queries.data?.items ?? []) out.push({ id: `query:${q.id}`, group: 'queries', label: q.title, hint: q.slug, icon: ScrollText, to: `/queries/${q.id}` })
  for (const c of connections.data?.items ?? []) out.push({ id: `connection:${c.id}`, group: 'connections', label: c.name, icon: Cable, to: `/connections/${c.id}` })
  for (const c of channels.data?.items ?? []) out.push({ id: `channel:${c.id}`, group: 'channels', label: c.name, icon: Megaphone, to: `/channels/${c.id}` })
  records.value = out
}

watch(open, async (v) => {
  if (!v) return
  term.value = ''
  active.value = 0
  void loadRecords()
  await nextTick()
  input.value?.focus()
})
watch(term, () => (active.value = 0))

async function go(e?: Entry) {
  if (!e) return
  open.value = false
  await router.push(e.to)
}

function onKey(ev: KeyboardEvent) {
  const n = flat.value.length
  if (ev.key === 'ArrowDown' && n) {
    ev.preventDefault()
    active.value = (active.value + 1) % n
  } else if (ev.key === 'ArrowUp' && n) {
    ev.preventDefault()
    active.value = (active.value - 1 + n) % n
  } else if (ev.key === 'Enter') {
    ev.preventDefault()
    void go(flat.value[active.value])
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="top-[15%] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-xl" :show-close-button="false" data-testid="palette">
      <DialogTitle class="sr-only">{{ t('palette.title') }}</DialogTitle>
      <DialogDescription class="sr-only">{{ t('palette.help') }}</DialogDescription>
      <div class="flex items-center gap-2 border-b px-3">
        <Search class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
        <input
          ref="input"
          v-model="term"
          class="placeholder:text-muted-foreground h-12 w-full bg-transparent text-sm outline-none"
          :placeholder="t('palette.placeholder')"
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          :aria-activedescendant="flat[active] ? `palette-${flat[active]!.id}` : undefined"
          data-testid="palette-input"
          @keydown="onKey"
        >
      </div>
      <div id="palette-list" role="listbox" class="max-h-96 overflow-y-auto p-2">
        <p v-if="!flat.length" class="text-muted-foreground p-4 text-center text-sm">{{ t('palette.empty') }}</p>
        <div v-for="grp in groups" :key="grp.g" role="group" :aria-label="t(`palette.groups.${grp.g}`)" class="mb-1">
          <div class="text-muted-foreground px-2 py-1.5 text-xs font-medium">{{ t(`palette.groups.${grp.g}`) }}</div>
          <button
            v-for="e in grp.items"
            :id="`palette-${e.id}`"
            :key="e.id"
            type="button"
            role="option"
            :aria-selected="flat[active]?.id === e.id"
            class="flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm"
            :class="flat[active]?.id === e.id ? 'bg-accent text-accent-foreground' : ''"
            :data-testid="`palette-item-${e.id}`"
            @mouseenter="active = flat.indexOf(e)"
            @click="go(e)"
          >
            <component :is="e.group === 'actions' ? Plus : e.icon" class="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
            <span class="truncate">{{ e.label }}</span>
            <span v-if="e.hint" class="text-muted-foreground ml-auto truncate font-mono text-xs">{{ e.hint }}</span>
          </button>
        </div>
      </div>
    </DialogContent>
  </Dialog>
</template>
