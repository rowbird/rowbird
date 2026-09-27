import createClient, { type Middleware } from 'openapi-fetch'

import type { components, paths } from './schema'

export type Schemas = components['schemas']

const CSRF_COOKIE = 'rowbird_csrf'
const CSRF_HEADER = 'X-CSRF-Token'

/** Reads a cookie set by the server; the CSRF cookie is deliberately not HttpOnly. */
export function readCookie(name: string, cookies: string = document.cookie): string | undefined {
  for (const part of cookies.split(';')) {
    const [k, ...v] = part.trim().split('=')
    if (k === name) return decodeURIComponent(v.join('='))
  }
  return undefined
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

let unauthorizedHandler: (() => void) | undefined

/** Registers what happens when the API answers 401 (the session store sends the user to login). */
export function onUnauthorized(handler: () => void) {
  unauthorizedHandler = handler
}

export const authMiddleware: Middleware = {
  onRequest({ request }) {
    if (!SAFE_METHODS.has(request.method)) {
      const token = readCookie(CSRF_COOKIE)
      if (token) request.headers.set(CSRF_HEADER, token)
    }
    return request
  },
  onResponse({ response, schemaPath }) {
    // Signing in answers 401 for bad credentials; that is not an expired session.
    if (response.status === 401 && !schemaPath.startsWith('/api/v1/auth/login')) unauthorizedHandler?.()
    return response
  },
}

/**
 * The only way the UI talks to the backend (ADR-0002). Types come from api/openapi.yaml through
 * `make generate`; paths in the contract are absolute, so the base URL is the page origin. fetch is
 * looked up on every call so tests can replace it.
 */
export const api = createClient<paths>({
  baseUrl: globalThis.location?.origin ?? '',
  fetch: (input) => globalThis.fetch(input),
})
api.use(authMiddleware)

export type ApiClient = typeof api
