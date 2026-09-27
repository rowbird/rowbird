<script setup lang="ts">
import { Cable, CircleCheck, Circle, FileClock, ScrollText, Send, X } from '@lucide/vue'
import { type Component, computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSessionStore } from '@/stores/session'

/**
 * The first-use checklist (docs/spec/06-ui.md, "Home"): connect a database, write a query, schedule
 * a report, receive the first delivery. It disappears once everything is done, and "Hide" keeps it
 * away in this browser. Viewers never see it: they cannot do the steps.
 */
const props = defineProps<{ state: Schemas['Onboarding'] }>()
const { t } = useI18n()
const session = useSessionStore()
const HIDE_KEY = 'rowbird.onboarding.hidden'

function readHidden() {
  try {
    return localStorage.getItem(HIDE_KEY) === 'true'
  } catch {
    return false
  }
}
const hidden = ref(readHidden())
function hide() {
  hidden.value = true
  try {
    localStorage.setItem(HIDE_KEY, 'true')
  } catch {
    // Without storage it stays hidden for this visit.
  }
}

interface Step { key: string; done: boolean; icon: Component; to: string; action: string; enabled: boolean }
const steps = computed<Step[]>(() => [
  { key: 'connection', done: props.state.has_connection, icon: Cable, to: '/connections/new', action: 'connections.add', enabled: session.hasRole('admin') },
  { key: 'query', done: props.state.has_query, icon: ScrollText, to: '/queries/new', action: 'queries.add', enabled: true },
  { key: 'report', done: props.state.has_report, icon: FileClock, to: '/reports/new', action: 'reports.add', enabled: true },
  { key: 'delivery', done: props.state.has_delivery, icon: Send, to: '/reports', action: 'home.onboarding.openReports', enabled: true },
])
const doneCount = computed(() => steps.value.filter((s) => s.done).length)
const visible = computed(() => session.hasRole('editor') && !hidden.value && doneCount.value < steps.value.length)
// The next step is the first one not done; later ones wait for it.
const nextKey = computed(() => steps.value.find((s) => !s.done)?.key)
</script>

<template>
  <Card v-if="visible" data-testid="onboarding">
    <CardHeader class="flex flex-row items-start justify-between gap-2">
      <div class="grid gap-1">
        <CardTitle as="h2">{{ t('home.onboarding.title') }}</CardTitle>
        <CardDescription>{{ t('home.onboarding.help', { done: doneCount, total: steps.length }) }}</CardDescription>
      </div>
      <Button variant="ghost" size="sm" data-testid="onboarding-hide" @click="hide"><X aria-hidden="true" />{{ t('home.onboarding.hide') }}</Button>
    </CardHeader>
    <CardContent>
      <ol class="grid gap-3 md:grid-cols-4">
        <li
          v-for="(s, i) in steps"
          :key="s.key"
          class="grid content-start gap-2 rounded-md border p-3"
          :class="s.done ? 'bg-muted/40' : ''"
          :data-testid="`onboarding-${s.key}`"
          :data-done="s.done"
        >
          <div class="flex items-center gap-2 text-sm font-medium">
            <CircleCheck v-if="s.done" class="size-4 text-emerald-600" aria-hidden="true" />
            <Circle v-else class="text-muted-foreground size-4" aria-hidden="true" />
            <span>{{ i + 1 }}. {{ t(`home.onboarding.steps.${s.key}.title`) }}</span>
            <span class="sr-only">{{ s.done ? t('home.onboarding.done') : t('home.onboarding.todo') }}</span>
          </div>
          <p class="text-muted-foreground text-xs">{{ t(`home.onboarding.steps.${s.key}.help`) }}</p>
          <Button v-if="!s.done && s.enabled && s.key === nextKey" as-child size="sm" class="justify-self-start">
            <RouterLink :to="s.to"><component :is="s.icon" aria-hidden="true" />{{ t(s.action) }}</RouterLink>
          </Button>
          <p v-else-if="!s.done && !s.enabled" class="text-muted-foreground text-xs italic">{{ t('home.onboarding.askAdmin') }}</p>
        </li>
      </ol>
    </CardContent>
  </Card>
</template>
