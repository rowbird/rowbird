import { createHmac } from 'node:crypto'

import { expect, type Page, test } from '@playwright/test'

import { docker, ENV, type Received, S3_ACCESS } from './services'

/**
 * The delivery journey: channels for every destination, a report delivering in every mode, shared
 * links, a partial run with a resend, and a test sent to the caller. It runs inside the main
 * journey, after the report "Daily orders" exists, with the admin signed in.
 */

const env = (name: string) => {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set; global setup starts the delivery services`)
  return v
}

async function csrf(page: Page, method: 'post' | 'put' | 'patch', path: string, data: unknown) {
  const token = (await page.context().cookies()).find((c) => c.name === 'rowbird_csrf')?.value ?? ''
  const res = await page.request[method](path, { data, headers: { 'X-CSRF-Token': token } })
  expect(res.ok(), `${method} ${path}: ${await res.text()}`).toBe(true)
  return res.json()
}

async function inbox(page: Page): Promise<Received[]> {
  return (await page.request.get(`${env(ENV.fake)}/_inbox`)).json()
}

interface MailpitMessage {
  ID: string
  Subject: string
  To: { Address: string }[]
  Attachments: number
}

async function mails(page: Page): Promise<MailpitMessage[]> {
  return (await (await page.request.get(`${env(ENV.mailpit)}/api/v1/messages`)).json()).messages
}

async function mailBody(page: Page, id: string): Promise<{ HTML: string; Attachments: { FileName: string }[] }> {
  return (await page.request.get(`${env(ENV.mailpit)}/api/v1/message/${id}`)).json()
}

const channels: Record<string, string> = {}

export function deliveryTests(getPage: () => Page) {
  test('the admin adds a channel for every destination', async () => {
    const page = getPage()
    const fake = env(ENV.fake)

    // Email through the form, tested against Mailpit and made the system mailer.
    await page.goto('/channels')
    await expect(page.getByText('No channels yet')).toBeVisible()
    await page.getByTestId('channel-new').click()
    await page.getByTestId('type-email').click()
    await page.locator('#channel-name').fill('team-mail')
    await page.locator('#cfg-host').fill('127.0.0.1')
    await page.locator('#cfg-port').fill(env(ENV.smtpPort))
    await page.locator('#cfg-tls_mode').selectOption('none')
    await page.locator('#cfg-from_address').fill('reports@example.com')
    await page.getByTestId('channel-test').click()
    await expect(page.getByTestId('channel-test-result')).toContainText('Test sent')
    await page.getByTestId('channel-system-mailer').click()
    await page.getByTestId('channel-save').click()
    await expect(page).toHaveURL(/\/channels\/[0-9a-f-]{36}$/)
    channels.email = page.url().split('/').pop()!
    await expect.poll(async () => (await mails(page)).length).toBe(1)

    const create = async (name: string, type: string, config: Record<string, unknown>) => {
      channels[type] = (await csrf(page, 'post', '/api/v1/channels', { name, type, config })).id
    }
    await create('ops-telegram', 'telegram', { bot_token: '123:secret-bot-token', api_base: `${fake}/telegram` })
    await create('ops-slack', 'slack', { auth: 'bot', bot_token: 'xoxb-secret', api_base: `${fake}/slack/api` })
    await create('ops-discord', 'discord', { webhook_url: `${fake}/discord/api/webhooks/1/secret` })
    await create('erp-hook', 'webhook', { url: `${fake}/webhook/orders`, hmac_secret: 'hook-secret' })
    await create('monitor', 'uptime_kuma', { push_url: `${fake}/kuma/api/push/secret` })
    await create('archive', 's3', {
      endpoint: env(ENV.s3), bucket: S3_ACCESS.bucket, access_key: S3_ACCESS.key, secret_key: S3_ACCESS.secret, path_style: true,
    })

    await page.goto('/channels')
    await expect(page.locator('[data-testid^="channel-row-"]')).toHaveCount(7)
    await expect(page.getByTestId('channel-row-team-mail')).toContainText('System mail')
    // Secrets are never sent back.
    const listed = JSON.stringify(await (await page.request.get('/api/v1/channels')).json())
    for (const secret of ['secret-bot-token', 'xoxb-secret', 'hook-secret', S3_ACCESS.secret, '/1/secret']) expect(listed).not.toContain(secret)
  })

  test('a report delivers to every destination in every mode', async () => {
    const page = getPage()
    await page.goto('/reports')
    await page.getByRole('link', { name: 'Daily orders' }).click()
    await expect(page).toHaveURL(/\/reports\/[0-9a-f-]{36}$/)
    const reportId = page.url().split('/').pop()!

    // One delivery through the editor, with its preview.
    await expect(page.getByText('This report delivers nowhere yet')).toBeVisible()
    await page.getByTestId('delivery-add').click()
    const editor = page.getByTestId('delivery-editor')
    await editor.locator('#delivery-channel').selectOption({ label: 'team-mail (Email)' })
    // The first channel (S3) only takes files; files without a format are pointed out as you go.
    await expect(editor.locator('#delivery-mode')).toHaveValue('attachment')
    await expect(editor.getByTestId('delivery-formats-error')).toBeVisible()
    await editor.locator('#delivery-mode').selectOption('inline')
    await editor.locator('#delivery-option-to').fill('sales@example.com')
    await editor.locator('#delivery-option-subject').fill('Orders: {{run.rows}} rows')
    await expect(editor.getByTestId('preview-subject')).toHaveText('Orders: 1 rows')
    await expect(editor.getByTestId('preview-html')).toHaveAttribute('sandbox', '')
    await expect(editor.frameLocator('[data-testid="preview-html"]').locator('body')).toContainText('Ana')
    await editor.getByTestId('delivery-save').click()
    await expect(page.getByTestId('delivery-team-mail')).toContainText('Rows in the message')

    const add = (channel: string, mode: string, formats: string[] = [], options: Record<string, unknown> = {}) =>
      csrf(page, 'post', `/api/v1/reports/${reportId}/deliveries`, { channel_id: channel, mode, formats, options })
    await add(channels.email!, 'attachment', ['csv', 'xlsx'], { to: 'finance@example.com' })
    await add(channels.email!, 'link', ['pdf'], { to: 'board@example.com' })
    await add(channels.telegram!, 'attachment', ['csv'], { chat_id: '-100123' })
    await add(channels.slack!, 'attachment', ['xlsx'], { channel: '#sales' })
    await add(channels.discord!, 'link', ['csv'])
    await add(channels.webhook!, 'inline', [], { include_rows: 10 })
    await add(channels.uptime_kuma!, 'inline')
    await add(channels.s3!, 'attachment', ['json'])
    await page.reload()
    await expect(page.getByTestId('delivery-summary')).toHaveCount(9)

    await page.request.delete(`${env(ENV.fake)}/_inbox`)
    const before = (await mails(page)).length
    await page.getByTestId('report-run-deliver').click()
    await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/)
    await expect(page.getByTestId('run-status-success')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('run-attempts').locator('li')).toHaveCount(9)
    await expect(page.getByTestId('run-attempts')).not.toContainText('Failed')
    await expect(page.getByTestId('run-files')).toContainText('.xlsx')

    // Email: rows in the message, attached files, and a link.
    await expect.poll(async () => (await mails(page)).length).toBe(before + 3)
    const byRecipient = Object.fromEntries((await mails(page)).map((m) => [m.To[0]!.Address, m]))
    expect(byRecipient['sales@example.com']!.Subject).toBe('Orders: 1 rows')
    expect((await mailBody(page, byRecipient['sales@example.com']!.ID)).HTML).toContain('Ana')
    expect((await mailBody(page, byRecipient['finance@example.com']!.ID)).Attachments.map((a) => a.FileName).sort()).toEqual([
      expect.stringMatching(/^daily-orders-.*\.csv$/), expect.stringMatching(/^daily-orders-.*\.xlsx$/),
    ])
    expect((await mailBody(page, byRecipient['board@example.com']!.ID)).HTML).toMatch(/\/r\/rbl_[A-Za-z0-9_-]+/)

    const got = await inbox(page)
    const of = (service: string) => got.filter((r) => r.service === service)
    expect(of('telegram').map((r) => r.path.split('/').pop())).toEqual(expect.arrayContaining(['sendMessage', 'sendDocument']))
    expect(of('slack').map((r) => r.path.split('/').pop())).toEqual(expect.arrayContaining(['chat.postMessage', 'files.getUploadURLExternal', 'files.completeUploadExternal']))
    expect(of('discord')[0]!.body).toMatch(/\/r\/rbl_/)
    const kuma = new URL(of('kuma')[0]!.path, 'http://x')
    expect(kuma.searchParams.get('status')).toBe('up')

    // The webhook is signed over "<timestamp>.<body>".
    const hook = of('webhook')[0]!
    const [t, v1] = hook.headers['x-rowbird-signature']!.split(',').map((p) => p.split('=')[1])
    expect(createHmac('sha256', 'hook-secret').update(`${t}.${hook.body}`).digest('hex')).toBe(v1)
    expect(JSON.parse(hook.body)).toMatchObject({ event: 'run.completed', rows: [expect.anything()] })

    // The S3 destination wrote the file where its path template says.
    const files = docker(['exec', env(ENV.s3Container), 'find', `/data/${S3_ACCESS.bucket}`, '-type', 'f'])
    expect(files).toMatch(/reports\/daily-orders\/\d{4}-\d{2}-\d{2}\/daily-orders\.json/)
  })

  test('a shared link downloads until it is revoked', async () => {
    const page = getPage()
    const discord = (await inbox(page)).find((r) => r.service === 'discord')!
    const url = new URL(discord.body.match(/https?:\/\/[^\s"<>)\\]+\/r\/rbl_[A-Za-z0-9_-]+/)![0])
    const link = url.pathname

    const ok = await page.request.get(link, { maxRedirects: 0 })
    expect(ok.status()).toBe(200)
    expect(ok.headers()['x-robots-tag']).toContain('noindex')
    expect(await ok.text()).toContain('Ana')

    await page.goto('/links')
    const row = page.locator('[data-testid^="link-row-"]').filter({ hasText: '.csv' }).first()
    await expect(row).toContainText('Daily orders')
    // The detail lists the download above, made with the admin's session.
    await row.locator('[data-testid^="link-open-"]').click()
    await expect(page.getByTestId('link-download').first()).toContainText('Ana Admin')
    await page.keyboard.press('Escape')
    await row.getByTestId('link-revoke').click()
    await page.getByTestId('confirm-action').click()
    await expect(page.getByText('Link revoked')).toBeVisible()
    expect((await page.request.get(link, { maxRedirects: 0 })).status()).toBe(410)
  })

  test('a failed delivery makes the run partial and a resend completes it', async () => {
    const page = getPage()
    // The webhook answers 503 to every in-run attempt.
    await page.request.get(`${env(ENV.fake)}/_fail?times=3`)
    await page.goto('/reports')
    await page.getByRole('link', { name: 'Daily orders' }).click()
    await page.getByTestId('report-run-deliver').click()
    await expect(page.getByTestId('run-status-partial')).toBeVisible({ timeout: 30_000 })
    const hook = page.getByTestId('attempt-erp-hook')
    await expect(hook).toContainText('3 tries')
    await expect(hook.getByTestId('attempt-error')).toContainText('HTTP 503')

    await hook.getByTestId('attempt-resend').click()
    await expect(page.getByTestId('run-status-success')).toBeVisible()
    await expect(hook).toContainText('Sent')
    await expect(hook).toContainText('4 tries')
  })

  test('"send test to me" emails the caller through the system mailer', async () => {
    const page = getPage()
    const before = (await mails(page)).length
    await page.goto('/reports')
    await page.getByRole('link', { name: 'Daily orders' }).click()
    await page.getByTestId('delivery-test-me').click()
    await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/)
    await expect(page.getByTestId('run-status-success')).toBeVisible({ timeout: 30_000 })
    await expect.poll(async () => (await mails(page)).length).toBe(before + 1)
    const latest = (await mails(page))[0]!
    expect(latest.To[0]!.Address).toBe('ana@example.com')
    expect(latest.Attachments).toBeGreaterThan(0)
  })
}
