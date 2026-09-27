import { toast } from 'vue-sonner'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/errors'
import type { Locale } from '@/i18n'
import { type Theme, usePreferencesStore } from '@/stores/preferences'
import { useSessionStore } from '@/stores/session'

/** Changes language or theme and, when signed in, saves it to the user's profile. */
export function usePreferenceActions() {
  const prefs = usePreferencesStore()
  const session = useSessionStore()
  const { t, te } = useI18n()

  async function save(patch: { locale?: Locale; theme?: Theme }) {
    if (!session.me) return
    try {
      await session.updateMe(patch)
    } catch (e) {
      toast.error(errorMessage(e, t, te))
    }
  }

  return {
    setLocale(value: Locale) {
      prefs.setLocale(value)
      void save({ locale: value })
    },
    setTheme(value: Theme) {
      prefs.setTheme(value)
      void save({ theme: value })
    },
    cycleTheme() {
      prefs.cycleTheme()
      void save({ theme: prefs.theme })
    },
  }
}
