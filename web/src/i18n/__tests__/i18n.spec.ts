import { describe, expect, it } from 'vitest'

import en from '@/locales/en.json'
import ptBR from '@/locales/pt-BR.json'

import { detectLocale } from '..'

function keys(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) =>
    typeof v === 'object' && v !== null ? keys(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  )
}

describe('detectLocale', () => {
  it.each([
    [['pt-BR', 'en'], 'pt-BR'],
    [['pt'], 'pt-BR'],
    [['pt-PT'], 'pt-BR'],
    [['en-GB'], 'en'],
    [['de', 'pt-BR'], 'pt-BR'],
    [['de'], 'en'],
    [[], 'en'],
  ])('%j -> %s', (preferred, expected) => {
    expect(detectLocale(preferred)).toBe(expected)
  })
})

describe('locale files', () => {
  it('have the same keys', () => {
    expect(keys(ptBR).sort()).toEqual(keys(en).sort())
  })

  it('have no empty messages', () => {
    for (const messages of [en, ptBR]) {
      for (const key of keys(messages)) {
        const value = key.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown>)[k], messages)
        expect(value, key).not.toBe('')
      }
    }
  })
})
