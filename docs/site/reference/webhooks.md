# Webhook payloads

A webhook channel sends a JSON `POST` (or `PUT`) to your URL for each run it delivers, for tests,
and for system alerts when it is an alert channel.

## Headers

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `X-Rowbird-Event` | `run.completed`, `test` or `alert` |
| `X-Rowbird-Delivery` | the delivery's id |
| `X-Rowbird-Signature` | `t=<unix seconds>,v1=<hex HMAC-SHA256>`, when the channel has an HMAC secret |

Headers configured on the channel are added, but cannot replace Rowbird's own, `Content-Type`,
`Content-Length` or `Host`.

## Run payload

```json
{
  "event": "run.completed",
  "delivery_id": "01a0e418-d85f-7439-948d-bf50d604d3d9",
  "status": "up",
  "report": {
    "id": "01a0e418-d862-7b50-b9cf-689c08016538",
    "title": "Low stock alert",
    "slug": "low-stock-alert",
    "url": "https://rowbird.example.com/reports/01a0e418-d862-7b50-b9cf-689c08016538"
  },
  "run": {
    "id": "01a0e41c-fed4-7933-a97a-9344f105f3c6",
    "url": "https://rowbird.example.com/runs/01a0e41c-fed4-7933-a97a-9344f105f3c6",
    "status": "success",
    "rows": 2,
    "truncated": false,
    "duration_ms": 184,
    "started_at": "2026-09-27T18:25:00Z",
    "finished_at": "2026-09-27T18:25:00.184Z"
  },
  "columns": [
    { "name": "sku", "type": "text" },
    { "name": "stock", "type": "integer" }
  ],
  "rows": [
    { "sku": "GEA-002", "stock": 3 },
    { "sku": "COF-004", "stock": 4 }
  ],
  "links": [
    {
      "name": "low-stock-alert.csv",
      "format": "csv",
      "url": "https://rowbird.example.com/r/rbl_...",
      "expires_at": "2026-10-04T18:25:00Z"
    }
  ]
}
```

- `rows` holds up to "include rows" rows of the result (0 to 1000, set per delivery). Decimals,
  large integers, dates and times are strings, so no precision is lost.
- `links` is present in `link` mode, unless the delivery turns "include links" off.
- `status` is `up`, `down` or `skipped`.
- A test sends `"event": "test"` and `"test": true`.

## Alert payload

```json
{
  "event": "alert",
  "alert": {
    "title": "Report \"Daily sales\" is failing",
    "text": "3 runs failed in a row: connection.timeout",
    "severity": "error",
    "recovered": false,
    "url": "https://rowbird.example.com/reports/..."
  }
}
```

`severity` is `info`, `warning` or `error`. The message that ends an alert has `recovered: true`.

## Verifying the signature

Compute HMAC-SHA256 over `<t>.<raw body>` with the channel's secret, compare it with `v1` in
constant time, and reject timestamps older than five minutes (replays).

::: code-group

```python [Python]
import hashlib, hmac, time

def verify(secret: bytes, header: str, body: bytes, max_age=300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t, sig = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or abs(time.time() - int(t)) > max_age:
        return False
    mac = hmac.new(secret, f"{t}.".encode() + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(mac, sig)
```

```js [Node.js]
import crypto from 'node:crypto'

export function verify(secret, header, rawBody, maxAge = 300) {
  const parts = Object.fromEntries(header.split(',').map((p) => p.split('=', 2)))
  const t = Number(parts.t)
  if (!Number.isInteger(t) || Math.abs(Date.now() / 1000 - t) > maxAge) return false
  const mac = crypto.createHmac('sha256', secret).update(`${parts.t}.`).update(rawBody).digest('hex')
  const a = Buffer.from(mac)
  const b = Buffer.from(parts.v1 ?? '')
  return a.length === b.length && crypto.timingSafeEqual(a, b)
}
```

```go [Go]
func verify(secret, header string, body []byte, maxAge time.Duration) bool {
	var t, sig string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(p, "=")
		switch k {
		case "t":
			t = v
		case "v1":
			sig = v
		}
	}
	ts, err := strconv.ParseInt(t, 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > maxAge {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t + "."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}
```

:::

Use the raw request body, before any JSON parsing, or the signature will not match.
