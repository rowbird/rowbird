import { type Browser, expect, test } from '@playwright/test'

import { secondURL } from './paths'

/**
 * Passkeys on the second instance, whose public URL uses a domain (WebAuthn does not accept an IP
 * address as relying party). Chromium's virtual authenticator stands in for the device: the user
 * adds a passkey on the profile page, signs out, and signs back in with it and no password.
 */
export function passkeyTests(getBrowser: () => Browser) {
  test('a passkey added on the profile signs in without a password', async () => {
    const context = await getBrowser().newContext({ baseURL: secondURL })
    const page = await context.newPage()
    const cdp = await context.newCDPSession(page)
    await cdp.send('WebAuthn.enable')
    await cdp.send('WebAuthn.addVirtualAuthenticator', {
      options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
    })

    await page.goto('/login')
    await page.locator('#email').fill('gitops@example.com')
    await page.locator('#password').fill('a sturdy second passphrase')
    await page.getByTestId('login-submit').click()
    await expect(page).not.toHaveURL(/\/login/)

    await page.goto('/profile')
    await page.getByTestId('passkey-add').click()
    await page.getByTestId('passkey-name').fill('E2E laptop')
    await page.getByTestId('passkey-password').fill('a sturdy second passphrase')
    await page.getByTestId('passkey-create').click()
    await expect(page.getByTestId('passkey-item')).toContainText('E2E laptop')

    await page.getByTestId('user-menu').click()
    await page.getByTestId('menu-sign-out').click()
    await expect(page).toHaveURL(/\/login/)
    await page.getByTestId('passkey-login').click()
    await expect(page).not.toHaveURL(/\/login/)
    await page.goto('/profile')
    await expect(page.getByTestId('passkey-item')).toContainText('last used')
    await context.close()
  })
}
