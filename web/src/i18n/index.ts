import { createI18n } from 'vue-i18n'

import en from '@/locales/en.json'
import ptBR from '@/locales/pt-BR.json'

export const SUPPORTED_LOCALES = ['en', 'pt-BR'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export const DEFAULT_LOCALE: Locale = 'en'

/** Display names are shown in their own language, so they are not translated. */
export const LOCALE_NAMES: Record<Locale, string> = {
  en: 'English',
  'pt-BR': 'Português (Brasil)',
}

export type MessageSchema = typeof en

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && (SUPPORTED_LOCALES as readonly string[]).includes(value)
}

/**
 * Picks the first supported locale from the browser preferences. An exact match wins; otherwise
 * the language part is compared, so "pt-PT" and "pt" map to "pt-BR".
 */
export function detectLocale(preferred: readonly string[]): Locale {
  for (const tag of preferred) {
    const exact = SUPPORTED_LOCALES.find((l) => l.toLowerCase() === tag.toLowerCase())
    if (exact) return exact
    const language = tag.split('-')[0]?.toLowerCase()
    const partial = SUPPORTED_LOCALES.find((l) => l.split('-')[0]?.toLowerCase() === language)
    if (partial) return partial
  }
  return DEFAULT_LOCALE
}

export function createAppI18n(locale: Locale = DEFAULT_LOCALE) {
  return createI18n<[MessageSchema], Locale>({
    legacy: false,
    locale,
    fallbackLocale: DEFAULT_LOCALE,
    messages: { en, 'pt-BR': ptBR },
  })
}

export type AppI18n = ReturnType<typeof createAppI18n>
