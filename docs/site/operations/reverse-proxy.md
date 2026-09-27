# Reverse proxies

Rowbird speaks plain HTTP on port 8080. Put a reverse proxy in front for HTTPS, and tell Rowbird
about it:

- `ROWBIRD_BASE_URL` is the public `https://` URL. Rowbird uses it for links, passkeys and to mark
  cookies `Secure`.
- `ROWBIRD_TRUSTED_PROXIES` lists the proxy's addresses (CIDRs). Only these may set
  `X-Forwarded-For` (the client IP, used by rate limits and the security log) and
  `X-Forwarded-Proto`. Requests from anywhere else have those headers ignored.

## Server-sent events

The UI receives live updates from `GET /api/v1/events`, a `text/event-stream` response that stays
open. The proxy must pass it through **without buffering** and must not time it out quickly.
Rowbird sends a comment every 15 seconds to keep the connection alive and sets
`X-Accel-Buffering: no`, which Nginx honors. If updates only appear after a reload, the proxy is
buffering the stream.

## Nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name rowbird.example.com;

    ssl_certificate     /etc/letsencrypt/live/rowbird.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/rowbird.example.com/privkey.pem;

    client_max_body_size 10m;   # YAML imports

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location = /api/v1/events {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host              $host;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 1h;
    }
}
```

With Nginx on the same host, use `ROWBIRD_TRUSTED_PROXIES=127.0.0.1/32` and bind Rowbird to
`127.0.0.1:8080`.

## Caddy

```caddy
rowbird.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080 {
		flush_interval -1
	}
}
```

Caddy obtains the certificate and sets `X-Forwarded-For` and `X-Forwarded-Proto` by itself.
`flush_interval -1` sends every event as soon as it is written. The
[Compose setup](/install/compose) uses exactly this.

## Traefik

With the Docker provider, as labels on the Rowbird container:

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.rowbird.rule=Host(`rowbird.example.com`)
  - traefik.http.routers.rowbird.entrypoints=websecure
  - traefik.http.routers.rowbird.tls.certresolver=letsencrypt
  - traefik.http.services.rowbird.loadbalancer.server.port=8080
```

Traefik streams responses without buffering unless you add the `buffering` middleware; do not
add it to this router. Set `ROWBIRD_TRUSTED_PROXIES` to the Docker network Traefik uses.

## A path prefix

Rowbird expects to be served at the root of its host. Serving it under a path such as
`https://example.com/rowbird/` is not supported; use a subdomain.
