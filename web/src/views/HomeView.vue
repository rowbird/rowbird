<script setup lang="ts">
import { CalendarClock, CircleCheck, FileCode, Plus, Radio } from '@lucide/vue'
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import OnboardingChecklist from '@/components/home/OnboardingChecklist.vue'
import RunStatusBadge from '@/components/run/RunStatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useFormat } from '@/composables/useFormat'
import { useDashboardStore } from '@/stores/dashboard'
import { useSessionStore } from '@/stores/session'

/** Next runs, recent failures, success rate and failing channels (docs/spec/06-ui.md, "Home"). */
const { t, locale } = useI18n()
const fmt = useFormat()
const session = useSessionStore()
const dashboard = useDashboardStore()
const canEdit = computed(() => session.hasRole('editor'))

onMounted(() => {
  dashboard.start()
  void dashboard.load().catch(() => undefined)
})

const d = computed(() => dashboard.data)

function rate(r: Schemas['SuccessRate'] | undefined) {
  if (!r || r.total === 0) return null
  return new Intl.NumberFormat(locale.value, { style: 'percent', maximumFractionDigits: 1 }).format(r.succeeded / r.total)
}
const rates = computed(() => [
  { key: '7d', value: rate(d.value?.success_rate_7d), r: d.value?.success_rate_7d },
  { key: '30d', value: rate(d.value?.success_rate_30d), r: d.value?.success_rate_30d },
])
</script>

<template>
  <div class="grid gap-4">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold" data-testid="page-title">{{ t('nav.home') }}</h1>
        <p class="text-muted-foreground text-sm">{{ t('home.help') }}</p>
      </div>
      <div v-if="canEdit" class="flex flex-wrap gap-2">
        <Button as-child size="sm"><RouterLink to="/reports/new"><Plus aria-hidden="true" />{{ t('reports.add') }}</RouterLink></Button>
        <Button as-child size="sm" variant="outline"><RouterLink to="/queries/new"><FileCode aria-hidden="true" />{{ t('queries.add') }}</RouterLink></Button>
      </div>
    </div>

    <OnboardingChecklist v-if="d?.onboarding" :state="d.onboarding" />

    <div class="grid gap-4 md:grid-cols-2">
      <Card data-testid="home-success-rate">
        <CardHeader>
          <CardTitle>{{ t('home.successRate.title') }}</CardTitle>
          <CardDescription>{{ t('home.successRate.help') }}</CardDescription>
        </CardHeader>
        <CardContent class="grid grid-cols-2 gap-4">
          <div v-for="x in rates" :key="x.key">
            <p class="text-muted-foreground text-xs">{{ t(`home.successRate.${x.key}`) }}</p>
            <p class="text-2xl font-semibold">{{ x.value ?? '-' }}</p>
            <p class="text-muted-foreground text-xs">
              {{ x.r && x.r.total > 0 ? t('home.successRate.count', { succeeded: x.r.succeeded, total: x.r.total }) : t('home.successRate.none') }}
            </p>
          </div>
        </CardContent>
      </Card>

      <Card data-testid="home-next-runs">
        <CardHeader>
          <CardTitle>{{ t('home.nextRuns.title') }}</CardTitle>
        </CardHeader>
        <CardContent>
          <div v-if="d && d.next_runs.length === 0" class="text-muted-foreground flex flex-col items-start gap-2 text-sm">
            <span class="flex items-center gap-2"><CalendarClock class="size-4" aria-hidden="true" />{{ t('home.nextRuns.empty') }}</span>
            <Button v-if="canEdit" as-child size="sm" variant="outline"><RouterLink to="/reports/new">{{ t('reports.add') }}</RouterLink></Button>
          </div>
          <ul v-else class="grid gap-2">
            <li v-for="r in d?.next_runs ?? []" :key="r.id" class="flex items-center justify-between gap-3 text-sm">
              <RouterLink :to="`/reports/${r.id}`" class="truncate hover:underline">{{ r.title }}</RouterLink>
              <span class="text-muted-foreground shrink-0">{{ fmt.dateTime(r.next_run_at) }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Card data-testid="home-failures">
        <CardHeader>
          <CardTitle>{{ t('home.failures.title') }}</CardTitle>
        </CardHeader>
        <CardContent>
          <p v-if="d && d.recent_failures.length === 0" class="text-muted-foreground flex items-center gap-2 text-sm">
            <CircleCheck class="size-4 text-emerald-600" aria-hidden="true" />{{ t('home.failures.empty') }}
          </p>
          <ul v-else class="grid gap-2">
            <li v-for="r in d?.recent_failures ?? []" :key="r.id" class="flex items-center justify-between gap-3 text-sm">
              <RouterLink :to="`/runs/${r.id}`" class="flex min-w-0 items-center gap-2 hover:underline">
                <RunStatusBadge :status="r.status" />
                <span class="truncate">{{ r.report_title }}</span>
              </RouterLink>
              <span class="text-muted-foreground shrink-0">{{ fmt.dateTime(r.finished_at ?? r.created_at) }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Card data-testid="home-channels">
        <CardHeader>
          <CardTitle>{{ t('home.channels.title') }}</CardTitle>
        </CardHeader>
        <CardContent>
          <p v-if="d && d.failing_channels.length === 0" class="text-muted-foreground flex items-center gap-2 text-sm">
            <Radio class="size-4 text-emerald-600" aria-hidden="true" />{{ t('home.channels.empty') }}
          </p>
          <ul v-else class="grid gap-2">
            <li v-for="c in d?.failing_channels ?? []" :key="c.id" class="grid gap-0.5 text-sm">
              <RouterLink :to="`/channels/${c.id}`" class="text-destructive font-medium hover:underline">{{ c.name }}</RouterLink>
              <span v-if="c.last_error" class="text-muted-foreground truncate text-xs">{{ c.last_error }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>
    </div>
  </div>
</template>
