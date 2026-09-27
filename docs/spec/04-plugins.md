# 04: Plugins

## Common contract

```go
type Metadata struct {
    ID, Name, Description, Icon, Version string
}

type Plugin interface {
    Meta() Metadata
    ConfigSchema() Schema        // JSON Schema subset; secret fields have "x-secret": true
    Capabilities() Capabilities  // kind-specific, see below
}
```

- `Icon` names the picture the UI shows next to the plugin: a brand whose free logo the UI bundles
  (from simple-icons, CC0: `postgres`, `sqlite`, `mysql`, `discord`, `telegram`, `uptimekuma`) or a
  generic icon (`mssql`, `mail`, `slack`, `bucket`, `webhook`, `database`); unknown names get a plug.
- Schemas drive UI forms, API validation, YAML import validation and secret handling. Supported:
  types `string`, `integer`, `number`, `boolean`; `enum`, `default`, `minimum`, `maximum`,
  `minLength`, `maxLength`, `format`; extensions `x-order` (field order), `x-secret`, `x-group`
  (`connection`, `tls`, `ssh`, `advanced`), `x-multiline`, `x-show-if` (`{field, in}`), `x-label`
  and `x-help` (ADR-0018). Hidden fields are neither validated nor stored.
- Labels/descriptions in schemas are **i18n keys** (`plugin.slack.token.label`). Each plugin embeds
  its catalogs in `locales/<lang>.json`; `GET /plugins` returns them and the UI merges them. The
  label of an enum option is the label key with `.label` replaced by `.<value>`, when translated.
- Registration: `plugin.Register(kind, id, factory)` inside the plugin package `init()`;
  enabled by a blank import in `internal/app/plugins.go`.
- Each kind has a **conformance suite** (`internal/plugin/<kind>test`) every plugin MUST pass.

## Connectors

```go
type Connector interface {
    Plugin
    Open(ctx context.Context, opts OpenOptions) (Conn, error) // values, dialer, SQLite directories
}
type Conn interface {
    Ping(ctx context.Context) (ServerInfo, error)
    Schema(ctx context.Context) (*Schema, error)
    CanWrite(ctx context.Context) (*bool, error)             // nil = unknown
    Query(ctx context.Context, q BoundQuery, opts QueryOptions) (RowStream, error)
    Close() error
}
```

- `QueryOptions`: timeout, max rows, read-only, allow multi-statement.
- Capabilities: `readOnlyTx`, `serverTimeout`, `cancel`, `multiStatement`, `placeholderStyle`.
- **Normalized types:** `text`, `integer`, `decimal` (string-backed, exact), `float`, `boolean`,
  `date`, `datetime` (with/without tz), `time`, `json`, `binary`, `unknown`.
- Common options for network connectors: TLS mode (`disable`, `prefer`, `require`, `verify-ca`,
  `verify-full`; SQL Server adds `strict`) + CA/cert/key in PEM, connect timeout, **SSH tunnel**
  (host, port, user, password or private key + passphrase, host key fingerprint). Connectors dial
  through the dialer they receive, which applies the network policy or goes through the tunnel;
  DNS is resolved there (or on the SSH host), never by the driver itself.
- A query with more than one statement is refused unless the connection allows it, using a lexical
  scan that ignores literals and comments (`internal/sqlscan`). SQL Server does not require `;`
  between statements, so there the scan only catches `;` and `GO`; its safety relies on a read-only
  login (ADR-0013).
- The row limit reads `max_rows + 1` rows and marks the result as truncated.
- Each connector passes `internal/plugin/connectortest` (metadata and translations, ping, type
  normalization with a 38-digit decimal, schema, bound parameters, row limit, multi-statement
  rejection, read-only enforcement, timeout, cancel, write-permission check, error codes). Network
  connectors run it against real databases with testcontainers.

| Driver | Library | Read-only strategy | Server timeout | Cancel |
|---|---|---|---|---|
| `postgres` | pgx (database/sql) | `BEGIN READ ONLY` | `SET LOCAL statement_timeout` | protocol cancel |
| `mysql` (MySQL + MariaDB) | go-sql-driver/mysql | `START TRANSACTION READ ONLY` | MySQL `max_execution_time`, MariaDB `max_statement_time` | `KILL QUERY` from another connection |
| `mssql` | microsoft/go-mssqldb | rely on DB permissions (warn) | client deadline (TDS attention) + `SET LOCK_TIMEOUT` | TDS attention |
| `sqlite` | modernc.org/sqlite | open with `mode=ro` + `query_only` | client deadline | interrupt |

Notes: MySQL's `max_execution_time` does not apply to `SLEEP()` (it returns early without an error)
and only to `SELECT`. MySQL `TINYINT(1)` is reported as `integer`. MariaDB `JSON` is `LONGTEXT` and is
reported as `text`. SQLite stores `DECIMAL` with REAL or INTEGER affinity, so its decimals are only as
exact as SQLite keeps them. SQLite files must be inside `ROWBIRD_SQLITE_DIRS`, symlinks are resolved
before the check, and the internal store is always refused.

## Formatters

```go
type Formatter interface {
    Plugin
    Format(ctx context.Context, in FormatInput, w io.Writer) (FormatResult, error)
}
```

`FormatInput`: column metadata, row iterator (from spool), total row count and whether the row limit
cut the result, locale, timezone, report title, run id and time, options (validated by the plugin's
schema), and for inline outputs `MaxRows`, `MaxChars` and the link to the full result; an optional
logo for documents. `FormatResult`: rows written and whether an inline output left something out.
Capabilities: `kind` (`file|inline`), `content_type`, `extension`, `inline_target`
(`html|markdown|text`). Formatters live in `internal/format/<name>` and pass
`internal/plugin/formattest` with golden files in `en` and `pt-BR` (ADR-0021).

| ID | Kind | Notes |
|---|---|---|
| `csv` | file | Options: `delimiter` (auto, comma, semicolon, tab, pipe), `bom` (auto, yes, no), `number_format` (locale, plain), `encoding` (UTF-8, Windows-1252 with `?` for unmappable characters), `header`. Automatic defaults for `pt-BR`: `;`, UTF-8 BOM, decimal comma; for `en`: `,`, no BOM, decimal point. No digit grouping, ISO dates, booleans `true`/`false`, binary as base64, CRLF line ends. Formula-injection neutralization. |
| `xlsx` | file | typed cells (numbers up to 15 significant digits, longer ones as text; dates and datetimes with the language's format), bold frozen header, autofilter, column widths fitted to the first 100 rows (8 to 60), sheet named after the report (31 characters, invalid characters replaced). |
| `json` | file | `array` (default) or `ndjson`, keys in column order. Decimals as strings (option: numbers); integers beyond 2^53 as strings; JSON columns embedded; datetimes as RFC 3339 in the report's time zone. |
| `pdf` | file | pure Go (maroto) with the embedded Go fonts. Title, generated-at, orientation (auto: landscape when > 6 columns), header repeated on every page, zebra rows, numbers right-aligned, page numbers, optional logo. At most 5,000 rows, then "showing N of M". |
| `html_table` | inline | inline CSS only, email-client-safe, escaped content, zebra rows, "showing N of M" footer with a link. |
| `markdown_table` | inline | Slack/Discord; code-block fallback when a row is wider than 100 characters; respects length limits. |
| `text` | inline | Telegram (HTML parse mode, escaped): an aligned table in `<pre>`, cells cut at 40 characters. |

Locale-aware formatting (`1.234,56` / `25/09/2026` for pt-BR; ADR-0021). Inline renderers MUST
truncate to fit destination limits (they drop rows, never cut a row in half) and append "view full
report" when a link is given; only absolute http(s) links are used.

## Conditions

```go
type Condition interface {
    Plugin
    // params were validated by ConfigSchema (the rule's parameters).
    Evaluate(ctx context.Context, in ConditionInput, params map[string]any) (RuleOutcome, error)
}
```

`ConditionInput`: columns, the first row (nil when empty), row count, truncated, result hash, the
hash of the last delivered result and the report's time zone. `RuleOutcome`: passed plus a detail
map for the run page (the actual value, the count compared). A rule that cannot be evaluated returns
a `ConditionError` (`condition.column_not_found`, `condition.invalid_value`), which fails the run.
Capabilities: `needs_column`.

Stored on the report as:

```json
{ "match": "all", "rules": [ { "type": "row_count", "op": "gt", "value": 0 } ] }
```

| Type | Params |
|---|---|
| `always` | none (default when no rules) |
| `has_rows` / `is_empty` | none |
| `row_count` | `op` (`eq ne gt gte lt lte`, default `gt`), `value` (default 0). When the row limit cut the result the count is a lower bound (the detail says so). |
| `value` | `column`, `op` (+ `between`, inclusive), `value`, `value2`; first row; typed comparison |
| `changed` | compares `result_hash` with report `last_result_hash`; the first result counts as changed |

`value` compares with the column's type: integers and decimals exactly, floats as floats, dates and
times in calendar order, booleans only with `eq`/`ne`, text by bytes. Values are text: `2026-09-25`,
`2026-09-25 14:30` (the report's time zone for columns with a time zone, wall time otherwise) or
RFC 3339, numbers with a dot. NULL and an empty result never pass.

`match`: `all` or `any`; every rule is evaluated so the run shows all outcomes. At most 20 rules.
Built-in conditions live in `internal/condition` and pass `internal/plugin/conditiontest`.

## Destinations

```go
type Destination interface {
    Plugin
    DeliverySchema() *Schema   // per-delivery options (recipients, templates, path, ...)
    Send(ctx context.Context, env DestinationEnv, msg Message) (SendResult, error)
    Test(ctx context.Context, env DestinationEnv) error
    Preview(ctx context.Context, env DestinationEnv, msg Message) (Preview, error)
}
```

A **channel** is a destination with its configuration; a **delivery** sends a report's runs to a
channel with options validated by `DeliverySchema`. `DestinationEnv` carries the channel's
configuration (secrets included), the delivery's options, an HTTP client and a dialer bound to the
network policy, and the locale for texts the destination writes itself. `Message`: report and run
metadata (with links to the report and run pages when `ROWBIRD_BASE_URL` is set), status
(`up`, `down` or `skipped`), locale and time zone, the inline rendering (and whether rows were left
out), attachments (reopenable, so a retry reads them again), links, the first rows for destinations
that send data, whether it is a test and whether files fell back to links. `Preview` returns a
subject, a body (`html` or `text`) and the names of attachments and links.

Capabilities: `supports_attachments`, `max_attachment_bytes` (all files of a message together),
`max_text_chars`, `inline_target` (`html|markdown|text`, empty for none), `supports_status`,
`always_notify` (gets failed and skipped runs too) and `modes` (the delivery modes it supports; the
first is the default). Destinations whose capabilities depend on the channel (a Slack incoming
webhook cannot upload files, attachment limits are configurable) implement `CapabilitiesFor(config)`;
`GET /channels` returns the channel's capabilities, `GET /plugins` the defaults and the
`delivery_schema`. A destination may also check options beyond its schema (`ValidateDelivery`, used
for email address lists).

Errors are `DeliveryError{Code, Retry}` with stable codes: `delivery.auth_failed`,
`delivery.rejected`, `delivery.unreachable`, `delivery.too_large`, `delivery.rate_limited`,
`delivery.timeout`, `delivery.network_blocked` and `delivery.failed`; the engine adds
`delivery.base_url_missing`, `delivery.channel_unavailable` and `delivery.result_unavailable`.
Shared helpers in `internal/destination` map HTTP statuses and network failures (401/403 auth,
413 too large, 429 and 5xx retried, timeouts and unreachable hosts retried, the network policy
never), and remove the channel's secrets from messages stored on attempts and channel health.
Every destination passes `internal/plugin/destinationtest` (metadata and translations, valid
schemas, send and test against a fake service, preview without sending, failures mapped to stable
codes, retryable or not, no secret in errors, cancellation).

| ID | Config (secrets *) | Delivery options | Modes | Notes |
|---|---|---|---|---|
| `email` | host, port (587), TLS mode (`starttls`, `tls`, `none`), username, password*, from address, from name, max attachment MB (20, at most 200) | to (required), cc, bcc (comma separated, validated), subject tpl, intro tpl | inline, attachment, link | HTML (email-safe inline table) + plain-text alternative. SMTP errors mapped to delivery codes. The system mailer is an email channel. |
| `telegram` | bot token*, API base URL | chat id, message tpl, silent | inline, attachment, link | HTML parse mode (everything escaped); 4096-char messages; files with `sendDocument` (50 MB). |
| `slack` | auth (`bot` or `webhook`), bot token* or incoming-webhook URL*, API base URL | channel, message tpl | bot: inline, attachment, link; webhook: inline, link | Markdown table; 40,000-char messages; files through the external upload flow (`files.getUploadURLExternal` + `files.completeUploadExternal`, 100 MB). |
| `discord` | webhook URL*, max attachment MB (8, at most 500) | message tpl, username | inline, attachment, link | 2000-char messages; files as multipart. |
| `webhook` | URL, method (`POST`, `PUT`), headers, secret headers* (`Name: value` lines), HMAC secret* | include rows (0 to 1000), include links (default true) | inline, link | JSON payload `{event, delivery_id, status, report, run, columns, rows, links, test}`; headers `X-Rowbird-Event` (`run.completed` or `test`), `X-Rowbird-Delivery`, `X-Rowbird-Signature: t=<unix>,v1=<hex hmac-sha256("<t>.<body>")>`. Rowbird's own headers cannot be overridden. |
| `uptime_kuma` | push URL* | status for skipped runs (`up` default, `down`), message tpl | inline | Gets every run: `GET <push>?status=up|down&msg=<...>&ping=<duration_ms>`; failed runs are `down`. |
| `s3` | endpoint (default AWS), region, bucket, access key*, secret key*, path-style | path tpl, content disposition (`attachment`, `inline`) | attachment | One object per file; the default path is `reports/{{report.slug}}/{{run.date}}/{{report.slug}}.{{format}}`. Paths are cleaned and may not climb out of the bucket. No size limit short of 5 GB. |

### Message templates

Mustache syntax, rendered by `internal/msgtemplate` for subjects, message texts and S3 paths.
Templates are written by editors, so they are sandboxed: partials are refused, `{{{x}}}` and `{{&x}}`
are read as `{{x}}` (escaping cannot be turned off), only the variables below exist (unknown ones
fail validation with `validation.template_variable`, syntax errors with `validation.template`), and
a template has at most 10,000 characters. Values are escaped for the destination: HTML for email
and Telegram, Slack's `&`, `<`, `>` for Slack, none for Discord, Uptime Kuma, webhooks (the JSON is
built by the destination) and paths. Variables, formatted for the reader's language:
`report.name` (and its alias `report.title`), `report.slug`, `report.url`, `run.id`, `run.url`,
`run.status` (translated), `run.rows`, `run.truncated` (usable as a section), `run.duration`,
`run.started_at`, `run.date` (`YYYY-MM-DD` in the report's time zone), `link.url` and
`link.expires_at` (the first link), `condition.summary` and `format` (the formats sent, comma
separated; the file's own format in S3 paths). Each destination has a localized default template,
used when the option is empty.

## AI providers

```go
type AIProvider interface {
    Plugin
    // JSON matching req.Output; errors are AIError with a stable code (ai.auth_failed, ...).
    Complete(ctx context.Context, env AIEnv, req AIRequest) (AIResponse, error)
    // Cheapest check of key and model (reading the model where the API allows).
    Test(ctx context.Context, env AIEnv) error
}
type AIRequest struct { System, User, OutputName string; Output map[string]any /* JSON Schema */ }
```

IDs: `openai`, `anthropic`, `gemini`, `ollama`, `openai_compatible` (base URL: OpenRouter, LM Studio,
vLLM, Groq, ...). The AI service (`internal/ai`) builds the instructions and the request from the
user's request, the dialect, the schema minus excluded tables, the current SQL and the locale, and
validates the answer (ADR-0025); providers only carry them. Structured output per provider: OpenAI
a strict JSON schema; Anthropic structured outputs (`output_config.format`) through the official Go
SDK, ignoring the host's environment credentials; Gemini `responseJsonSchema` with the key in a
header; Ollama the chat `format`; OpenAI-compatible JSON mode by default (a JSON schema optionally)
with the schema in the instructions. Every field of an answer is required and "none" is an empty
string. HTTP goes through the network policy. Capabilities: `structured` (`json_schema`, `tool` or
`json`) and `local` (no key needed). Providers pass `internal/plugin/aitest`. Never sends row data.

## Artifact storage

Artifact storage is **not a plugin**: one backend is chosen for the whole instance by configuration
(`ROWBIRD_STORAGE_BACKEND`, 08), not per workspace in the UI, so it has no schema, translations or
registry entry (ADR-0023). The contract lives in `internal/plugin/storage.go` so that the delivery
engine and links depend only on it:

```go
type Storage interface {
    Put(ctx context.Context, key string, r io.Reader, meta ObjectMeta) error
    Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error)
    Delete(ctx context.Context, key string) error
    // ErrPresignUnsupported when the backend cannot presign.
    PresignGet(ctx context.Context, key string, ttl time.Duration, meta ObjectMeta) (string, error)
}
```

Backends live in `internal/storage/<name>` and pass `internal/storage/storagetest`: `local`
(default, under `$ROWBIRD_DATA_DIR/artifacts`, written atomically, no presigning) and `s3` (any
S3-compatible service, with an optional key prefix; integration-tested against the Versity S3
Gateway). Keys are relative slash-separated paths without `.` or `..` segments. Shared link
downloads stream from storage, or redirect to a presigned URL valid for at most 5 minutes with the
file name as content disposition when the backend supports it.

## Languages

`web/src/locales/<lang>.json` and `internal/i18n/locales/<lang>.json`. Adding a language = adding
both files + registering it in the language list. CI checks that all keys in `en` exist in every
other locale (missing keys fall back to `en` at runtime).
