import { type Browser, expect, type Page, test } from '@playwright/test'

import { OIDC_CLIENT } from './fake-oidc'
import { ENV } from './services'

/**
 * Single sign-on: the admin sets up the fake provider in Settings > Security, and a fresh browser
 * signs in through it. The provider verified the admin's email, so the identity links to the
 * existing account, and the workspace's 2FA requirement is left to the provider.
 */
export function oidcTests(getPage: () => Page, getBrowser: () => Browser) {
  test('the admin sets up single sign-on and signs in through the provider', async () => {
    const page = getPage()
    const issuer = `${process.env[ENV.fake]}/oidc`
    await page.goto('/settings/security')
    const card = page.getByTestId('oidc-settings')
    await expect(card.getByTestId('oidc-redirect-uri')).toHaveValue(/\/api\/v1\/auth\/oidc\/callback$/)
    await card.getByTestId('oidc-issuer').fill(issuer)
    await card.getByTestId('oidc-test').click()
    await expect(card.getByTestId('oidc-test-result')).toHaveText('The provider answered.')
    await card.getByTestId('oidc-enabled').click()
    await card.getByTestId('oidc-client-id').fill(OIDC_CLIENT.id)
    await card.getByTestId('oidc-client-secret').fill(OIDC_CLIENT.secret)
    await card.getByTestId('oidc-label').fill('Sign in with Acme')
    await card.getByTestId('oidc-save').click()
    await expect(card.getByTestId('oidc-secret-configured')).toBeVisible()

    const context = await getBrowser().newContext()
    const guest = await context.newPage()
    await guest.goto('/login?redirect=/runs')
    await guest.getByTestId('sso-login').click()
    await expect(guest).toHaveURL(/\/runs$/)
    await guest.goto('/profile')
    await expect(guest.getByText('ana@example.com')).toBeVisible()
    await context.close()

    await page.goto('/settings/security')
    await expect(page.getByRole('cell', { name: 'Account linked to single sign-on' }).first()).toBeVisible()
  })
}
