/** IANA time zones known to the browser, UTC first. */
export function timeZones(): string[] {
  const zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []
  return ['UTC', ...zones.filter((z) => z !== 'UTC')]
}

/** The browser's own time zone, used as the default choice. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}
