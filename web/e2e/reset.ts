import { type Browser, expect, type Page, test } from '@playwright/test'

import { ENV } from './services'

/**
 * "Forgot password": a user asks for a link on the login page, it arrives by email through the
 * system mailer (Mailpit), and the new password signs them in. It runs after the delivery journey,
 * which marks an email channel for system mail.
 */
export function resetTests(getPage: () => Page, getBrowser: () => Browser) {
  test('a forgotten password is reset through an emailed link', async () => {
    const admin = getPage()
    const csrf = (await admin.context().cookies()).find((c) => c.name === 'rowbird_csrf')?.value ?? ''
    const created = await admin.request.post('/api/v1/users', {
      data: { email: 'rui@example.com', name: 'Rui Reset', role: 'editor' }, headers: { 'X-CSRF-Token': csrf },
    })
    expect(created.ok(), await created.text()).toBe(true)

    const context = await getBrowser().newContext()
    const page = await context.newPage()
    await page.goto('/login')
    await page.getByTestId('forgot-link').click()
    await page.getByTestId('forgot-email').fill('rui@example.com')
    await page.getByTestId('forgot-submit').click()
    await expect(page.getByTestId('forgot-sent')).toBeVisible()

    const mailpit = process.env[ENV.mailpit]!
    let path = ''
    await expect.poll(async () => {
      const list = (await (await page.request.get(`${mailpit}/api/v1/messages`)).json()).messages as { ID: string; To: { Address: string }[] }[]
      const msg = list.find((m) => m.To.some((to) => to.Address === 'rui@example.com'))
      if (!msg) return ''
      const body = await (await page.request.get(`${mailpit}/api/v1/message/${msg.ID}`)).json()
      path = /\/reset-password\?token=rbr_[A-Za-z0-9_-]+/.exec(body.HTML as string)?.[0] ?? ''
      return path
    }, { timeout: 15_000 }).not.toBe('')

    await page.goto(path)
    await page.getByTestId('reset-password').fill('a sturdy reset passphrase')
    await page.getByTestId('reset-confirm').fill('a sturdy reset passphrase')
    await page.getByTestId('reset-submit').click()
    await expect(page.getByTestId('reset-done')).toBeVisible()
    await page.getByTestId('reset-login').click()
    await page.locator('#email').fill('rui@example.com')
    await page.locator('#password').fill('a sturdy reset passphrase')
    await page.getByTestId('login-submit').click()
    // The reset replaced the temporary password, so no change is asked.
    await expect(page).toHaveURL(/\/$/)
    await context.close()
  })
}
