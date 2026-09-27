import { expect, type Page, test } from '@playwright/test'

import { AI_ANSWERS, ENV, type Received } from './services'

/**
 * The Phase 8 journey: the admin sets up an OpenAI-compatible provider (the fake server), then the
 * query editor proposes SQL through Ctrl+I, shown as a diff and applied only on request, and the
 * report editor turns a description into a schedule. It runs after the report "Daily orders"
 * exists; the connection "shop" excludes the table salaries from AI.
 */

const env = (name: string) => {
  const v = process.env[name]
  if (!v) throw new Error(`${name} is not set; global setup starts the fake services`)
  return v
}

async function aiRequests(page: Page): Promise<Received[]> {
  const got: Received[] = await (await page.request.get(`${env(ENV.fake)}/_inbox`)).json()
  return got.filter((r) => r.service === 'openai' && r.path.endsWith('/chat/completions'))
}

export function aiTests(getPage: () => Page) {
  test('the admin sets up the AI assistant', async () => {
    const page = getPage()
    await page.goto('/queries/new')
    await page.getByTestId('ai-open').click()
    await expect(page.getByTestId('ai-off')).toContainText('not set up')
    await page.getByRole('link', { name: 'Set it up in Settings > AI' }).click()
    await expect(page).toHaveURL(/\/settings\/ai$/)

    await page.getByTestId('ai-provider').selectOption('openai_compatible')
    await page.locator('#ai-base_url').fill(`${env(ENV.fake)}/openai/v1`)
    await page.locator('#ai-api_key').fill('sk-e2e-secret')
    await page.locator('#ai-model').fill('fake-model')
    await page.getByTestId('ai-test').click()
    await expect(page.getByTestId('ai-test-result')).toContainText('accepted the key and the model')
    await page.getByTestId('ai-save').click()
    await expect(page.getByText('AI settings saved')).toBeVisible()
    await expect(page.getByTestId('secret-api_key')).toBeVisible()
    expect(await (await page.request.get('/api/v1/settings/ai')).text()).not.toContain('sk-e2e-secret')
  })

  test('the query editor proposes SQL as a diff and applies it only on request', async () => {
    const page = getPage()
    await page.request.delete(`${env(ENV.fake)}/_inbox`)
    await page.goto('/queries/new')
    await expect(page.getByTestId('ai-open')).toBeVisible()
    await page.locator('.cm-content').click()
    await page.keyboard.type('select 1')
    await page.keyboard.press('ControlOrMeta+i')
    const panel = page.getByTestId('ai-panel')
    await panel.getByTestId('ai-prompt').fill('the largest orders above a minimum total')
    await panel.getByTestId('ai-generate').click()
    await expect(panel.getByTestId('ai-explanation')).toHaveText(AI_ANSWERS.query.explanation)
    await expect(panel.getByTestId('ai-diff')).toContainText('select customer, total from orders')
    await expect(page.locator('.cm-content').first()).toHaveText('select 1')

    // The provider got the request and the schema, but not the excluded table or any row.
    const [req] = await aiRequests(page)
    expect(req!.body).toContain('the largest orders above a minimum total')
    expect(req!.body).toContain('orders: id INTEGER')
    expect(req!.body).not.toContain('salaries')
    expect(req!.body).not.toContain('Bruno')
    expect(req!.headers.authorization).toBe('Bearer sk-e2e-secret')

    await panel.getByTestId('ai-apply').click()
    await expect(page.getByTestId('ai-panel')).toHaveCount(0)
    await expect(page.locator('.cm-content').first()).toContainText('select customer, total from orders where total >= {{min_total}}')
    await expect(page.getByTestId('query-title')).toHaveValue(AI_ANSWERS.query.suggested_name)
    await expect(page.locator('#param-min_total-type')).toBeVisible()
  })

  test('the report editor turns a description into a schedule', async () => {
    const page = getPage()
    await page.goto('/reports/new')
    await page.getByTestId('schedule-ai-text').fill('every weekday at half past seven')
    await page.getByTestId('schedule-ai-suggest').click()
    await expect(page.getByTestId('schedule-ai-description')).toContainText('Monday through Friday')
    await expect(page.locator('#schedule-cron')).not.toHaveValue('30 7 * * 1-5')
    await page.getByTestId('schedule-ai-apply').click()
    await expect(page.locator('#schedule-cron')).toHaveValue('30 7 * * 1-5')
    await expect(page.getByTestId('schedule-description')).toContainText('Monday through Friday')
  })
}
