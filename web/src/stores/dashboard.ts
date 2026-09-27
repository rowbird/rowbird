import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, type Schemas } from '@/api/client'
import { unwrap } from '@/api/errors'

import { useEventsStore } from './events'

/** How long events are collected before the summary is fetched again. */
const DEBOUNCE_MS = 1000

/**
 * The workspace summary behind the home page and the health banner. It is fetched again, at most
 * once a second, when runs, reports or channels change.
 */
export const useDashboardStore = defineStore('dashboard', () => {
  const data = ref<Schemas['Dashboard'] | null>(null)
  let listening = false
  let timer: ReturnType<typeof setTimeout> | undefined

  async function load() {
    data.value = unwrap(await api.GET('/api/v1/dashboard'))
  }

  function start() {
    if (listening) return
    listening = true
    useEventsStore().on((e) => {
      if (e.type === 'notification.created') return
      clearTimeout(timer)
      timer = setTimeout(() => void load().catch(() => undefined), DEBOUNCE_MS)
    })
    void load().catch(() => undefined)
  }

  const failingChannels = computed(() => data.value?.failing_channels ?? [])
  const failingReports = computed(() => data.value?.failing_reports ?? [])

  return { data, load, start, failingChannels, failingReports }
})
