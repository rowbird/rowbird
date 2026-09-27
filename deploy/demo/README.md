# Rowbird demo

Rowbird, a Postgres database with a fictional coffee shop, and [Mailpit](https://mailpit.axllent.org)
to catch the email Rowbird sends. Nothing leaves your machine.

```bash
docker compose up
```

1. Open http://localhost:8080 and create the first admin in the setup wizard.
2. A few seconds later Rowbird applies the [config](config) directory: a connection to the shop
   database, an email channel pointing at Mailpit, four queries and four reports. They are marked
   as managed by GitOps, so the UI shows them read only (an admin can choose "Edit anyway").
3. Open http://localhost:8025. "Sales pulse" arrives every minute with a table in the body, and
   "Low stock alert" arrives once, because it only sends when the list of products changes.
4. Open "Weekly top customers" and press "Run now" to get Excel and PDF attachments, or
   "Monthly revenue by category" for an email with a download link.

Pause "Sales pulse" when you have seen enough of it.

| Service | URL | Notes |
|---|---|---|
| Rowbird | http://localhost:8080 | data in the `rowbird-data` volume |
| Mailpit | http://localhost:8025 | every email Rowbird sends ends up here |
| Postgres | `postgres:5432` inside the network | Rowbird reads it as `rowbird_reader`, a read-only login |

To try a build that is not published yet, point `ROWBIRD_IMAGE` at it:

```bash
ROWBIRD_IMAGE=ghcr.io/rowbird/rowbird:0.9.0 docker compose up
```

`docker compose down -v` removes the containers and the demo's data.
