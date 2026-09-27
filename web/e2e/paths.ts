import { tmpdir } from 'node:os'
import { join } from 'node:path'

/** Directory the server allows for SQLite connections during E2E, and the fixture inside it. */
export const sqliteDir = join(tmpdir(), 'rowbird-e2e-sqlite')
export const shopDB = join(sqliteDir, 'shop.db')

/**
 * The second instance's configuration directory (GitOps), and its address. It is reached through
 * "localhost" because WebAuthn needs a domain as relying party, and the passkey tests use it.
 */
export const gitopsDir = join(tmpdir(), 'rowbird-e2e-gitops')
export const secondPort = Number(process.env.ROWBIRD_E2E_PORT ?? 18080) + 1
export const secondURL = `http://localhost:${secondPort}`
