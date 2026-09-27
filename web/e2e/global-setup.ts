import { mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'

import { gitopsDir, shopDB, sqliteDir } from './paths'
import { ENV, fakeURL, startContainers, startFake } from './services'

/**
 * Creates the SQLite database the journey reads, and starts the services deliveries go to. The
 * returned function stops them.
 */
export default async function globalSetup() {
  rmSync(sqliteDir, { recursive: true, force: true })
  mkdirSync(sqliteDir, { recursive: true })
  const db = new DatabaseSync(shopDB)
  db.exec(`
    CREATE TABLE orders (id INTEGER PRIMARY KEY, customer TEXT NOT NULL, total DECIMAL(10,2), created_at DATETIME);
    CREATE TABLE salaries (employee TEXT, amount DECIMAL(10,2));
    INSERT INTO orders (customer, total, created_at) VALUES ('Ana', 120.50, '2020-01-10 10:00:00'), ('Bruno', 80.00, '2020-01-11 11:30:00');
  `)
  db.close()

  // The second instance applies this directory once its setup is done.
  rmSync(gitopsDir, { recursive: true, force: true })
  mkdirSync(gitopsDir, { recursive: true })
  writeFileSync(join(gitopsDir, 'warehouse.yaml'), [
    'apiVersion: rowbird.dev/v1',
    'kind: Connection',
    'metadata: { name: warehouse }',
    'spec:',
    '  driver: sqlite',
    `  config: { path: ${JSON.stringify(shopDB)} }`,
    '---',
    'apiVersion: rowbird.dev/v1',
    'kind: Report',
    'metadata: { name: gitops-orders, title: Orders from Git }',
    'spec:',
    '  query:',
    '    connection: warehouse',
    '    sql: select customer, total from orders order by total desc',
    '  schedule: { cron: "0 6 * * *", timezone: UTC }',
    '',
  ].join('\n'))

  const fake = await startFake()
  process.env[ENV.fake] = fakeURL(fake)
  const stopContainers = await startContainers()
  return () => {
    fake.close()
    stopContainers()
  }
}
