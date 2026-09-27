# Try the demo

The repository has a ready-made demo in
[`deploy/demo`](https://github.com/rowbird/rowbird/tree/main/deploy/demo): Rowbird, a PostgreSQL
database with a fictional coffee shop, and [Mailpit](https://mailpit.axllent.org), a mail server
that catches every email and shows it in a web page. Nothing leaves your machine.

```bash
git clone https://github.com/rowbird/rowbird.git
cd rowbird/deploy/demo
docker compose up
```

1. Open http://localhost:8080 and create the first admin.
2. A few seconds later Rowbird applies the demo's
   [configuration directory](/reference/config-as-code#gitops-directory): a connection, an email
   channel, four queries and four reports. They are managed by GitOps, so the UI shows them read
   only; an admin can choose "Edit anyway".
3. Open http://localhost:8025. "Sales pulse" arrives every minute with a table in the body.
   "Low stock alert" arrives once: it sends only when some product is below its reorder point
   **and** the list changed since the last run.
4. Open "Weekly top customers" and press "Run now" for Excel and PDF attachments, or "Monthly
   revenue by category" for an email with a download link.

Pause "Sales pulse" when you have seen enough of it. `docker compose down -v` removes everything.

## What to look at

- **Runs.** Every run shows its rows, the condition's outcome and each delivery attempt.
- **Delivery preview.** In a report, open a delivery and press "Preview" to see the message
  without sending it.
- **Export.** Settings > Import and export writes the reports as YAML: the same format as the
  files in `deploy/demo/config`.
- **Read-only access.** The connection uses `rowbird_reader`, a login that can only read. That is
  the setup we recommend for every database.
