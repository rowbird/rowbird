import AxeBuilder from '@axe-core/playwright'
import { expect, type Page, test } from '@playwright/test'

/**
 * Accessibility and small screens across the main pages, with the data the journey created:
 * axe finds no serious or critical violation, and at phone width no page scrolls sideways.
 */
async function pages(page: Page) {
  const first = async (path: string) => ((await (await page.request.get(path)).json()) as { items: { id: string }[] }).items[0]?.id
  const reportId = await first('/api/v1/reports')
  const queryId = await first('/api/v1/queries')
  const list = ['/', '/reports', '/queries', '/queries/new', '/connections', '/channels', '/runs', '/links', '/profile',
    '/settings/general', '/settings/users', '/settings/security', '/settings/storage', '/settings/about', '/reports/new']
  if (reportId) list.push(`/reports/${reportId}`)
  if (queryId) list.push(`/queries/${queryId}`)
  return list
}

export function qualityTests(getPage: () => Page) {
  test('the main pages have no serious accessibility problems', async () => {
    const page = getPage()
    await page.setViewportSize({ width: 1280, height: 800 })
    const problems: string[] = []
    for (const path of await pages(page)) {
      await page.goto(path)
      await page.waitForLoadState('networkidle')
      const res = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      for (const v of res.violations.filter((x) => x.impact === 'serious' || x.impact === 'critical')) {
        problems.push(`${path}: ${v.id} (${v.impact}) ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(' | ')}`)
      }
    }
    expect(problems, problems.join('\n')).toEqual([])
  })

  test('no page scrolls sideways on a phone', async () => {
    const page = getPage()
    await page.setViewportSize({ width: 375, height: 812 })
    const wide: string[] = []
    for (const path of await pages(page)) {
      await page.goto(path)
      await page.waitForLoadState('networkidle')
      const over = await page.evaluate<number>('document.documentElement.scrollWidth - window.innerWidth')
      if (over > 1) {
        // Name the widest elements that stick out, to find the culprit quickly.
        const culprits = await page.evaluate<string>(`[...document.querySelectorAll('body *')]
          .filter((e) => e.getBoundingClientRect().right > window.innerWidth + 1)
          .sort((a, b) => b.getBoundingClientRect().width - a.getBoundingClientRect().width).slice(0, 6)
          .map((e) => e.tagName.toLowerCase() + '.' + String(e.className).split(' ').slice(0, 3).join('.') + '=' + Math.round(e.getBoundingClientRect().width)).join(' | ')`)
        wide.push(`${path}: ${over}px (${culprits})`)
      }
    }
    await page.setViewportSize({ width: 1280, height: 800 })
    expect(wide, wide.join('\n')).toEqual([])
  })
}
