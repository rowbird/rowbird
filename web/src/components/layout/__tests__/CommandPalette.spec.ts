import { describe, expect, it, vi } from 'vitest'

import App from '@/App.vue'
import { useSessionStore } from '@/stores/session'
import { json, mockApi, sampleMe } from '@/test/api'
import { flush, mountView } from '@/test/mount'

const $ = (sel: string) => document.body.querySelector<HTMLElement>(sel)

describe('CommandPalette', () => {
  it('opens with Ctrl+K, finds a report ignoring accents and opens it with Enter', async () => {
    mockApi({
      'GET /api/v1/reports': () => json(200, { items: [{ id: 'r1', title: 'Vendas diárias', slug: 'vendas' }] }),
      'GET /api/v1/queries': () => json(200, { items: [] }),
      'GET /api/v1/connections': () => json(200, { items: [] }),
      'GET /api/v1/channels': () => json(200, { items: [] }),
    })
    const { wrapper, router } = await mountView(App, { path: '/' })
    useSessionStore().me = { ...sampleMe, role: 'viewer' }
    await flush()

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    await vi.waitFor(() => expect($('[data-testid="palette-input"]')).not.toBeNull())
    // Viewers are not offered to create anything.
    expect($('[data-testid="palette-item-action:/queries/new"]')).toBeNull()
    expect($('[data-testid="palette-item-page:/runs"]')).not.toBeNull()

    const input = $('[data-testid="palette-input"]') as HTMLInputElement
    input.value = 'diarias'
    input.dispatchEvent(new Event('input'))
    await vi.waitFor(() => expect($('[data-testid="palette-item-report:r1"]')).not.toBeNull())
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
    await vi.waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/reports/r1'))
    wrapper.unmount()
  })
})
