import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

import { detectLocale, isLocale, type Locale } from '@/i18n'

export type Theme = 'system' | 'light' | 'dark'
export const THEMES: readonly Theme[] = ['system', 'light', 'dark']

// Until users exist (Phase 1) preferences live in the browser; afterwards they come from the profile.
const THEME_KEY = 'rowbird.theme'
const LOCALE_KEY = 'rowbird.locale'

function read(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function write(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // Storage can be unavailable (private mode, blocked site data); preferences then last one visit.
  }
}

function prefersDark(): MediaQueryList | null {
  return typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : null
}

/** Adds or removes the `dark` class on <html> for the given theme. */
export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement) {
  const dark = theme === 'dark' || (theme === 'system' && (prefersDark()?.matches ?? false))
  root.classList.toggle('dark', dark)
  root.style.colorScheme = dark ? 'dark' : 'light'
}

export const usePreferencesStore = defineStore('preferences', () => {
  const storedTheme = read(THEME_KEY)
  const theme = ref<Theme>(THEMES.includes(storedTheme as Theme) ? (storedTheme as Theme) : 'system')

  const storedLocale = read(LOCALE_KEY)
  const locale = ref<Locale>(isLocale(storedLocale) ? storedLocale : detectLocale(navigator.languages ?? []))

  function setTheme(value: Theme) {
    theme.value = value
  }

  function cycleTheme() {
    theme.value = THEMES[(THEMES.indexOf(theme.value) + 1) % THEMES.length] ?? 'system'
  }

  function setLocale(value: Locale) {
    locale.value = value
  }

  watch(
    theme,
    (value) => {
      write(THEME_KEY, value)
      applyTheme(value)
    },
    { immediate: true },
  )
  watch(locale, (value) => write(LOCALE_KEY, value))

  // Follow the operating system while the theme is "system".
  prefersDark()?.addEventListener('change', () => {
    if (theme.value === 'system') applyTheme('system')
  })

  return { theme, locale, setTheme, cycleTheme, setLocale }
})
