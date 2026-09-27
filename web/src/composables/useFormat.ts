import { useI18n } from 'vue-i18n'

/** Formats dates, counts and sizes with the user's language (AGENTS.md, "Frontend conventions"). */
export function useFormat() {
  const { locale, t } = useI18n()
  const count = (n: number, digits = 0) => new Intl.NumberFormat(locale.value, { maximumFractionDigits: digits }).format(n)
  return {
    dateTime(value?: string | null) {
      if (!value) return ''
      return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
    },
    count,
    /** A byte count in the largest unit that keeps it above one (1.5 MB). */
    size(n: number) {
      const units = ['bytes', 'kb', 'mb', 'gb', 'tb'] as const
      let i = 0
      let v = n
      while (v >= 1024 && i < units.length - 1) {
        v /= 1024
        i++
      }
      return t(`units.${units[i]}`, { n: count(v, i === 0 ? 0 : 1) })
    },
  }
}
