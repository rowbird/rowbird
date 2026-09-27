import { vi } from 'vitest'

type Handler = (req: Request) => Response | Promise<Response>

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json', ...headers },
  })
}

/**
 * Replaces fetch with a router keyed by "METHOD /path". Unknown routes fail the test loudly.
 * Returns the recorded requests.
 */
export function mockApi(routes: Record<string, Handler>) {
  const calls: Request[] = []
  vi.stubGlobal('fetch', async (input: Request) => {
    const req = input instanceof Request ? input : new Request(input)
    calls.push(req.clone())
    const key = `${req.method} ${new URL(req.url).pathname}`
    const handler = routes[key]
    if (!handler) throw new Error(`unexpected request ${key}`)
    return handler(req)
  })
  return calls
}

export const sampleMe = {
  id: '01900000-0000-7000-8000-000000000001',
  email: 'ana@example.com',
  name: 'Ana',
  locale: 'en',
  theme: 'system',
  role: 'admin',
  totp_enabled: false,
  has_password: true,
  restriction: 'none',
  created_at: '2026-09-01T12:00:00Z',
  version: 1,
} as const
