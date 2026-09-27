import { describe, expect, it } from 'vitest'

import App from '@/App.vue'
import type { Locale } from '@/i18n'
import { useSessionStore } from '@/stores/session'
import { sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

async function mountApp(locale: Locale, path = '/') {
  const view = await mountView(App, { locale, path })
  useSessionStore().me = { ...sampleMe, locale }
  await flush()
  return view
}

const expected: Record<Locale, string[]> = {
  en: ['Home', 'Queries', 'Reports', 'Runs', 'Links', 'Connections', 'Channels', 'Settings'],
  'pt-BR': ['Início', 'Consultas', 'Relatórios', 'Execuções', 'Links', 'Conexões', 'Canais', 'Configurações'],
}

describe('app shell', () => {
  it.each(Object.entries(expected) as [Locale, string[]][])('renders the navigation in %s', async (locale, labels) => {
    const { wrapper } = await mountApp(locale)
    const nav = wrapper.findAll('[data-testid^="nav-"]').map((a) => a.text())
    expect(nav).toEqual(labels)
    expect(wrapper.text()).toContain(locale === 'en' ? 'Build' : 'Criar')
    expect(document.documentElement.lang).toBe(locale)
    wrapper.unmount()
  })

  it('collapses the sidebar on editors and remembers each choice', async () => {
    localStorage.clear()
    const { wrapper, router } = await mountApp('en', '/reports')
    const state = () => wrapper.find('[data-slot="sidebar"][data-state]').attributes('data-state')
    expect(state()).toBe('expanded')
    await router.push('/queries/new')
    await flush()
    expect(state()).toBe('collapsed')
    // Expanding it on an editor is remembered for editors only.
    await wrapper.find('[data-sidebar="trigger"]').trigger('click')
    await flush()
    expect(state()).toBe('expanded')
    expect(localStorage.getItem('rowbird.sidebar.wide')).toBe('true')
    await router.push('/reports')
    await flush()
    expect(state()).toBe('expanded')
    expect(localStorage.getItem('rowbird.sidebar.page')).toBeNull()
    wrapper.unmount()
  })

  it('marks the current section and shows its placeholder', async () => {
    const { wrapper } = await mountApp('en', '/reports')
    await flush()
    expect(wrapper.find('[data-testid="page-title"]').text()).toBe('Reports')
    expect(wrapper.find('[data-testid="nav-reports"]').attributes('data-active')).toBe('true')
    expect(wrapper.find('[data-testid="nav-home"]').attributes('data-active')).toBe('false')
    expect(document.title).toBe('Reports · Rowbird')
    wrapper.unmount()
  })

  it('shows a not found page for unknown paths', async () => {
    const { wrapper } = await mountApp('pt-BR', '/nao/existe')
    await flush()
    expect(wrapper.find('[data-testid="page-title"]').text()).toBe('Página não encontrada')
    wrapper.unmount()
  })

  it('uses the bare layout for sign-in pages', async () => {
    const { wrapper } = await mountView(App, { path: '/login' })
    await flush()
    expect(wrapper.find('[data-testid="nav-home"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="login-submit"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
