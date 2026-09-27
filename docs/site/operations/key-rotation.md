# Master key rotation

`rowbird keys rotate` encrypts every stored secret again with a new master key: connection and
channel secrets, TOTP secrets and secret settings (AI keys, the OIDC client secret, the heartbeat
URL). It works in one database transaction, so it either rotates everything or nothing.

Rotate when the key may have leaked, when someone who had it leaves, or on a schedule your security
policy sets.

```bash
rowbird keys rotate --dry-run   # decrypt and encrypt everything, then roll back
rowbird keys rotate
```

The rotation records a `keys_rotated` security event. Signed-in users are asked to sign in again,
because request forgery tokens derive from the key.

## A single instance

Stop the server, rotate, start it again.

**With the generated key** (`$ROWBIRD_DATA_DIR/master.key`), the command creates the new key, or
takes one from `--new-key-file`. It writes the new key to `master.key.next` before touching the
database, and after the commit moves it into place and keeps the old one as
`master.key.previous`. Back up the new `master.key`.

**With a configured key** (`ROWBIRD_MASTER_KEY` or `ROWBIRD_MASTER_KEY_FILE`), generate the new key
yourself and pass it with `--new-key-file`; then configure the new key and start the server.

```bash
openssl rand -base64 32 > master.key.new
docker compose stop rowbird
docker compose run --rm -v "$PWD/master.key.new:/tmp/new.key:ro" rowbird \
  keys rotate --new-key-file /tmp/new.key
# replace secrets/master.key with master.key.new, then
docker compose start rowbird
```

## Several instances (PostgreSQL)

Instances keep running during the rotation:

1. Restart every instance with the **new** key as `ROWBIRD_MASTER_KEY` and the **current** one as
   `ROWBIRD_MASTER_KEY_PREVIOUS`. Instances decrypt with either key.
2. Run `rowbird keys rotate --new-key-file <new key>` from one place.
3. Restart the instances without `ROWBIRD_MASTER_KEY_PREVIOUS`.

Instances record the id of their key in their heartbeat, and the command refuses while an
instance that runs workers still uses another key. `--force` overrides that check.

## If something goes wrong

A secret setting the command does not recognize stops it before anything changes. Any error rolls
the transaction back and the database keeps the old key. With the generated key, the old key stays
in `master.key` until the commit succeeds.
