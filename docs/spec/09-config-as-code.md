# 09: Config as code (YAML)

One format for four uses: UI export/import, `rowbird apply`, a GitOps directory applied by the
server, and community templates (ADR-0026). The code lives in `internal/gitops`.

## Documents

Multiple documents per file (`---`); empty documents are skipped. Kinds: `Connection`, `Channel`,
`Query`, `Report`. Every document has `apiVersion`, `kind`, `metadata` and `spec`; unknown fields
are errors.

```yaml
apiVersion: rowbird.dev/v1
kind: Connection
metadata:
  name: production-db
spec:
  driver: postgres
  config: { host: db.internal, port: 5432, database: shop, sslmode: require }
  secrets:
    user: ${env:DB_USER}
    password: ${env:DB_PASSWORD}
  options: { queryTimeoutSeconds: 60, maxRows: 100000, allowMultiStatement: false, aiExcludedTables: [salaries] }
---
apiVersion: rowbird.dev/v1
kind: Channel
metadata:
  name: sales-slack
spec:
  type: slack
  secrets: { bot_token: ${env:SLACK_BOT_TOKEN} }
  systemMailer: false        # email channels only
---
apiVersion: rowbird.dev/v1
kind: Query
metadata:
  name: sales-by-region      # the slug
  title: Sales by region
  description: Yesterday's totals
spec:
  connection: production-db
  sql: |
    SELECT region, SUM(total) AS total
    FROM orders
    WHERE created_at >= {{yesterday}} AND created_at < {{today}}
    GROUP BY region
  params:
    - { name: region, type: text, default: south }
---
apiVersion: rowbird.dev/v1
kind: Report
metadata:
  name: daily-sales
  title: Daily sales
spec:
  query: { ref: sales-by-region }
  schedule: { cron: "0 7 * * 1-5", timezone: America/Sao_Paulo }
  condition: { match: all, rules: [ { type: row_count, op: gt, value: 0 } ] }
  params: { region: north }  # overrides of the query's parameters
  enabled: true
  run: { maxRows: 5000, retryMax: 2, retryBackoffSeconds: 30, misfire: run_once, overlap: skip, autoPauseAfter: 5, notifyOwner: true }
  deliveries:
    - channel: sales-slack
      mode: link             # inline | attachment | link
      formats: [xlsx]
      enabled: true
      inline: { rows: 20, withFiles: true }
      link: { expiresIn: 30d, requireLogin: false }   # durations: 30d, 12h, 90m, 3600s
      options: { channel: "#sales" }                  # the destination's delivery options
```

A `Report` MAY reference a query (`query: { ref: slug }`) or hold one inline
(`query: { connection, sql, params }`), which becomes a `Query` with the report's slug, title and
description. Exports always write queries as their own documents and reference them, so an export
reads back exactly.

## Rules

- References are **by name**, never by id. For queries and reports `metadata.name` is the slug; the
  free-text title goes in `metadata.title` (the name when absent).
- **Secrets are never exported.** Export writes `${env:ROWBIRD_<KIND>_<NAME>_<FIELD>}` for each secret
  that is set (for example `${env:ROWBIRD_CHANNEL_SALES_SLACK_BOT_TOKEN}`). Import resolves
  `${env:NAME}` from values the user gives in the UI (`secrets`), then from the environment of the
  server (GitOps) or of the CLI (`apply`, which sends them with `--server`). A placeholder without a
  value is required for a new resource and keeps the stored secret for an existing one. A literal
  secret in a document is accepted.
- `apiVersion` is mandatory and must be `rowbird.dev/v1`; other versions are rejected (later
  versions will be converted).
- Validation uses the same plugin schemas and service rules as the API (the import calls the same
  services). Errors carry the file, line and column, and every problem is reported at once.
- Fields a document leaves out keep their current value on update, except the declared ones:
  `config` of connections and channels (replaced as a whole, as the API does), and a report's
  `condition`, `params` and `deliveries`: leaving one out empties it. Deliveries are replaced only
  when they change, so unchanged ones keep their ids and the resends of their runs.

## Import / apply semantics

1. Parse and validate all documents; fail with every error and its position.
2. Resolve references, first within the documents, then in the workspace; missing ones go to the UI
   mapping step or `--map old=new` (connections and channels).
3. Build a **plan**, item by item in dependency order (connections, channels, queries, reports):
   `create`, `unchanged`, or `conflict` (exists and differs, with a field-by-field diff; secrets
   are compared with the stored values and shown only as "changes").
4. Conflict policy: `overwrite` (update) | `copy` (create `name-2`, `name-3`, ...; references in the
   documents follow the copy) | `skip` | `fail` (the default). The UI chooses per item, the CLI with
   `--policy`.
5. Apply is **one transaction** through the services; connections are checked after it commits. A
   dry run applies and rolls back, so it reports every error a real apply would.
6. GitOps mode (`ROWBIRD_CONFIG_DIR`): see below.

## GitOps directory

`ROWBIRD_CONFIG_DIR` names a directory whose `.yaml` and `.yml` files (recursively, in name order)
the server applies at startup, or right after setup when it starts before one. It applies with
`overwrite`, secrets from the server's environment, and records who manages each resource
(`managed_by`):

- `gitops`: applied from the directory. The API refuses changes to it (`409
  resource.managed_by_gitops`) and the UI shows it read only.
- `gitops_detached`: an admin chose "Edit anyway" (`POST /gitops/detach`). The UI can change it and
  the directory leaves it alone (logged at each start) until an admin gives it back
  (`POST /gitops/attach`).

A `gitops` report whose document left the directory is **paused** (`paused_reason: gitops_orphan`,
`gitops_orphan: true`), admins get a notification and the log a warning; nothing is deleted. It
resumes when its document returns (unless the document says `enabled: false`). Orphan connections,
channels and queries stay as they are. A directory that does not parse or whose plan is blocked
(errors, missing references or secrets) applies nothing; the problems go to the log and to an
admin notification (`gitops_failed`), resolved by the next successful apply.

## Export

A selection of reports (each with its query) and queries, optionally with the connections and
channels they use, or the whole workspace. Deterministic output: documents ordered by kind and name,
map keys sorted (a rule's `type` first), so diffs in Git are clean. `POST /export` (editor),
`rowbird export`, and the export dialog in the UI.
