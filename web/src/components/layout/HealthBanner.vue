<script setup lang="ts">
import { TriangleAlert } from '@lucide/vue'
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { useDashboardStore } from '@/stores/dashboard'

/** Shown on every page while a channel or a report is failing (docs/spec/06-ui.md, "Global"). */
const { t } = useI18n()
const dashboard = useDashboardStore()

onMounted(() => dashboard.start())

const channels = computed(() => dashboard.failingChannels)
const reports = computed(() => dashboard.failingReports)
const channelLink = computed(() => (channels.value.length === 1 ? `/channels/${channels.value[0]!.id}` : '/channels'))
const reportLink = computed(() => (reports.value.length === 1 ? `/reports/${reports.value[0]!.id}` : '/reports'))
</script>

<template>
  <Alert v-if="channels.length > 0 || reports.length > 0" variant="destructive" class="mb-4" data-testid="health-banner">
    <TriangleAlert aria-hidden="true" />
    <AlertDescription class="flex flex-wrap gap-x-4 gap-y-1">
      <RouterLink v-if="channels.length > 0" :to="channelLink" class="underline underline-offset-2" data-testid="health-banner-channels">
        {{ channels.length === 1 ? t('health.channelFailing', { name: channels[0]!.name }) : t('health.channelsFailing', { count: channels.length }) }}
      </RouterLink>
      <RouterLink v-if="reports.length > 0" :to="reportLink" class="underline underline-offset-2" data-testid="health-banner-reports">
        {{ reports.length === 1 ? t('health.reportFailing', { name: reports[0]!.title }) : t('health.reportsFailing', { count: reports.length }) }}
      </RouterLink>
    </AlertDescription>
  </Alert>
</template>
