import { describe, expect, it } from 'vitest'

import { createAppI18n } from '@/i18n'
import { unflatten } from '@/lib/schema'

import { literalMessages } from '../plugins'

describe('literalMessages', () => {
  it('keeps Mustache braces, addresses and pipes as written', () => {
    const raw = {
      'plugin.s3.path.help': 'Default: reports/{{report.slug}}/{{run.date}}.{{format}}',
      'plugin.email.recipients.help': 'Separate addresses with commas: ana@example.com, bruno@example.com | $5',
    }
    const { global } = createAppI18n('en')
    global.mergeLocaleMessage('en', unflatten(literalMessages(raw)))
    for (const [key, text] of Object.entries(raw)) expect(global.t(key)).toBe(text)
  })
})
