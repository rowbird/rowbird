# Config as code (YAML)

Connections, channels, queries and reports can be written as YAML documents. The same format is
used in four places:

- **Export and import** in Settings > Import and export.
- **`rowbird apply`** from the command line, locally or against a remote server.
- **A GitOps directory** that the server applies at every start (`ROWBIRD_CONFIG_DIR`).
- **Sharing**: a report someone else wrote can be imported as is.

## Documents

A file holds one or more documents separated by `---`. Every document has `apiVersion`
(`rowbird.dev/v1`), `kind`, `metadata` and `spec`. Unknown fields are errors, reported with their
file, line and column.

```yaml
apiVersion: rowbird.dev/v1
kind: Connection
metadata:
  name: production-db
spec:
  driver: postgres
  config: { host: db.internal, port: 5432, database: shop, user: rowbird_reader, tls_mode: require }
  secrets:
    password: ${env:DB_PASSWORD}
  options: { queryTimeoutSeconds: 60, maxRows: 100000, allowMultiStatement: false, aiExcludedTables: [salaries] }
---
apiVersion: rowbird.dev/v1
kind: Channel
metadata:
  name: sales-slack
spec:
  type: slack
  config: { auth: bot }
  secrets: { bot_token: ${env:SLACK_BOT_TOKEN} }
---
apiVersion: rowbird.dev/v1
kind: Query
metadata:
  name: sales-by-region        # the slug
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
  params: { region: north }    # overrides the query's defaults
  enabled: true
  run: { maxRows: 5000, retryMax: 2, retryBackoffSeconds: 30, misfire: run_once, overlap: skip, autoPauseAfter: 5, notifyOwner: true }
  deliveries:
    - channel: sales-slack
      mode: link               # inline | attachment | link
      formats: [xlsx]
      enabled: true
      inline: { rows: 20, withFiles: true }
      link: { expiresIn: 30d, requireLogin: false }   # 30d, 12h, 90m, 3600s
      options: { channel: "#sales" }                  # the destination's delivery options
```

The keys under `config`, `secrets` and a delivery's `options` are the plugin's own fields: the same
the forms in the UI show. `GET /api/v1/plugins` lists them for every plugin.

A report may reference a query (`query: { ref: slug }`) or hold one inline
(`query: { connection, sql, params }`); an inline query becomes a query with the report's slug.
Exports always write queries as their own documents.

## Rules

- **References are by name**, never by id. For queries and reports, `metadata.name` is the slug and
  `metadata.title` the display name.
- **Secrets are never exported.** An export writes a placeholder per secret, such as
  `${env:ROWBIRD_CHANNEL_SALES_SLACK_BOT_TOKEN}`. On import, placeholders are filled from values
  given in the UI, then from the environment of the server (GitOps) or of the CLI (`apply`). A
  placeholder without a value is required for a new resource and keeps the stored secret for an
  existing one. A literal secret in a file is accepted, but keep secrets out of Git.
- **Leaving a field out keeps its value** on update, except `config` of connections and channels
  (replaced as a whole) and a report's `condition`, `params` and `deliveries` (leaving one out
  empties it).
- Exports are deterministic (sorted documents and keys), so diffs in Git stay small.

## Applying

Every import builds a **plan** first, like Terraform: each item is `create`, `unchanged` or
`conflict` (it exists and differs, shown field by field; secrets only as "changes"). Conflicts
follow a policy:

| Policy | Conflicting resources are |
|---|---|
| `fail` (default) | reported, and nothing is applied |
| `overwrite` | updated to match the documents |
| `copy` | created again as `name-2`, `name-3`, ..., and references follow the copy |
| `skip` | left as they are |

The whole import is one transaction; a dry run applies and rolls back, so it reports every error a
real apply would. References missing from the documents and the workspace can be mapped to existing
connections and channels (`--map old=new`).

```bash
rowbird apply -f reports/ --dry-run
rowbird apply -f reports/ --policy overwrite
rowbird apply -f reports/ --server https://rowbird.example.com   # with ROWBIRD_API_KEY
rowbird export --with-connections --with-channels -o workspace.yaml
```

`apply` prints the plan and exits with status 1 when it is blocked or fails, which suits CI.

## GitOps directory

Set `ROWBIRD_CONFIG_DIR` to a directory (for example a Git checkout mounted read-only). At every
start, or right after the setup wizard on a new server, Rowbird applies its `.yaml` and `.yml`
files, recursively and in name order, with the `overwrite` policy and secrets from the server's
environment.

- Applied resources are **managed by GitOps**: the API refuses to change them
  (`409 resource.managed_by_gitops`) and the UI shows them read only.
- An admin can choose **Edit anyway**, which detaches the resource: the UI can change it and the
  directory leaves it alone until an admin gives it back.
- A report whose document left the directory is **paused** as an orphan and the admins are
  notified. Nothing is deleted. It resumes when its document returns.
- A directory that does not parse, or whose plan is blocked, applies nothing; the problems go to the
  log and to an admin notification, cleared by the next successful apply.

To apply changes, update the directory and restart Rowbird, or run `rowbird apply` against the
server from your CI.
