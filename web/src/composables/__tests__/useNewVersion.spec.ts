import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'

import { noteAbout, useNewVersion } from '../useNewVersion'

const about = (over: Partial<Schemas['About']>) =>
  ({ version: '1.0.0', update_check_allowed: true, update_check_enabled: true, update_available: false, ...over }) as Schemas['About']

describe('useNewVersion', () => {
  it('follows what the latest About answer says', () => {
    const newVersion = useNewVersion()
    noteAbout(about({ update_available: true, latest_version: '1.0.1' }))
    expect(newVersion.value).toBe('1.0.1')
    // After an upgrade (or with the check turned off) the notice goes away.
    noteAbout(about({ update_available: false, latest_version: '1.0.1' }))
    expect(newVersion.value).toBe('')
  })
})
