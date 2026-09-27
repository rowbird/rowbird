import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import SetupView from '../SetupView.vue'

async function mountWith(source: 'generated' | 'env', tokenRequired = false) {
  const view = await mountView(SetupView, { path: '/setup' })
  const session = useSessionStore()
  session.setupStatus = { setup_required: true, token_required: tokenRequired, master_key: { source, path: '/data/master.key' }, passkeys_available: false, oidc_enabled: false, password_reset_available: false }
  session.setupRequired = true
  await flush()
  return view
}

describe('SetupView', () => {
  it('blocks submission until the generated master key is backed up', async () => {
    const { wrapper } = await mountWith('generated')
    const submit = wrapper.find('[data-testid="setup-submit"]')
    expect(wrapper.find('[data-testid="master-key-warning"]').text()).toContain('/data/master.key')
    expect(submit.attributes('disabled')).toBeDefined()
    await wrapper.find('[data-testid="master-key-ack"]').trigger('click')
    expect(submit.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('has no warning when the key comes from the environment, and sends the form', async () => {
    const calls = mockApi({ 'POST /api/v1/setup': () => json(201, sampleMe) })
    const { wrapper, router } = await mountWith('env', true)
    expect(wrapper.find('[data-testid="master-key-warning"]').exists()).toBe(false)
    await wrapper.find('#token').setValue('tok')
    await wrapper.find('#name').setValue('Ana')
    await wrapper.find('#email').setValue('ana@example.com')
    await wrapper.find('#password').setValue('a fine passphrase')
    await wrapper.find('#confirm').setValue('a fine passphrase')
    await wrapper.find('form').trigger('submit')
    await flush()
    await vi.waitFor(() => expect(calls).toHaveLength(1))
    const body = await calls[0]!.json()
    expect(body).toMatchObject({ token: 'tok', name: 'Ana', email: 'ana@example.com', password: 'a fine passphrase', locale: 'en' })
    expect(typeof body.timezone).toBe('string')
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('home'))
    wrapper.unmount()
  })

  it('shows field errors from the server', async () => {
    mockApi({
      'POST /api/v1/setup': () => json(400, { code: 'validation.failed', errors: [{ field: 'password', code: 'validation.password_common' }] }),
    })
    const { wrapper } = await mountWith('env')
    for (const [id, v] of [['#name', 'Ana'], ['#email', 'ana@example.com'], ['#password', 'password123'], ['#confirm', 'password123']]) {
      await wrapper.find(id!).setValue(v)
    }
    await wrapper.find('form').trigger('submit')
    await flush()
    expect(wrapper.find('#password-error').text()).toBe('This password is too common or too easy to guess.')
    wrapper.unmount()
  })

  it('checks that both passwords match before sending', async () => {
    const calls = mockApi({})
    const { wrapper } = await mountWith('env')
    await wrapper.find('#password').setValue('a fine passphrase')
    await wrapper.find('#confirm').setValue('another passphrase')
    await wrapper.find('form').trigger('submit')
    await flush()
    expect(calls).toHaveLength(0)
    expect(wrapper.find('#confirm-error').exists()).toBe(true)
    wrapper.unmount()
  })
})
