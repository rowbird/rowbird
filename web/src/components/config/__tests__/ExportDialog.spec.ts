import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { describe, expect, it, vi } from 'vitest'

import { createAppI18n } from '@/i18n'
import { mockApi } from '@/test/api'
import { flush } from '@/test/mount'

import * as download from '../download'
import ExportDialog from '../ExportDialog.vue'

describe('ExportDialog', () => {
  it('exports the chosen reports with their connections and saves the YAML', async () => {
    const calls = mockApi({ 'POST /api/v1/export': () => new Response('apiVersion: rowbird.dev/v1\n', { status: 200, headers: { 'Content-Type': 'application/yaml' } }) })
    const save = vi.spyOn(download, 'downloadText').mockImplementation(() => {})
    setActivePinia(createPinia())
    const wrapper = mount(ExportDialog, { props: { open: true, reports: ['daily-sales'] }, global: { plugins: [createAppI18n('en')] }, attachTo: document.body })
    await flush()
    document.querySelector<HTMLButtonElement>('[data-testid="export-channels"]')!.click()
    await flush()
    document.querySelector<HTMLButtonElement>('[data-testid="export-download"]')!.click()
    await vi.waitFor(() => expect(save).toHaveBeenCalledWith('daily-sales.yaml', 'apiVersion: rowbird.dev/v1\n'))
    expect(await calls[0]!.json()).toEqual({ reports: ['daily-sales'], include_connections: true, include_channels: false })
    wrapper.unmount()
  })
})
