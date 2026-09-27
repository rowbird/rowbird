import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import en from '@/locales/en.json'

import serverEn from '../../../../internal/i18n/locales/en.json'

function flatten(obj: object, prefix = ''): string[] {
  return Object.entries(obj).flatMap(([k, v]) => (typeof v === 'object' && v !== null ? flatten(v, `${prefix}${k}.`) : [`${prefix}${k}`]))
}

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) return name === '__tests__' ? [] : sourceFiles(p)
    return /\.(vue|ts)$/.test(name) && !name.endsWith('.d.ts') ? [p] : []
  })
}

const keys = new Set(flatten(en))
const src = join(__dirname, '..', '..')

describe('translation keys', () => {
  it('every literal key used in the source exists', () => {
    const missing: string[] = []
    const pattern = /(?:\$t|\bt|titleKey:|labelKey:)\(?\s*['"]([a-zA-Z][\w]*(?:\.[\w]+)+)['"]/g
    for (const file of sourceFiles(src)) {
      for (const m of readFileSync(file, 'utf8').matchAll(pattern)) {
        if (!keys.has(m[1]!)) missing.push(`${m[1]} (${file.slice(src.length + 1)})`)
      }
    }
    expect(missing).toEqual([])
  })

  it('dynamic key families are complete', () => {
    const families = {
      roles: ['viewer', 'editor', 'admin', 'viewerHelp', 'editorHelp', 'adminHelp'],
      scopes: ['read', 'run', 'write', 'admin', 'readHelp', 'runHelp', 'writeHelp', 'adminHelp'],
      theme: ['system', 'light', 'dark'],
    }
    for (const [family, members] of Object.entries(families)) {
      for (const m of members) expect(keys.has(`${family}.${m}`), `${family}.${m}`).toBe(true)
    }
  })

  it('every server error code has a message in the UI', () => {
    const missing = flatten(serverEn.errors).filter((k) => !keys.has(`errors.${k}`))
    expect(missing).toEqual([])
  })
})
