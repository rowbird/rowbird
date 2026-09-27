import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppI18n } from '@/i18n'
import { createAppRouter } from '@/router'

import RunHistoryChart from '../RunHistoryChart.vue'

const run = (id: string, status: string, ms: number | null, rows: number | null) => ({
  id, report_id: 'r', report_title: 'R', trigger: 'schedule', triggered_by_name: null, deliver: true, status,
  scheduled_for: '2026-09-25T10:00:00Z', started_at: '2026-09-25T10:00:00Z', finished_at: null, duration_ms: ms, row_count: rows,
  truncated: false, attempt: 1, error_code: null, created_at: '2026-09-25T10:00:00Z',
})

describe('RunHistoryChart', () => {
  it('draws one bar per run and summarizes the finished ones', async () => {
    const router = createAppRouter(createMemoryHistory())
    // Newest first, as the API lists them.
    const runs = [run('c', 'failed', 3000, null), run('b', 'success', 1000, 12), run('a', 'success', 200, 3), run('s', 'skipped', null, null)]
    const w = mount(RunHistoryChart, { props: { runs: runs as never }, global: { plugins: [createAppI18n('en'), router] } })
    const bars = w.findAll('[data-testid="run-bar"]')
    expect(bars.map((b) => b.attributes('href'))).toEqual(['/runs/s', '/runs/a', '/runs/b', '/runs/c'])
    expect(bars[3]!.find('rect').classes()).toContain('fill-destructive')
    expect(bars[2]!.find('title').text()).toContain('12 rows')
    expect(w.find('[data-testid="run-history-rate"]').text()).toBe('67%')
    expect(w.find('[data-testid="run-history-median"]').text()).toBe('1 s')
    expect(w.find('svg').attributes('aria-label')).toContain('67% succeeded')
    await bars[0]!.trigger('click')
    await router.isReady()
    expect(router.currentRoute.value.fullPath).toBe('/runs/s')
  })
})
