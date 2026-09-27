import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { emitMediaChange, media } from '@/test/setup'

import { usePreferencesStore } from '../preferences'

describe('preferences store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('defaults to the system theme and follows the OS', async () => {
    const prefs = usePreferencesStore()
    expect(prefs.theme).toBe('system')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    media.darkMode = true
    emitMediaChange()
    expect(document.documentElement.classList.contains('dark')).toBe(true)

    prefs.setTheme('light')
    await nextTick()
    emitMediaChange()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('applies and persists an explicit theme', async () => {
    const prefs = usePreferencesStore()
    prefs.setTheme('dark')
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(window.localStorage.getItem('rowbird.theme')).toBe('dark')

    setActivePinia(createPinia())
    expect(usePreferencesStore().theme).toBe('dark')
  })

  it('cycles system, light, dark', () => {
    const prefs = usePreferencesStore()
    const seen = [prefs.theme]
    for (let i = 0; i < 3; i++) {
      prefs.cycleTheme()
      seen.push(prefs.theme)
    }
    expect(seen).toEqual(['system', 'light', 'dark', 'system'])
  })

  it('ignores invalid stored values', () => {
    window.localStorage.setItem('rowbird.theme', 'neon')
    window.localStorage.setItem('rowbird.locale', 'xx')
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['pt-BR'])
    const prefs = usePreferencesStore()
    expect(prefs.theme).toBe('system')
    expect(prefs.locale).toBe('pt-BR')
  })

  it('persists the locale', async () => {
    const prefs = usePreferencesStore()
    prefs.setLocale('pt-BR')
    await nextTick()
    expect(window.localStorage.getItem('rowbird.locale')).toBe('pt-BR')
  })

  it('works when storage is unavailable', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const prefs = usePreferencesStore()
    prefs.setTheme('dark')
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    vi.restoreAllMocks()
  })
})
