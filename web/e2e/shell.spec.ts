import { readFileSync } from 'node:fs'

import { type APIRequestContext, type Browser, expect, type Page, test } from '@playwright/test'

import { aiTests } from './ai'
import { alertTests } from './alerts'
import { configTests } from './config'
import { oidcTests } from './oidc'
import { passkeyTests } from './passkeys'
import { qualityTests } from './quality'
import { resetTests } from './reset'
import { deliveryTests } from './deliveries'
import { shopDB } from './paths'
import { totp } from './totp'

// One Rowbird instance with a fresh store runs the whole journey, in order.
test.describe.configure({ mode: 'serial' })

const admin = { name: 'Ana Admin', email: 'ana@example.com', password: 'a sturdy admin passphrase' }
const viewer = { name: 'Vic Viewer', email: 'vic@example.com', password: 'a sturdy viewer passphrase' }

let adminPage: Page
let totpSecret = ''
let recoveryCodes: string[] = []
let viewerTempPassword = ''
let viewerPage: Page

async function csrfPost(page: Page, path: string, data: unknown) {
  const csrf = (await page.context().cookies()).find((c) => c.name === 'rowbird_csrf')?.value ?? ''
  return page.request.post(path, { data, headers: { 'X-CSRF-Token': csrf } })
}

async function signOut(page: Page) {
  await page.getByTestId('user-menu').click()
  await page.getByTestId('menu-sign-out').click()
  await expect(page).toHaveURL(/\/login/)
}

async function signIn(page: Page, email: string, password: string) {
  await page.goto('/login')
  await page.locator('#email').fill(email)
  await page.locator('#password').fill(password)
  await page.getByTestId('login-submit').click()
}

async function newPage(browser: Browser) {
  return (await browser.newContext()).newPage()
}

let sharedBrowser: Browser

test.beforeAll(async ({ browser }) => {
  sharedBrowser = browser
  adminPage = await newPage(browser)
})

test('health endpoints and unknown API routes', async ({ request }: { request: APIRequestContext }) => {
  expect((await request.get('/health/live')).status()).toBe(200)
  const ready = await request.get('/health/ready')
  expect(ready.status()).toBe(200)
  const res = await request.get('/api/v1/does-not-exist')
  expect(res.status()).toBe(404)
  expect(res.headers()['content-type']).toBe('application/problem+json')
  expect((await request.get('/api/v1/me')).status()).toBe(401)
})

test('setup creates the first admin after the master key warning', async () => {
  const page = adminPage
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => {
    if (m.type() === 'error' && !m.text().includes('401')) errors.push(m.text())
  })

  const response = await page.goto('/')
  expect(response?.headers()['content-security-policy']).toContain("default-src 'self'")
  await expect(page).toHaveURL(/\/setup$/)
  await expect(page.getByTestId('master-key-warning')).toContainText('master.key')

  await page.locator('#name').fill(admin.name)
  await page.locator('#email').fill(admin.email)
  await page.locator('#password').fill(admin.password)
  await page.locator('#confirm').fill(admin.password)
  await expect(page.getByTestId('setup-submit')).toBeDisabled()
  await page.getByTestId('master-key-ack').click()
  await page.getByTestId('setup-submit').click()

  await expect(page).toHaveURL(/\/$/)
  // Seven sections in the main navigation, settings at the bottom of the sidebar.
  await expect(page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link')).toHaveCount(7)
  await expect(page.getByTestId('nav-settings')).toBeVisible()
  // A fresh workspace shows the first-use checklist, starting with the connection.
  await expect(page.getByTestId('onboarding-connection').getByRole('link')).toBeVisible()
  await page.getByTestId('nav-reports').click()
  await expect(page.getByTestId('page-title')).toHaveText('Reports')
  expect(errors).toEqual([])
})

test('the language is saved to the profile', async () => {
  const page = adminPage
  await page.getByTestId('user-menu').click()
  await page.getByRole('menuitemradio', { name: 'Português (Brasil)' }).click()
  await expect(page.getByTestId('nav-reports')).toHaveText('Relatórios')
  await page.reload()
  await expect(page.getByTestId('nav-reports')).toHaveText('Relatórios')
  await expect(page.locator('html')).toHaveAttribute('lang', 'pt-BR')
  await page.getByTestId('user-menu').click()
  await page.getByRole('menuitemradio', { name: 'English' }).click()
  await expect(page.getByTestId('nav-reports')).toHaveText('Reports')
})

test('the admin turns on two-factor authentication', async () => {
  const page = adminPage
  await page.goto('/profile')
  await page.getByTestId('totp-start').click()
  await page.getByText('Cannot scan? Enter this key manually').click()
  totpSecret = (await page.getByTestId('totp-secret').textContent())!.trim()
  await page.locator('#totp-code').fill(totp(totpSecret))
  await page.getByRole('button', { name: 'Turn on two-factor authentication' }).last().click()
  await expect(page.getByTestId('recovery-codes').locator('li')).toHaveCount(10)
  recoveryCodes = await page.getByTestId('recovery-codes').locator('li').allTextContents()
  await page.getByTestId('totp-finish').click()
  await expect(page.getByTestId('totp-status')).toHaveText('On')
})

test('signing in asks for the second factor', async () => {
  const page = adminPage
  await signOut(page)
  await signIn(page, admin.email, admin.password)
  // The code used to enroll is spent; the next time step is accepted and new.
  await page.locator('#code').fill(totp(totpSecret, Date.now() + 30_000))
  await page.getByTestId('code-submit').click()
  await expect(page).toHaveURL(/\/$/)

  await signOut(page)
  await signIn(page, admin.email, admin.password)
  await page.getByRole('button', { name: 'Use a recovery code instead' }).click()
  await page.locator('#code').fill(recoveryCodes[0]!)
  await page.getByTestId('code-submit').click()
  await expect(page).toHaveURL(/\/$/)
})

test('the admin adds a viewer with a temporary password', async () => {
  const page = adminPage
  await page.goto('/settings/users')
  await page.getByTestId('user-create').click()
  await page.locator('#new-user-name').fill(viewer.name)
  await page.locator('#new-user-email').fill(viewer.email)
  await page.getByTestId('user-create-submit').click()
  viewerTempPassword = (await page.getByTestId('copy-value').textContent())!.trim()
  expect(viewerTempPassword).toHaveLength(16)
  await page.keyboard.press('Escape')
  await expect(page.getByTestId(`user-row-${viewer.email}`)).toContainText('Viewer')
})

test('the viewer must change the temporary password and has limited access', async ({ browser }) => {
  viewerPage = await newPage(browser)
  const page = viewerPage
  await signIn(page, viewer.email, viewerTempPassword)
  await expect(page).toHaveURL(/\/account\/password$/)
  await page.goto('/reports')
  await expect(page).toHaveURL(/\/account\/password$/)

  await page.locator('#current-password').fill(viewerTempPassword)
  await page.locator('#new-password').fill(viewer.password)
  await page.locator('#confirm-password').fill(viewer.password)
  await page.getByTestId('change-password-submit').click()
  await expect(page).toHaveURL(/\/$/)

  await page.goto('/settings')
  await expect(page.getByTestId('settings-tab-settings-users')).toBeVisible()
  await expect(page.getByTestId('settings-tab-settings-api-keys')).toHaveCount(0)
  await expect(page.getByTestId('user-create')).toHaveCount(0)
  await page.goto('/settings/api-keys')
  await expect(page).toHaveURL(/\/$/)

  const res = await csrfPost(page, '/api/v1/users', { email: 'x@example.com', name: 'X', role: 'viewer' })
  expect(res.status()).toBe(403)
  expect((await res.json()).code).toBe('auth.forbidden')
})

test('an API key works within its scope until revoked', async ({ request }) => {
  const page = adminPage
  await page.goto('/settings/api-keys')
  await page.getByTestId('api-key-create').click()
  await page.locator('#key-name').fill('ci')
  await page.getByTestId('api-key-create-submit').click()
  const key = (await page.getByTestId('copy-value').textContent())!.trim()
  expect(key).toMatch(/^rbk_[0-9A-Za-z]{43}$/)
  await page.keyboard.press('Escape')

  const auth = { Authorization: `Bearer ${key}` }
  expect((await request.get('/api/v1/users', { headers: auth })).status()).toBe(200)
  const write = await request.post('/api/v1/users', { headers: auth, data: { email: 'y@example.com', name: 'Y', role: 'viewer' } })
  expect(write.status()).toBe(403)

  await page.getByTestId('api-key-revoke-ci').click()
  await page.getByTestId('confirm-action').click()
  await expect(page.getByTestId('api-key-row-ci')).toContainText('Inactive')
  expect((await request.get('/api/v1/users', { headers: auth })).status()).toBe(401)
})

test('the admin adds a SQLite connection and browses its schema', async () => {
  const page = adminPage
  await page.goto('/connections')
  await expect(page.getByText('No connections yet')).toBeVisible()
  await page.getByTestId('connection-new').click()
  await page.getByTestId('driver-sqlite').click()
  await page.locator('#connection-name').fill('shop')
  await page.locator('#cfg-path').fill('/etc/hosts')
  await page.getByTestId('connection-test').click()
  await expect(page.getByTestId('test-result')).toContainText('outside the directories allowed')

  await page.locator('#cfg-path').fill(shopDB)
  await page.getByTestId('connection-test').click()
  await expect(page.getByTestId('test-result')).toContainText('Connection works')
  await expect(page.getByTestId('test-result')).toContainText('2 tables')
  await page.getByTestId('connection-save').click()
  await expect(page).toHaveURL(/\/connections\/[0-9a-f-]{36}$/)
  await expect(page.getByTestId('connection-status')).toHaveText('OK')

  await page.getByTestId('schema-search').fill('amount')
  await expect(page.getByTestId('schema-tables').locator('li')).toHaveCount(1)
  await page.getByTestId('table-salaries').getByRole('button').first().click()
  await expect(page.getByTestId('table-salaries')).toContainText('decimal')
  await page.getByTestId('exclude-salaries').click()
  await page.reload()
  await expect(page.getByTestId('exclude-salaries')).toHaveAttribute('data-state', 'checked')

  await page.getByTestId('connection-edit').click()
  await expect(page.locator('#cfg-path')).toHaveValue(shopDB)
  await page.getByTestId('connection-save').click()
  await expect(page).toHaveURL(/\/connections\/[0-9a-f-]{36}$/)
})

test('viewers can browse connections but not change them', async () => {
  await viewerPage.goto('/connections')
  await expect(viewerPage.getByTestId('connection-row-shop')).toBeVisible()
  await expect(viewerPage.getByTestId('connection-new')).toHaveCount(0)
  await viewerPage.getByTestId('connection-row-shop').getByRole('link').click()
  await expect(viewerPage.getByTestId('schema-tables')).toBeVisible()
  await expect(viewerPage.getByTestId('connection-edit')).toHaveCount(0)
  await expect(viewerPage.getByTestId('exclude-salaries')).toHaveCount(0)
})

test('an editor writes, previews and versions a query', async () => {
  const page = adminPage
  const editor = page.locator('.cm-content')
  await page.goto('/queries')
  await expect(page.getByText('No queries yet')).toBeVisible()
  await page.getByTestId('query-new').click()
  await page.getByTestId('query-title').fill('Recent orders')
  await expect(page.locator('#query-slug')).toHaveValue('recent-orders')
  await expect(page.getByTestId('schema-insert-orders')).toBeVisible()

  await editor.click()
  await page.keyboard.type('select customer, total from orders where created_at < {{today}} and total >= {{min_total}} order by id')
  await expect(page.getByTestId('param-min_total')).toBeVisible()
  await page.locator('#param-min_total-type').selectOption('decimal')
  await page.locator('#param-min_total-default').fill('50')

  await page.getByTestId('query-run').click()
  await expect(page.getByTestId('results-summary')).toContainText('2 rows')
  await expect(page.getByTestId('results')).toContainText('120.5')
  await expect(page.getByTestId('resolved-param').filter({ hasText: 'min_total = 50' })).toBeVisible()
  await expect(page.getByTestId('resolved-param').filter({ hasText: 'today =' })).toBeVisible()

  await page.getByTestId('query-save').click()
  await page.locator('#version-note').fill('first')
  await page.getByTestId('query-save-confirm').click()
  await expect(page).toHaveURL(/\/queries\/[0-9a-f-]{36}$/)
  await expect(page.getByText('Version 1 by Ana Admin')).toBeVisible()

  await editor.click()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.type('select customer from orders where total >= {{min_total}}')
  await page.getByTestId('query-save').click()
  await page.locator('#version-note').fill('only customers')
  await page.getByTestId('query-save-confirm').click()
  await expect(page.getByText('Saved as version 2.')).toBeVisible()

  await page.getByTestId('versions-open').click()
  await expect(page.getByTestId('diff-view')).toBeVisible()
  await expect(page.getByTestId('diff-view')).toContainText('created_at')
  await page.getByTestId('version-restore').click()
  await page.getByTestId('confirm-action').click()
  await expect(page.getByText('Version 1 restored as version 3.')).toBeVisible()
  await expect(page.getByText('Version 3 by Ana Admin')).toBeVisible()
})

test('an editor schedules a report and runs it by hand', async () => {
  const page = adminPage
  await page.goto('/reports')
  await expect(page.getByText('No reports yet')).toBeVisible()
  await page.getByTestId('report-new').click()
  await page.getByTestId('report-title').fill('Daily orders')
  await expect(page.locator('#report-slug')).toHaveValue('daily-orders')
  await expect(page.getByTestId('report-overrides')).toContainText('Default: 50')
  await page.locator('#override-min_total').fill('100')

  await page.locator('#schedule-kind').selectOption('weekdays')
  await page.locator('#schedule-time').fill('07:00')
  await expect(page.locator('#schedule-cron')).toHaveValue('0 7 * * 1-5')
  await expect(page.getByTestId('schedule-description')).toContainText('Monday through Friday')
  await expect(page.getByTestId('schedule-next').locator('li')).toHaveCount(5)

  await page.locator('#condition-add-type').selectOption('row_count')
  await page.getByTestId('condition-add').click()
  await expect(page.getByTestId('condition-rule-0')).toContainText('Row count')
  await page.getByTestId('report-save').click()

  await expect(page).toHaveURL(/\/reports\/[0-9a-f-]{36}$/)
  await expect(page.getByTestId('report-schedule')).toContainText('Monday through Friday')
  await expect(page.getByTestId('report-condition')).toHaveText('All of: Row count')
  await expect(page.getByTestId('report-next-run')).not.toHaveText('Not scheduled')

  // A manual run without delivery executes the query and shows the result.
  await page.getByTestId('report-run').click()
  await expect(page).toHaveURL(/\/runs\/[0-9a-f-]{36}$/)
  await expect(page.getByTestId('run-status-success')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('run-rows')).toHaveText('1 row')
  await expect(page.getByTestId('run-param').filter({ hasText: 'min_total = 100' })).toBeVisible()
  await expect(page.getByTestId('run-condition')).toContainText('Holds')
  await expect(page.getByTestId('results')).toContainText('Ana')
  await expect(page.getByTestId('step-deliveries')).toContainText('only shows the result')

  // The result can be downloaded for a day.
  const [csv] = await Promise.all([page.waitForEvent('download'), page.getByTestId('download-csv').click()])
  expect(csv.suggestedFilename()).toMatch(/^daily-orders-\d{4}-\d{2}-\d{2}-\d{4}\.csv$/)
  expect(readFileSync(await csv.path(), 'utf8')).toBe('customer,total\r\nAna,120.5\r\n')
  const [xlsx] = await Promise.all([page.waitForEvent('download'), page.getByTestId('download-xlsx').click()])
  expect(xlsx.suggestedFilename()).toMatch(/\.xlsx$/)
  expect(readFileSync(await xlsx.path()).subarray(0, 2).toString()).toBe('PK')

  await page.getByRole('link', { name: 'Daily orders' }).click()
  await page.getByTestId('report-toggle').click()
  await expect(page.getByTestId('report-status-paused')).toBeVisible()
  await expect(page.getByTestId('report-next-run')).toHaveText('Not scheduled')
  await page.getByTestId('report-toggle').click()
  await expect(page.getByTestId('report-status-active')).toBeVisible()
})

aiTests(() => adminPage)
deliveryTests(() => adminPage)
alertTests(() => adminPage)
resetTests(() => adminPage, () => sharedBrowser)
qualityTests(() => adminPage)
configTests(() => adminPage, () => sharedBrowser)
passkeyTests(() => sharedBrowser)
oidcTests(() => adminPage, () => sharedBrowser)

test('runs are listed and a query used by a report cannot be deleted', async () => {
  const page = adminPage
  await page.goto('/runs')
  await expect(page.getByTestId('run-status-success').first()).toBeVisible()
  await page.locator('#runs-status').selectOption('failed')
  await expect(page.getByTestId('runs-filtered-empty')).toBeVisible()
  await expect(page).toHaveURL(/status=failed/)

  await page.goto('/queries')
  await page.getByTestId('query-row-recent-orders').getByRole('link').click()
  await expect(page.getByText('used by one report')).toBeVisible()
  await page.getByTestId('query-delete').click()
  await page.getByTestId('confirm-action').click()
  await expect(page.getByText('The query is used by reports and cannot be deleted.')).toBeVisible()

  await page.goto('/reports')
  await page.getByTestId('report-row-daily-orders').getByRole('link').first().click()
  await page.getByTestId('report-delete').click()
  await page.getByTestId('confirm-action').click()
  await expect(page).toHaveURL(/\/reports$/)
  await expect(page.getByText('No reports yet')).toBeVisible()
})

test('a connection used by a query cannot be deleted', async () => {
  const page = adminPage
  await page.goto('/connections')
  await page.getByTestId('connection-row-shop').getByRole('link').click()
  await page.getByTestId('connection-delete').click()
  await page.getByTestId('confirm-action').click()
  await expect(page.getByText('The connection is used by queries and cannot be deleted.')).toBeVisible()

  await page.goto('/queries')
  await page.getByTestId('query-row-recent-orders').getByRole('link').click()
  await page.getByTestId('query-delete').click()
  await page.getByTestId('confirm-action').click()
  await expect(page).toHaveURL(/\/queries$/)
})

test('deleting a connection asks for confirmation', async () => {
  const page = adminPage
  await page.goto('/connections')
  await page.getByTestId('connection-row-shop').getByRole('link').click()
  await page.getByTestId('connection-delete').click()
  await page.getByTestId('confirm-action').click()
  await expect(page).toHaveURL(/\/connections$/)
  await expect(page.getByText('No connections yet')).toBeVisible()
})

test('requiring 2FA sends users without it to enrollment', async () => {
  await adminPage.goto('/settings/security')
  await adminPage.getByTestId('require-2fa').click()
  await adminPage.getByTestId('confirm-action').click()
  await expect(adminPage.getByTestId('security-event').first()).toContainText('Settings changed')

  await viewerPage.goto('/reports')
  await expect(viewerPage).toHaveURL(/\/account\/two-factor$/)
  await expect(viewerPage.locator('img[alt*="QR"]')).toBeVisible()
})

test('repeated wrong passwords lock the account', async ({ browser }) => {
  const page = await newPage(browser)
  for (let i = 0; i < 5; i++) {
    await signIn(page, viewer.email, 'definitely not the password')
    await expect(page.getByTestId('login-error')).toHaveText('Incorrect email or password.')
  }
  await signIn(page, viewer.email, viewer.password)
  await expect(page.getByTestId('login-error')).toContainText('Too many failed attempts')
})
