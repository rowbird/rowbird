/**
 * The schedule builder of the report editor (docs/spec/03-flows.md, section 4): a few common
 * shapes that map to five-field cron expressions and back. Anything else is a custom expression,
 * which the server validates and describes.
 */

export type ScheduleKind = 'daily' | 'weekdays' | 'weekly' | 'monthly' | 'hourly' | 'minutes' | 'custom'

export const SCHEDULE_KINDS: readonly ScheduleKind[] = ['daily', 'weekdays', 'weekly', 'monthly', 'hourly', 'minutes', 'custom']

/** Intervals offered for "every N minutes"; they divide an hour evenly. */
export const MINUTE_STEPS: readonly number[] = [1, 2, 5, 10, 15, 20, 30]

export interface ScheduleModel {
  kind: ScheduleKind
  /** HH:MM, for daily, weekdays, weekly and monthly. */
  time: string
  /** Days of the week (0 = Sunday), for weekly. */
  days: number[]
  /** Day of the month, for monthly. */
  dayOfMonth: number
  /** Minute past the hour, for hourly. */
  minute: number
  /** Step, for minutes. */
  every: number
  /** The expression, for custom. */
  expression: string
}

export function defaultSchedule(): ScheduleModel {
  return { kind: 'daily', time: '08:00', days: [1], dayOfMonth: 1, minute: 0, every: 15, expression: '0 8 * * *' }
}

function hm(time: string): [number, number] {
  const m = /^(\d{1,2}):(\d{2})$/.exec(time)
  if (!m) return [0, 0]
  return [Math.min(Number(m[1]), 23), Math.min(Number(m[2]), 59)]
}

const pad = (n: number) => String(n).padStart(2, '0')

/** The cron expression for a model. */
export function toCron(m: ScheduleModel): string {
  const [h, min] = hm(m.time)
  switch (m.kind) {
    case 'daily':
      return `${min} ${h} * * *`
    case 'weekdays':
      return `${min} ${h} * * 1-5`
    case 'weekly': {
      const days = [...new Set(m.days)].filter((d) => d >= 0 && d <= 6).sort((a, b) => a - b)
      return `${min} ${h} * * ${days.length ? days.join(',') : '1'}`
    }
    case 'monthly':
      return `${min} ${h} ${Math.min(Math.max(Math.trunc(m.dayOfMonth) || 1, 1), 31)} * *`
    case 'hourly':
      return `${Math.min(Math.max(Math.trunc(m.minute) || 0, 0), 59)} * * * *`
    case 'minutes':
      return m.every === 1 ? '* * * * *' : `*/${m.every} * * * *`
    default:
      return m.expression.trim()
  }
}

const num = String.raw`(\d{1,2})`

/** The builder model for an expression; shapes the builder does not know become custom. */
export function fromCron(expr: string): ScheduleModel {
  const e = expr.trim().split(/\s+/).join(' ')
  const base = { ...defaultSchedule(), expression: e }
  const time = (h: string, min: string) => `${pad(Number(h))}:${pad(Number(min))}`
  const valid = (h: string, min: string) => Number(h) <= 23 && Number(min) <= 59
  let m: RegExpExecArray | null
  if ((m = new RegExp(`^${num} ${num} \\* \\* \\*$`).exec(e)) && valid(m[2]!, m[1]!)) {
    return { ...base, kind: 'daily', time: time(m[2]!, m[1]!) }
  }
  if ((m = new RegExp(`^${num} ${num} \\* \\* 1-5$`).exec(e)) && valid(m[2]!, m[1]!)) {
    return { ...base, kind: 'weekdays', time: time(m[2]!, m[1]!) }
  }
  if ((m = new RegExp(`^${num} ${num} \\* \\* ([0-6](?:,[0-6])*)$`).exec(e)) && valid(m[2]!, m[1]!)) {
    return { ...base, kind: 'weekly', time: time(m[2]!, m[1]!), days: m[3]!.split(',').map(Number) }
  }
  if ((m = new RegExp(`^${num} ${num} ${num} \\* \\*$`).exec(e)) && valid(m[2]!, m[1]!) && Number(m[3]) >= 1 && Number(m[3]) <= 31) {
    return { ...base, kind: 'monthly', time: time(m[2]!, m[1]!), dayOfMonth: Number(m[3]) }
  }
  if ((m = new RegExp(`^${num} \\* \\* \\* \\*$`).exec(e)) && Number(m[1]) <= 59) {
    return { ...base, kind: 'hourly', minute: Number(m[1]) }
  }
  if (e === '* * * * *') return { ...base, kind: 'minutes', every: 1 }
  if ((m = /^\*\/(\d{1,2}) \* \* \* \*$/.exec(e)) && MINUTE_STEPS.includes(Number(m[1]))) {
    return { ...base, kind: 'minutes', every: Number(m[1]) }
  }
  return { ...base, kind: 'custom' }
}
