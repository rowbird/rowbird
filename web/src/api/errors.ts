import type { Schemas } from './client'

/** A failed API call, built from the RFC 9457 problem the server returns. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly title: string
  /** Field path -> validation code, for example { password: 'validation.too_short' }. */
  readonly fields: Record<string, string>
  /** Seconds to wait, from Retry-After. */
  readonly retryAfter?: number
  readonly requestId?: string

  constructor(status: number, problem: Partial<Schemas['Problem']>, retryAfter?: number) {
    super(problem.code ?? 'internal')
    this.name = 'ApiError'
    this.status = status
    this.code = problem.code ?? 'internal'
    this.title = problem.title ?? ''
    this.requestId = problem.request_id
    this.retryAfter = retryAfter
    this.fields = {}
    for (const f of problem.errors ?? []) this.fields[f.field] = f.code
  }
}

interface FetchResult<T> {
  data?: T
  error?: unknown
  response: Response
}

/** Returns the data of a call or throws an ApiError, so callers can use try/catch. */
export function unwrap<T>(result: FetchResult<T>): T {
  if (result.error !== undefined || !result.response.ok) {
    const retry = Number(result.response.headers.get('Retry-After'))
    const problem = (typeof result.error === 'object' && result.error !== null ? result.error : {}) as Partial<Schemas['Problem']>
    throw new ApiError(result.response.status, problem, Number.isFinite(retry) && retry > 0 ? retry : undefined)
  }
  return result.data as T
}

type Translate = (key: string, named?: Record<string, unknown>) => string
type HasKey = (key: string) => boolean

/** The message to show for an error: the translated code, else the server title, else a generic one. */
export function errorMessage(err: unknown, t: Translate, te: HasKey): string {
  if (err instanceof ApiError) {
    const key = `errors.${err.code}`
    if (te(key)) return t(key, { seconds: err.retryAfter ?? 0 })
    if (err.title) return err.title
  }
  return t('errors.internal')
}

/** The message for one field, or undefined when the field is valid. */
export function fieldMessage(err: unknown, field: string, t: Translate, te: HasKey): string | undefined {
  if (!(err instanceof ApiError)) return undefined
  const code = err.fields[field]
  if (!code) return undefined
  const key = `errors.${code}`
  return te(key) ? t(key) : code
}
