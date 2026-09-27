# Quick start

This starts Rowbird with Docker, SQLite as its internal store and everything in one volume.

```bash
docker run -d --name rowbird \
  -p 8080:8080 \
  -v rowbird-data:/data \
  -e ROWBIRD_BASE_URL=http://localhost:8080 \
  ghcr.io/rowbird/rowbird:1
```

Open http://localhost:8080. The setup wizard creates the first admin: whoever opens it first
becomes the admin, so on a server reachable by others set `ROWBIRD_SETUP_TOKEN` and give the token
to the person doing the setup.

::: warning Back up the master key
On first start Rowbird generates a master key in `/data/master.key` and uses it to encrypt every
stored credential. Copy it somewhere safe now. Without it, a backup cannot be read. See
[Configuration](/operations/configuration#master-key) to provide your own key instead.
:::

## Your first report

1. **Add a channel.** Channels > New channel. For email, fill in your SMTP server and
   press "Send test". Mark one email channel as the system mailer so Rowbird can send password
   resets and "send test to me".
2. **Add a connection.** Connections > New connection. Use a database login that can only read;
   Rowbird warns you when the login can write. Test it before saving.
3. **Write a query.** Queries > New query. Pick the connection, write SQL, press "Run preview".
   Use built-in dates such as <code v-pre>{{yesterday}}</code> and <code v-pre>{{start_of_month}}</code>, or define your
   own parameters.
4. **Create a report.** From the query, "New report". Choose a schedule (the builder shows the
   next runs), an optional condition, and a delivery to your channel.
5. **Try it.** "Send test to me" emails you the message, or "Run now" runs it for real.

The home page keeps a checklist of these steps until you have done them.

## Next steps

- [Try the demo](./demo) with sample data and a fake mail server, no setup needed.
- [Install with Docker Compose](/install/compose) behind HTTPS for real use.
- Read about [backups](/operations/backups) before you depend on it.
