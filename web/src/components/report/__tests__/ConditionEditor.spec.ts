import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'

import type { Schemas } from '@/api/client'
import { createAppI18n } from '@/i18n'
import { usePluginsStore, type PluginInfo } from '@/stores/plugins'
import { conditionCatalog } from '@/test/catalog'

import ConditionEditor from '../ConditionEditor.vue'

function mountEditor(initial: Schemas['Condition'], errors: Record<string, string> = {}) {
  const model = ref<Schemas['Condition']>(initial)
  const Host = defineComponent({
    setup() {
      usePluginsStore().plugins = conditionCatalog.plugins as unknown as PluginInfo[]
      return () => h(ConditionEditor, { modelValue: model.value, 'onUpdate:modelValue': (v: Schemas['Condition']) => (model.value = v), errors })
    },
  })
  const pinia = createPinia()
  setActivePinia(pinia)
  const wrapper = mount(Host, { global: { plugins: [pinia, createAppI18n('en')] } })
  return { wrapper, model }
}

describe('ConditionEditor', () => {
  it('adds rules with their defaults, edits and removes them', async () => {
    const { wrapper, model } = mountEditor({ match: 'all', rules: [] })
    expect(wrapper.find('[data-testid="condition-always"]').exists()).toBe(true)
    // "always" is implied by an empty list, so it is not offered.
    expect(wrapper.find('#condition-add-type').findAll('option').map((o) => o.element.value)).toEqual(['has_rows', 'row_count'])

    await wrapper.find('#condition-add-type').setValue('row_count')
    await wrapper.find('[data-testid="condition-add"]').trigger('click')
    expect(model.value.rules).toEqual([{ type: 'row_count', op: 'gt', value: 0 }])

    await wrapper.find('#rule-0-value').setValue('10')
    expect(model.value.rules[0]).toEqual({ type: 'row_count', op: 'gt', value: 10 })

    await wrapper.find('#condition-add-type').setValue('has_rows')
    await wrapper.find('[data-testid="condition-add"]').trigger('click')
    await nextTick()
    await wrapper.find('#condition-match').setValue('any')
    expect(model.value.match).toBe('any')

    await wrapper.find('[data-testid="condition-remove-0"]').trigger('click')
    expect(model.value.rules).toEqual([{ type: 'has_rows' }])
  })

  it('shows errors next to the rule parameters', () => {
    const { wrapper } = mountEditor({ match: 'all', rules: [{ type: 'row_count', op: 'gt', value: -1 }] }, { 'condition.rules[0].value': 'Out of the allowed range.' })
    expect(wrapper.find('[data-testid="condition-rule-0"]').text()).toContain('Out of the allowed range.')
  })
})
