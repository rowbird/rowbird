import { afterEach, beforeEach, vi } from 'vitest'

// jsdom has no matchMedia; tests can flip `darkMode` and `mobile` to simulate the OS and viewport.
export const media = { darkMode: false, mobile: false }
const listeners = new Set<() => void>()

export function emitMediaChange() {
  listeners.forEach((l) => l())
}

beforeEach(() => {
  media.darkMode = false
  media.mobile = false
  listeners.clear()
  window.localStorage.clear()
  document.documentElement.className = ''
  vi.stubGlobal('matchMedia', (query: string) => ({
    get matches() {
      if (query.includes('prefers-color-scheme: dark')) return media.darkMode
      if (query.includes('max-width')) return media.mobile
      return false
    },
    media: query,
    onchange: null,
    addEventListener: (_: string, l: () => void) => listeners.add(l),
    removeEventListener: (_: string, l: () => void) => listeners.delete(l),
    addListener: (l: () => void) => listeners.add(l),
    removeListener: (l: () => void) => listeners.delete(l),
    dispatchEvent: () => true,
  }))
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
