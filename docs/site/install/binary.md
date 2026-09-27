# Binary and systemd

Every [release](https://github.com/rowbird/rowbird/releases) has archives for Linux, macOS and
Windows, on amd64 and arm64. The binary is static: no runtime, no libc, no CGO.

```bash
VERSION=1.0.0
curl -LO https://github.com/rowbird/rowbird/releases/download/v$VERSION/rowbird_${VERSION}_linux_amd64.tar.gz
tar xzf rowbird_${VERSION}_linux_amd64.tar.gz rowbird
./rowbird version
```

[Verify the download](./verify) before you install it on a server.

## Running it

```bash
ROWBIRD_DATA_DIR=./data ROWBIRD_BASE_URL=http://localhost:8080 ./rowbird serve
```

`serve` is the default command, so `./rowbird` alone does the same. Without `ROWBIRD_DATA_DIR`
Rowbird uses `/data`.

## systemd

[`deploy/systemd/rowbird.service`](https://github.com/rowbird/rowbird/blob/main/deploy/systemd/rowbird.service)
runs Rowbird as its own user with a hardened sandbox, keeping its data in `/var/lib/rowbird`.

```bash
sudo install -m 0755 rowbird /usr/local/bin/rowbird
sudo useradd --system --home /var/lib/rowbird --shell /usr/sbin/nologin rowbird
sudo install -d -o root -g rowbird -m 0750 /etc/rowbird
sudoedit /etc/rowbird/rowbird.env
sudo cp rowbird.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now rowbird
```

`/etc/rowbird/rowbird.env` holds the configuration, one variable per line:

```ini
ROWBIRD_BASE_URL=https://rowbird.example.com
ROWBIRD_LISTEN_ADDR=127.0.0.1:8080
ROWBIRD_TRUSTED_PROXIES=127.0.0.1/32
ROWBIRD_BACKUP_SCHEDULE=0 3 * * *
```

Logs go to the journal: `journalctl -u rowbird -f`. Administration commands must run as the
`rowbird` user with the same environment, for example:

```bash
sudo -u rowbird sh -c 'set -a; . /etc/rowbird/rowbird.env; ROWBIRD_DATA_DIR=/var/lib/rowbird \
  exec rowbird user reset-password --email admin@example.com'
```

## macOS

The macOS binaries are not notarized. macOS blocks them the first time; allow the binary with
`xattr -d com.apple.quarantine ./rowbird`, or run the Docker image instead.

## Windows

The Windows archive has `rowbird.exe`. Set `ROWBIRD_DATA_DIR` to a writable folder, since the
default `/data` does not exist on Windows:

```powershell
$env:ROWBIRD_DATA_DIR = "C:\ProgramData\Rowbird"
.\rowbird.exe serve
```
