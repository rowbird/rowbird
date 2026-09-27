import { describe, expect, it, vi } from 'vitest'

import { json, mockApi } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import ForgotPasswordView from '../ForgotPasswordView.vue'
import ResetPasswordView from '../ResetPasswordView.vue'

describe('password reset', () => {
  it('asks for the link and always shows the same confirmation', async () => {
    const calls = mockApi({ 'POST /api/v1/auth/password-reset': () => new Response(null, { status: 202 }) })
    const { wrapper } = await mountView(ForgotPasswordView, { path: '/forgot-password' })
    await wrapper.find('[data-testid="forgot-email"]').setValue('ana@example.com')
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="forgot-sent"]').text()).toContain('If ana@example.com has an account'))
    expect(await calls[0]!.json()).toEqual({ email: 'ana@example.com' })
    wrapper.unmount()
  })

  it('sets the new password with the token, and explains a used link', async () => {
    let used = false
    const calls = mockApi({
      'POST /api/v1/auth/password-reset/confirm': () => {
        if (used) return json(410, { code: 'auth.reset_invalid', status: 410 })
        used = true
        return new Response(null, { status: 204 })
      },
    })
    const { wrapper } = await mountView(ResetPasswordView, { path: '/reset-password?token=rbr_abc' })
    await wrapper.find('[data-testid="reset-password"]').setValue('a brand new passphrase')
    await wrapper.find('[data-testid="reset-confirm"]').setValue('something else')
    await wrapper.find('form').trigger('submit')
    await flush()
    expect(calls).toHaveLength(0)
    expect(wrapper.text()).toContain('match')

    await wrapper.find('[data-testid="reset-confirm"]').setValue('a brand new passphrase')
    await wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(wrapper.find('[data-testid="reset-done"]').exists()).toBe(true))
    expect(await calls[0]!.json()).toEqual({ token: 'rbr_abc', password: 'a brand new passphrase' })
    wrapper.unmount()

    const again = await mountView(ResetPasswordView, { path: '/reset-password?token=rbr_abc' })
    await again.wrapper.find('[data-testid="reset-password"]').setValue('a brand new passphrase')
    await again.wrapper.find('[data-testid="reset-confirm"]').setValue('a brand new passphrase')
    await again.wrapper.find('form').trigger('submit')
    await vi.waitFor(() => expect(again.wrapper.find('[data-testid="reset-invalid"]').exists()).toBe(true))
    again.wrapper.unmount()
  })
})
