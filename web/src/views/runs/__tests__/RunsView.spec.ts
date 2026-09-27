import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { mountView } from '@/test/mount'

import RunsView from '../RunsView.vue'

describe('RunsView', () => {
  it('says when the filters hide every run, and clears them', async () => {
    mockApi({
      'GET /api/v1/runs': () => json(200, { items: [], next_cursor: null }),
      'GET /api/v1/reports': () => json(200, { items: [] }),
    })
    const { wrapper, router } = await mountView(RunsView, { path: '/runs?status=failed' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="runs-filtered-empty"]').exists()).toBe(true))
    await wrapper.find('[data-testid="runs-clear-filters"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/runs'))
    // Without filters, the general empty state explains what runs are.
    await vi.waitFor(() => expect(wrapper.text()).toContain('No runs yet'))
    wrapper.unmount()
  })
})
