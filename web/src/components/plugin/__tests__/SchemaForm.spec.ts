import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { nextTick, ref } from 'vue'

import { createAppI18n } from '@/i18n'
import { type ConfigSchema, type ConfigValues, unflatten } from '@/lib/schema'
import { pgSchema } from '@/test/schema'

import SchemaForm from '../SchemaForm.vue'

function mountForm(initial: ConfigValues) {
  const i18n = createAppI18n('en')
  i18n.global.mergeLocaleMessage('en', unflatten({
    'plugin.common.host.label': 'Host', 'plugin.common.password.label': 'Password',
    'plugin.common.tls_mode.label': 'TLS mode', 'plugin.common.tls_mode.require': 'Require TLS',
    'plugin.group.connection': 'Connection', 'plugin.group.ssh': 'SSH tunnel',
    'plugin.common.ssh_enabled.label': 'Use SSH',
  }))
  const model = ref<ConfigValues>(initial)
  const wrapper = mount(SchemaForm, {
    props: { schema: pgSchema, modelValue: model.value, 'onUpdate:modelValue': (v: ConfigValues) => { model.value = v; void wrapper.setProps({ modelValue: v }) }, errors: { host: 'Required.' } },
    global: { plugins: [i18n] },
  })
  return { wrapper, model }
}

describe('SchemaForm', () => {
  it('renders translated labels, groups, enum options and errors', () => {
    const { wrapper } = mountForm({ tls_mode: 'disable', ssh_enabled: false })
    expect(wrapper.find('[data-testid="group-connection"] legend').text()).toBe('Connection')
    expect(wrapper.find('label[for="cfg-host"]').text()).toBe('Host *')
    expect(wrapper.find('#cfg-host-error').text()).toBe('Required.')
    const options = wrapper.findAll('#cfg-tls_mode option').map((o) => o.text())
    expect(options).toEqual(['disable', 'Require TLS', 'verify-full'])
    expect(wrapper.find('#cfg-ssh_host').exists()).toBe(false)
    expect(wrapper.find('#cfg-password').attributes('type')).toBe('password')
  })

  it('shows conditional fields when their condition is met', async () => {
    const { wrapper, model } = mountForm({ tls_mode: 'disable', ssh_enabled: false })
    await wrapper.find('#cfg-tls_mode').setValue('verify-full')
    await nextTick()
    expect(model.value.tls_mode).toBe('verify-full')
    expect(wrapper.find('#cfg-tls_ca').element.tagName).toBe('TEXTAREA')
  })

  it('keeps a stored secret until the user replaces it', async () => {
    const { wrapper, model } = mountForm({ password: { configured: true } })
    expect(wrapper.find('[data-testid="secret-password"]').text()).toContain('Configured')
    expect(wrapper.find('#cfg-password').exists()).toBe(false)
    await wrapper.find('[data-testid="secret-password"] button').trigger('click')
    await nextTick()
    expect(model.value.password).toBe('')
    expect(wrapper.find('#cfg-password').exists()).toBe(true)
  })

  it('converts numbers', async () => {
    const { wrapper, model } = mountForm({})
    await wrapper.find('#cfg-port').setValue('6543')
    expect(model.value.port).toBe(6543)
  })

  it('keeps the hint next to an error and inserts template variables at the cursor', async () => {
    const i18n = createAppI18n('en')
    i18n.global.mergeLocaleMessage('en', unflatten({ 'plugin.email.subject.label': 'Subject', 'plugin.template.help': 'Use variables.' }))
    const schema: ConfigSchema = {
      type: 'object',
      required: [],
      'x-order': ['subject'],
      properties: { subject: { type: 'string', 'x-label': 'plugin.email.subject.label', 'x-help': 'plugin.template.help', 'x-group': 'connection' } },
    }
    const model = ref<ConfigValues>({ subject: 'Hello  today' })
    const wrapper = mount(SchemaForm, {
      props: { schema, modelValue: model.value, 'onUpdate:modelValue': (v: ConfigValues) => { model.value = v; void wrapper.setProps({ modelValue: v }) }, errors: { subject: 'The template has a syntax error.' } },
      global: { plugins: [i18n] },
      attachTo: document.body,
    })
    expect(wrapper.find('#cfg-subject-hint').text()).toBe('Use variables.')
    expect(wrapper.find('#cfg-subject-error').exists()).toBe(true)

    const input = wrapper.find('#cfg-subject').element as HTMLInputElement
    input.setSelectionRange(6, 6)
    const chip = wrapper.findAll('[data-testid="variables-subject"] button').find((b) => b.text() === 'report.name')
    await chip!.trigger('click')
    await nextTick()
    expect(model.value.subject).toBe('Hello {{report.name}} today')
    wrapper.unmount()
  })
})
