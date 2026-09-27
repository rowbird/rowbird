import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { createAppI18n } from '@/i18n'

import ResultsPanel from '../ResultsPanel.vue'

const result = {
  columns: [
    { name: 'id', type: 'integer', db_type: 'int' },
    { name: 'total', type: 'decimal', db_type: 'numeric' },
    { name: 'ok', type: 'boolean', db_type: 'bool' },
    { name: 'note', type: 'text', db_type: 'text' },
  ],
  rows: [[1, '12345678901234567890.5', true, null], ['9007199254740993', '0.10', false, 'x']],
  truncated: true, duration_ms: 12, timezone: 'America/Sao_Paulo',
  params: [{ name: 'today', type: 'date', value: '2026-09-25', builtin: true }],
} as const

describe('ResultsPanel', () => {
  it('formats cells with the locale and keeps decimals exact', () => {
    const w = mount(ResultsPanel, { props: { result: result as never }, global: { plugins: [createAppI18n('pt-BR')] } })
    const cells = w.findAll('tbody tr').map((tr) => tr.findAll('td').map((td) => td.text()))
    expect(cells[0]).toEqual(['1', '12.345.678.901.234.567.890,5', 'sim', 'NULL'])
    expect(cells[1]).toEqual(['9.007.199.254.740.993', '0,1', 'não', 'x'])
    expect(w.find('[data-testid="results-truncated"]').exists()).toBe(true)
    expect(w.find('[data-testid="resolved-param"]').text()).toBe('today = 2026-09-25')
    expect(w.text()).toContain('America/Sao_Paulo')
  })
})
