import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppI18n } from '@/i18n'
import { createAppRouter } from '@/router'
import { useSessionStore } from '@/stores/session'
import { sampleMe } from '@/test/api'

import OnboardingChecklist from '../OnboardingChecklist.vue'

function mountAs(role: 'viewer' | 'editor' | 'admin', state: Record<string, boolean>) {
  const pinia = createPinia()
  setActivePinia(pinia)
  useSessionStore().me = { ...sampleMe, role }
  return mount(OnboardingChecklist, { props: { state: state as never }, global: { plugins: [pinia, createAppI18n('en'), createAppRouter(createMemoryHistory())] } })
}

const none = { has_connection: false, has_query: false, has_report: false, has_delivery: false }

describe('OnboardingChecklist', () => {
  beforeEach(() => localStorage.clear())

  it('offers the next step and hides when asked', async () => {
    const w = mountAs('admin', { ...none, has_connection: true })
    expect(w.find('[data-testid="onboarding-connection"]').attributes('data-done')).toBe('true')
    expect(w.find('[data-testid="onboarding-query"] a').attributes('href')).toBe('/queries/new')
    // Only the next step has an action.
    expect(w.find('[data-testid="onboarding-report"] a').exists()).toBe(false)
    await w.find('[data-testid="onboarding-hide"]').trigger('click')
    expect(w.find('[data-testid="onboarding"]').exists()).toBe(false)
    expect(mountAs('admin', none).find('[data-testid="onboarding"]').exists()).toBe(false)
  })

  it('tells editors to ask for a connection, and stays away from viewers and when done', () => {
    expect(mountAs('editor', none).find('[data-testid="onboarding-connection"]').text()).toContain('Ask an admin')
    expect(mountAs('viewer', none).find('[data-testid="onboarding"]').exists()).toBe(false)
    expect(mountAs('admin', { has_connection: true, has_query: true, has_report: true, has_delivery: true }).find('[data-testid="onboarding"]').exists()).toBe(false)
  })
})
