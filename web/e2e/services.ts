import { execFileSync } from 'node:child_process'
import { createServer, type IncomingMessage, type Server } from 'node:http'
import { type AddressInfo, connect, createServer as createTCPServer } from 'node:net'

import { fakeOIDC } from './fake-oidc'

/**
 * Services the delivery journey sends to: Mailpit and an S3 gateway in Docker, and a fake server
 * that answers like Telegram, Slack, Discord, a webhook receiver and Uptime Kuma. Their addresses
 * reach the tests through environment variables set in global setup.
 */

export interface Received {
  service: string
  method: string
  path: string
  headers: Record<string, string>
  body: string
}

/** Env var names the tests read. */
export const ENV = {
  fake: 'E2E_FAKE_URL', smtpPort: 'E2E_SMTP_PORT', smtpProxyPort: 'E2E_SMTP_PROXY_PORT', mailpit: 'E2E_MAILPIT_URL', s3: 'E2E_S3_URL', s3Container: 'E2E_S3_CONTAINER',
} as const

const MAILPIT = 'axllent/mailpit:v1.21'
const S3_GATEWAY = 'versity/versitygw:v1.8.0'
export const S3_ACCESS = { key: 'rowbird', secret: 'rowbird-secret', bucket: 'reports' }

export function docker(args: string[]) {
  return execFileSync('docker', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
}

function hostPort(container: string, port: string) {
  const out = docker(['port', container, port]).split('\n')[0] ?? ''
  return Number(out.slice(out.lastIndexOf(':') + 1))
}

async function waitFor(url: string, ok: (status: number) => boolean) {
  for (let i = 0; i < 100; i++) {
    try {
      if (ok((await fetch(url)).status)) return
    } catch {
      // Not listening yet.
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  throw new Error(`service at ${url} did not start`)
}

function read(req: IncomingMessage) {
  return new Promise<Buffer>((resolve) => {
    const chunks: Buffer[] = []
    req.on('data', (c: Buffer) => chunks.push(c))
    req.on('end', () => resolve(Buffer.concat(chunks)))
  })
}

/** Answers like OpenAI's chat API: a schedule or a query proposal, depending on the instructions. */
export const AI_ANSWERS = {
  query: { sql: 'select customer, total from orders where total >= {{min_total}} order by total desc', explanation: 'The largest orders first.', suggested_name: 'Largest orders', schedule: { cron: '', timezone: '' } },
  schedule: { cron: '30 7 * * 1-5', timezone: '', explanation: 'Every weekday at 7:30 in the morning.' },
}

function fakeOpenAI(path: string, body: string) {
  if (path.endsWith('/models/fake-model')) return { id: 'fake-model', object: 'model' }
  const answer = body.includes('You turn a description') ? AI_ANSWERS.schedule : AI_ANSWERS.query
  return {
    model: 'fake-model',
    choices: [{ message: { role: 'assistant', content: JSON.stringify(answer) }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 100, completion_tokens: 20 },
  }
}

/**
 * Starts the fake chat and webhook server; GET /_inbox lists what it received, DELETE clears it.
 * It also runs a TCP proxy in front of Mailpit's SMTP port (ENV.smtpProxyPort) that
 * GET /_smtp?down=1 turns into a server refusing every connection, and ?down=0 back.
 */
export async function startFake(): Promise<Server> {
  let inbox: Received[] = []
  let failures = 0
  let smtpDown = false
  const proxy = createTCPServer((client) => {
    if (smtpDown) {
      client.destroy()
      return
    }
    const upstream = connect(Number(process.env[ENV.smtpPort]), '127.0.0.1')
    client.pipe(upstream).pipe(client)
    const close = () => {
      client.destroy()
      upstream.destroy()
    }
    client.on('error', close)
    upstream.on('error', close)
  })
  await new Promise<void>((resolve) => proxy.listen(0, '127.0.0.1', resolve))
  process.env[ENV.smtpProxyPort] = String((proxy.address() as AddressInfo).port)
  const server = createServer(async (req, res) => {
    const url = new URL(req.url ?? '/', 'http://fake')
    const body = await read(req)
    const reply = (status: number, value: unknown) => {
      res.writeHead(status, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify(value))
    }
    if (url.pathname.startsWith('/oidc/')) return fakeOIDC(req, res, url, body)
    if (url.pathname === '/_inbox') {
      if (req.method === 'DELETE') inbox = []
      return reply(200, inbox)
    }
    if (url.pathname === '/_smtp') {
      smtpDown = url.searchParams.get('down') === '1'
      return reply(200, { down: smtpDown })
    }
    if (url.pathname === '/_fail') {
      failures = Number(url.searchParams.get('times') ?? 0)
      return reply(200, { failures })
    }
    const service = url.pathname.split('/')[1] ?? ''
    const headers = Object.fromEntries(Object.entries(req.headers).map(([k, v]) => [k, String(v)]))
    inbox.push({ service, method: req.method ?? '', path: url.pathname + url.search, headers, body: body.toString('latin1') })
    if (service === 'webhook' && failures > 0) {
      failures--
      return reply(503, { error: 'try later' })
    }
    switch (service) {
      case 'telegram':
        return reply(200, { ok: true, result: { message_id: inbox.length } })
      case 'slack':
        if (url.pathname.endsWith('/files.getUploadURLExternal')) {
          return reply(200, { ok: true, upload_url: `http://${req.headers.host}/slack/upload/F${inbox.length}`, file_id: `F${inbox.length}` })
        }
        return reply(200, { ok: true, channel: 'C1', ts: '1.1' })
      case 'discord':
        return reply(200, { id: String(inbox.length) })
      case 'kuma':
        return reply(200, { ok: true })
      case 'openai':
        return reply(200, fakeOpenAI(url.pathname, body.toString('utf8')))
      default:
        return reply(200, { ok: true })
    }
  })
  server.on('close', () => proxy.close())
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)))
}

/** Starts Mailpit and the S3 gateway; returns a function that removes them. */
export async function startContainers(): Promise<() => void> {
  const suffix = `${process.pid}`
  const mail = `rowbird-e2e-mailpit-${suffix}`
  const s3 = `rowbird-e2e-s3-${suffix}`
  docker(['run', '-d', '--rm', '--name', mail, '-p', '127.0.0.1::1025', '-p', '127.0.0.1::8025', MAILPIT])
  docker([
    'run', '-d', '--rm', '--name', s3, '-p', '127.0.0.1::7070', '--entrypoint', 'sh', S3_GATEWAY, '-c',
    `mkdir -p /data/${S3_ACCESS.bucket} && exec versitygw --access ${S3_ACCESS.key} --secret ${S3_ACCESS.secret} --port :7070 posix /data`,
  ])
  const stop = () => {
    for (const name of [mail, s3]) {
      try {
        docker(['rm', '-f', name])
      } catch {
        // Already gone.
      }
    }
  }
  try {
    process.env[ENV.smtpPort] = String(hostPort(mail, '1025/tcp'))
    process.env[ENV.mailpit] = `http://127.0.0.1:${hostPort(mail, '8025/tcp')}`
    process.env[ENV.s3] = `http://127.0.0.1:${hostPort(s3, '7070/tcp')}`
    process.env[ENV.s3Container] = s3
    await waitFor(`${process.env[ENV.mailpit]}/api/v1/messages`, (s) => s === 200)
    await waitFor(process.env[ENV.s3]!, (s) => s > 0)
  } catch (e) {
    stop()
    throw e
  }
  return stop
}

export function fakeURL(server: Server) {
  return `http://127.0.0.1:${(server.address() as AddressInfo).port}`
}
