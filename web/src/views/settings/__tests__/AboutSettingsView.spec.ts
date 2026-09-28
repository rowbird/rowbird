import { describe, expect, it, vi } from 'vitest'

import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

import AboutSettingsView from '../AboutSettingsView.vue'

const about = {
  version: '1.2.0', commit: 'abc123', build_date: '2026-09-01', go_version: 'go1.27', platform: 'linux/amd64',
  update_check_allowed: true, update_check_enabled: true, update_available: true, latest_version: '1.3.0',
  release_url: 'https://github.com/rowbird/rowbird/releases/tag/v1.3.0', checked_at: '2026-09-25T00:00:00Z',
}

describe('AboutSettingsView', () => {
  it('shows the version, a new release, and turns the check off', async () => {
    let current = { ...about }
    const calls = mockApi({
      'GET /api/v1/system/about': () => json(200, current),
      'PATCH /api/v1/settings': async (req) => {
        const body = await req.json()
        current = { ...current, update_check_enabled: body.update_check, update_available: false }
        return json(200, {})
      },
    })
    const { wrapper } = await mountView(AboutSettingsView, { path: '/settings/about' })
    useSessionStore().me = { ...sampleMe }
    await vi.waitFor(() => expect(wrapper.find('[data-testid="about-version"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="about-version"]').text()).toBe('1.2.0')
    expect(wrapper.find('[data-testid="update-available"]').text()).toContain('1.3.0 is available')

    await wrapper.find('[data-testid="update-check"]').trigger('click')
    await flush()
    const patch = await calls.find((c) => c.method === 'PATCH')!.json()
    expect(patch).toEqual({ update_check: false })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="update-available"]').exists()).toBe(false))
    wrapper.unmount()
  })

  it('says when the server turned the check off', async () => {
    mockApi({ 'GET /api/v1/system/about': () => json(200, { ...about, update_check_allowed: false, update_available: false }) })
    const { wrapper } = await mountView(AboutSettingsView, { path: '/settings/about' })
    await vi.waitFor(() => expect(wrapper.find('[data-testid="update-check-disabled"]').exists()).toBe(true))
    expect(wrapper.find('[data-testid="update-check"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
