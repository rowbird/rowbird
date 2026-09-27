import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import { FAST_POLL_MS, SLOW_POLL_MS, useLiveRefresh } from '@/composables/useLiveRefresh'
import { useEventsStore } from '@/stores/events'

function harness(active: () => boolean) {
  const refresh = vi.fn(async () => undefined)
  const Comp = defineComponent({
    setup() {
      useLiveRefresh(refresh, { events: ['run.updated'], match: (e) => e.data.run_id === 'r1', active })
      return () => h('div')
    },
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  const wrapper = mount(Comp, { global: { plugins: [pinia] } })
  return { refresh, wrapper, events: useEventsStore() }
}

afterEach(() => vi.useRealTimers())

describe('useLiveRefresh', () => {
  it('refreshes on matching events only', async () => {
    const { refresh, wrapper, events } = harness(() => false)
    events.emit({ type: 'run.updated', data: { run_id: 'r2' } })
    events.emit({ type: 'report.updated', data: { run_id: 'r1' } })
    expect(refresh).not.toHaveBeenCalled()
    events.emit({ type: 'run.updated', data: { run_id: 'r1' } })
    await vi.waitFor(() => expect(refresh).toHaveBeenCalledTimes(1))
    wrapper.unmount()
    events.emit({ type: 'run.updated', data: { run_id: 'r1' } })
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('polls while something is active: quickly without a stream, slowly with one', async () => {
    vi.useFakeTimers()
    let active = true
    const { refresh, wrapper, events } = harness(() => active)
    await vi.advanceTimersByTimeAsync(FAST_POLL_MS)
    expect(refresh).toHaveBeenCalledTimes(1)
    events.connected = true
    await vi.advanceTimersByTimeAsync(FAST_POLL_MS * 2)
    expect(refresh).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(SLOW_POLL_MS)
    expect(refresh).toHaveBeenCalledTimes(2)
    active = false
    await vi.advanceTimersByTimeAsync(SLOW_POLL_MS * 2)
    expect(refresh).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
