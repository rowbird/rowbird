import { expect, type Page, test } from '@playwright/test'

import { ENV, type Received } from './services'

/**
 * The Phase 7 journey: with email as the primary alert channel and Telegram as the fallback,
 * stopping SMTP produces one grouped in-app notification and one alert through the fallback, and
 * bringing it back produces a recovery notification and a recovery email. It runs after the
 * delivery journey, which created the channels and the report "Daily orders".
 */

const env = (name: string) => {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set; global setup starts the delivery services`)
  return v
}

async function csrf(page: Page, method: 'post' | 'put', path: string, data: unknown) {
  const token = (await page.context().cookies()).find((c) => c.name === 'rowbird_csrf')?.value ?? ''
  const res = await page.request[method](path, { data, headers: { 'X-CSRF-Token': token } })
  expect(res.ok(), `${method} ${path}: ${await res.text()}`).toBe(true)
  return res.json()
}

async function telegramTexts(page: Page): Promise<string[]> {
  const got: Received[] = await (await page.request.get(`${env(ENV.fake)}/_inbox`)).json()
  return got.filter((r) => r.service === 'telegram' && r.path.endsWith('/sendMessage')).map((r) => JSON.parse(r.body).text as string)
}

async function mailSubjects(page: Page, to: string): Promise<string[]> {
  const { messages } = await (await page.request.get(`${env(ENV.mailpit)}/api/v1/messages`)).json()
  return (messages as { Subject: string; To: { Address: string }[] }[]).filter((m) => m.To.some((x) => x.Address === to)).map((m) => m.Subject)
}

/** Runs the report with delivery and waits until the run finished; returns its status. */
async function runAndWait(page: Page, reportId: string): Promise<string> {
  const run = await csrf(page, 'post', `/api/v1/reports/${reportId}/run`, { deliver: true })
  let status = run.status as string
  await expect
    .poll(async () => {
      status = (await (await page.request.get(`/api/v1/runs/${run.id}`)).json()).status
      return ['pending', 'running'].includes(status)
    }, { timeout: 60_000 })
    .toBe(false)
  return status
}

export function alertTests(getPage: () => Page) {
  test('stopping SMTP alerts once through the fallback and SMTP coming back sends a recovery', async () => {
    test.setTimeout(120_000)
    const page = getPage()
    const setSMTP = (down: boolean) => page.request.get(`${env(ENV.fake)}/_smtp?down=${down ? 1 : 0}`)

    // An email channel behind the switchable SMTP proxy, used by a report and as primary alert.
    const mail = await csrf(page, 'post', '/api/v1/channels', {
      name: 'alert-mail', type: 'email',
      config: { host: '127.0.0.1', port: Number(env(ENV.smtpProxyPort)), tls_mode: 'none', from_address: 'alerts@example.com' },
    })
    const queries = (await (await page.request.get('/api/v1/queries')).json()).items as { id: string; title: string }[]
    const report = await csrf(page, 'post', '/api/v1/reports', { title: 'Stock watch', query_id: queries[0]!.id, cron: '@daily', timezone: 'UTC' })
    await csrf(page, 'post', `/api/v1/reports/${report.id}/deliveries`, { channel_id: mail.id, mode: 'inline', options: { to: 'stock@example.com' } })

    // The admin picks the alert channels on the settings page.
    await page.goto('/settings/alerts')
    await page.getByTestId('alert-primary-channel').selectOption({ label: 'alert-mail (Email)' })
    await page.locator('#alert-primary-to').fill('ops@example.com')
    await page.getByTestId('alert-fallback-channel').selectOption({ label: 'ops-telegram (Telegram)' })
    await page.locator('#alert-fallback-chat_id').fill('-100999')
    await page.getByTestId('alert-settings-save').click()
    await expect(page.getByText('Settings saved')).toBeVisible()
    await page.goto('/channels/' + mail.id)
    await expect(page.getByTestId('channel-used-by-alert')).toHaveText('System alerts (primary)')

    await page.request.delete(`${env(ENV.fake)}/_inbox`)
    await setSMTP(true)
    try {
      expect(await runAndWait(page, report.id)).toBe('partial')
      expect(await runAndWait(page, report.id)).toBe('partial')
    } finally {
      await setSMTP(false)
    }

    // One alert, through the fallback: the failing channel is the primary itself.
    await expect.poll(() => telegramTexts(page)).toHaveLength(1)
    expect((await telegramTexts(page))[0]).toContain('Channel alert-mail is failing')
    expect(await mailSubjects(page, 'ops@example.com')).toEqual([])

    // In the app: a banner and one notification counting both failures.
    await page.goto('/')
    await expect(page.getByTestId('health-banner-channels')).toHaveText('Channel alert-mail is failing')
    await expect(page.getByTestId('notification-count')).toBeVisible()
    await page.getByTestId('notification-bell').click()
    const failing = page.getByTestId('notification').filter({ hasText: 'Channel alert-mail is failing' })
    await expect(failing).toHaveCount(1)
    await expect(failing.getByTestId('notification-occurrences')).toHaveText('2 times')
    await page.keyboard.press('Escape')

    // SMTP is back: the next run succeeds, the notification recovers and the primary gets email.
    expect(await runAndWait(page, report.id)).toBe('success')
    await expect.poll(() => mailSubjects(page, 'ops@example.com')).toEqual(['Channel alert-mail recovered'])
    expect(await mailSubjects(page, 'stock@example.com')).toHaveLength(1)
    expect(await telegramTexts(page)).toHaveLength(1)
    await expect(page.getByTestId('health-banner')).toHaveCount(0)
    await page.getByTestId('notification-bell').click()
    await expect(page.getByTestId('notification').filter({ hasText: 'Channel alert-mail recovered' })).toHaveCount(1)
    await page.getByTestId('notifications-read-all').click()
    await expect(page.getByTestId('notification-count')).toHaveCount(0)
    await page.keyboard.press('Escape')

    // Later tests expect the query to be used by one report only.
    const token = (await page.context().cookies()).find((c) => c.name === 'rowbird_csrf')?.value ?? ''
    expect((await page.request.delete(`/api/v1/reports/${report.id}`, { headers: { 'X-CSRF-Token': token } })).ok()).toBe(true)
  })

  test('metrics and readiness report the instance', async () => {
    const page = getPage()
    const ready = await (await page.request.get('/health/ready')).json()
    expect(ready.components.scheduler.status).toBe('ok')
    const metrics = await (await page.request.get('/metrics')).text()
    expect(metrics).toMatch(/rowbird_runs_total\{status="partial",trigger="manual"\} [1-9]/)
    expect(metrics).toMatch(/rowbird_deliveries_total\{destination="email",status="failed"\} [1-9]/)
  })
}
