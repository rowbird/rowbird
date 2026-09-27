import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { nextTick, ref } from 'vue'

import type { Schemas } from '@/api/client'
import { createAppI18n } from '@/i18n'

import ParamsPanel from '../ParamsPanel.vue'

function mountPanel(sql: string, initial: Schemas['QueryParam'][] = []) {
  const params = ref(initial)
  const values = ref<Record<string, string>>({})
  const wrapper = mount(ParamsPanel, {
    props: {
      dialect: 'postgres', sql,
      params: params.value, 'onUpdate:params': (v: Schemas['QueryParam'][]) => { params.value = v; void Promise.resolve().then(() => wrapper.setProps({ params: v })) },
      values: values.value, 'onUpdate:values': (v: Record<string, string>) => { values.value = v; void Promise.resolve().then(() => wrapper.setProps({ values: v })) },
    },
    global: { plugins: [createAppI18n('en')] },
  })
  return { wrapper, params, values }
}

describe('ParamsPanel', () => {
  it('adds a definition for every user parameter and lists built-ins', async () => {
    const { wrapper, params } = mountPanel("select * from t where d >= {{today}} and r = {{region}} and '{{ignored}}' <> ''")
    await nextTick()
    await nextTick()
    expect(params.value).toEqual([{ name: 'region', type: 'text', default: null }])
    expect(wrapper.text()).toContain('today')
    expect(wrapper.find('[data-testid="param-region"]').exists()).toBe(true)
  })

  it('flags definitions the SQL no longer uses and lets them be removed', async () => {
    const { wrapper, params } = mountPanel('select 1', [{ name: 'old', type: 'integer', default: '5' }])
    await nextTick()
    expect(wrapper.find('[data-testid="param-old"]').text()).toContain('Not used in the SQL')
    await wrapper.find('[data-testid="param-old"] button').trigger('click')
    expect(params.value).toEqual([])
  })

  it('edits type, default and preview value', async () => {
    const { wrapper, params, values } = mountPanel('select {{n}}')
    await nextTick()
    await nextTick()
    await wrapper.find('#param-n-type').setValue('integer')
    await wrapper.find('#param-n-default').setValue('10')
    await wrapper.find('#param-n-value').setValue('3')
    expect(params.value).toEqual([{ name: 'n', type: 'integer', default: '10' }])
    expect(values.value).toEqual({ n: '3' })
  })
})
