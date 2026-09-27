import { readFileSync } from 'node:fs'

import { type Browser, expect, type Page, test } from '@playwright/test'

import { secondURL } from './paths'

/**
 * The Phase 9 journey: the main instance exports a report with its query, connection and channels
 * through the UI; a second instance, set up empty, applies its GitOps directory right after setup
 * (read only in the UI) and imports the export through the wizard, filling in the secrets; both
 * instances then export the same YAML. It runs while the report "Daily orders" still exists.
 */

async function exportText(page: Page, base: string, body: unknown) {
  const token = (await page.context().cookies(base || undefined)).find((c) => c.name === 'rowbird_csrf')?.value ?? ''
  const res = await page.request.post(`${base}/api/v1/export`, { data: body, headers: { 'X-CSRF-Token': token } })
  expect(res.ok(), await res.text()).toBe(true)
  return res.text()
}

export function configTests(getPage: () => Page, getBrowser: () => Browser) {
  let second: Page
  let exported = ''

  test('a report exports as YAML with its query, connection and channels', async () => {
    const page = getPage()
    await page.goto('/reports')
    await page.getByTestId('report-pick-daily-orders').click()
    await page.getByTestId('reports-export').click()
    const dialog = page.getByTestId('export-dialog')
    await expect(dialog).toContainText('Export 1 report')
    const [file] = await Promise.all([page.waitForEvent('download'), dialog.getByTestId('export-download').click()])
    expect(file.suggestedFilename()).toBe('daily-orders.yaml')
    exported = readFileSync(await file.path(), 'utf8')
    for (const want of ['kind: Connection', 'kind: Channel', 'kind: Query', 'name: daily-orders', 'ref: recent-orders']) expect(exported).toContain(want)
    // Secrets never leave: they become environment placeholders.
    expect(exported).toContain('${env:ROWBIRD_CHANNEL_ERP_HOOK_HMAC_SECRET}')
    expect(exported).not.toContain('hook-secret')
  })

  test('a second instance applies its GitOps directory right after setup', async () => {
    second = await (await getBrowser().newContext({ baseURL: secondURL })).newPage()
    const res = await second.request.post('/api/v1/setup', {
      data: { email: 'gitops@example.com', name: 'Git Ops', password: 'a sturdy second passphrase', locale: 'en', timezone: 'UTC' },
    })
    expect(res.ok(), await res.text()).toBe(true)
    await expect.poll(async () => (await second.request.get('/api/v1/reports')).text(), { timeout: 15_000 }).toContain('gitops-orders')

    await second.goto('/reports')
    const row = second.getByTestId('report-row-gitops-orders')
    await expect(row).toContainText('Orders from Git')
    await expect(row).toContainText('GitOps')
    await row.getByRole('link', { name: 'Orders from Git' }).click()
    await expect(second.getByTestId('managed-notice')).toContainText('Managed by the configuration directory')
    await expect(second.getByTestId('report-edit')).toHaveCount(0)
  })

  test('the export imports into the second instance without losing anything but secrets', async () => {
    await second.goto('/settings/import-export')
    await second.getByTestId('import-yaml').fill(exported)
    await second.getByTestId('import-analyze').click()
    await expect(second.getByTestId('import-plan')).toContainText('daily-orders')
    await expect(second.getByTestId('import-apply')).toBeDisabled()
    // Every channel secret is asked for; the export had none of their values.
    const secrets = second.locator('[data-testid="import-secrets"] input[type="password"]')
    await expect(secrets.first()).toBeVisible()
    // Some secrets are URLs (Uptime Kuma, Discord), so the stand-in is one.
    for (const input of await secrets.all()) await input.fill('https://hooks.example.com/e2e-secret')
    await second.getByTestId('import-analyze').click()
    await expect(second.getByTestId('import-apply')).toBeEnabled()
    await second.getByTestId('import-apply').click()
    await expect(second.getByTestId('import-done')).toBeVisible()

    const selection = { reports: ['daily-orders'], include_connections: true, include_channels: true }
    expect(await exportText(second, secondURL, selection)).toBe(await exportText(getPage(), '', selection))
  })
}
