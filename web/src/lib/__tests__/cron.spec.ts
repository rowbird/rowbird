import { describe, expect, it } from 'vitest'

import { defaultSchedule, fromCron, type ScheduleModel, toCron } from '../cron'

const model = (m: Partial<ScheduleModel>): ScheduleModel => ({ ...defaultSchedule(), ...m })

describe('cron builder', () => {
  it('builds expressions', () => {
    expect(toCron(model({ kind: 'daily', time: '07:30' }))).toBe('30 7 * * *')
    expect(toCron(model({ kind: 'weekdays', time: '08:00' }))).toBe('0 8 * * 1-5')
    expect(toCron(model({ kind: 'weekly', time: '18:05', days: [5, 1, 1] }))).toBe('5 18 * * 1,5')
    expect(toCron(model({ kind: 'weekly', days: [] }))).toBe('0 8 * * 1')
    expect(toCron(model({ kind: 'monthly', time: '06:00', dayOfMonth: 40 }))).toBe('0 6 31 * *')
    expect(toCron(model({ kind: 'hourly', minute: 15 }))).toBe('15 * * * *')
    expect(toCron(model({ kind: 'minutes', every: 5 }))).toBe('*/5 * * * *')
    expect(toCron(model({ kind: 'minutes', every: 1 }))).toBe('* * * * *')
    expect(toCron(model({ kind: 'custom', expression: ' 0 7 1-7 * 1 ' }))).toBe('0 7 1-7 * 1')
  })

  it('reads expressions back', () => {
    const cases: [string, Partial<ScheduleModel>][] = [
      ['30 7 * * *', { kind: 'daily', time: '07:30' }],
      ['0 8 * * 1-5', { kind: 'weekdays', time: '08:00' }],
      ['5 18 * * 1,5', { kind: 'weekly', time: '18:05', days: [1, 5] }],
      ['0 6 31 * *', { kind: 'monthly', time: '06:00', dayOfMonth: 31 }],
      ['15 * * * *', { kind: 'hourly', minute: 15 }],
      ['*/5 * * * *', { kind: 'minutes', every: 5 }],
      ['* * * * *', { kind: 'minutes', every: 1 }],
      ['*/7 * * * *', { kind: 'custom' }],
      ['0 25 * * *', { kind: 'custom' }],
      ['@daily', { kind: 'custom', expression: '@daily' }],
      ['0 7 1-7 * 1', { kind: 'custom' }],
    ]
    for (const [expr, want] of cases) {
      expect(fromCron(expr), expr).toMatchObject(want)
      if (want.kind !== 'custom') expect(toCron(fromCron(expr))).toBe(expr)
    }
  })
})
