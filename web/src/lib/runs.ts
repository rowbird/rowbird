import type { Schemas } from '@/api/client'

export type RunStatus = Schemas['RunStatus']

const ACTIVE: readonly RunStatus[] = ['pending', 'running']

/** Whether a run has not finished yet (views refresh while one is shown). */
export function isActive(status: RunStatus): boolean {
  return ACTIVE.includes(status)
}

type Translate = (key: string, named?: Record<string, unknown>) => string
type HasKey = (key: string) => boolean

/** The message for a run's error code: run outcomes, conditions and connector failures. */
export function runCodeMessage(code: string | null | undefined, t: Translate, te: HasKey): string {
  if (!code) return ''
  const key = `errors.${code}`
  return te(key) ? t(key) : code
}
