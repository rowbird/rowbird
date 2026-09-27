import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import PluginIcon from '../PluginIcon.vue'

describe('PluginIcon', () => {
  it('draws the brand logo when there is a free one, and a generic icon otherwise', () => {
    const pg = mount(PluginIcon, { props: { icon: 'postgres' } })
    expect(pg.find('path').attributes('d')).toBeTruthy()
    expect(pg.find('svg').attributes('fill')).toBe('#4169E1')

    for (const icon of ['slack', 'mssql', 'bucket', 'unknown']) {
      const w = mount(PluginIcon, { props: { icon } })
      expect(w.find('svg').attributes('fill')).not.toMatch(/^#/)
      expect(w.find('svg').attributes('data-icon')).toBe(icon)
    }
  })
})
