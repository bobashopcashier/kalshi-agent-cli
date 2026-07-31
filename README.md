# kalshi-agent-cli

`kalshi` is a JSON-first, agent-native Go CLI for Kalshi's Predictions Trade API. It exposes a small, current V2 surface through one authoritative offline registry, strict schemas, bounded execution, and explicit mutation gates.

This repository is an independent implementation. It is not affiliated with or endorsed by Kalshi.

## MVP commands

| Command | Effect | Authentication |
|---|---|---|
| `exchange status` | read | public |
| `markets list`, `markets get` | read | public |
| `events list`, `events get` | read | public |
| `orderbook get` | read | anonymous by default, optional signed mode |
| `trades list` | read | public |
| `portfolio balance` | read | required |
| `orders list`, `orders get` | read | required |
| `orders reconcile` | bounded read and exact `client_order_id` match | required |
| `orders create` | write | required, policy + digest confirmation |
| `orders cancel` | write | required, policy + digest confirmation |

The write commands use the current V2 paths:

- `POST /portfolio/events/orders`
- `DELETE /portfolio/events/orders/{order_id}`

No command automatically retries a write.

## Build and verify

Requires Go 1.26 or newer.

```sh
go build -o ./bin/kalshi ./cmd/kalshi
make check
```

`make check` verifies formatting, runs `go vet`, normal tests, and the race detector.

## Discover the contract offline

```sh
./bin/kalshi commands list --compact
./bin/kalshi commands describe orders.create --pretty
```

These commands make no network requests. Their schemas are the same compiled registry used for argument parsing, planning, effect metadata, and request construction.

## Strict parameters and convenience flags

Pass one schema-checked JSON object:

```sh
./bin/kalshi markets list \
  --params '{"status":"open","limit":100}' \
  --max-pages 2 \
  --max-items 150
```

Or use convenience flags derived from the same schema:

```sh
./bin/kalshi markets list --status open --limit 100
```

Unknown keys, duplicate JSON keys, wrong types, trailing JSON, control/bidi characters, repeated flags, and supplying the same field in both forms are rejected before network access.

## Output contract

Every result uses `schema_version: "kalshi.agent/v1"`. JSON is compact by default when stdout is not a TTY and pretty on a TTY. Override with `--compact`, `--pretty`, or `--ndjson`.

List commands default to one page, 100 items, and a 1 MiB final output budget. Hard flag ceilings are 10 pages, 1,000 items, and 8 MiB. Successful partial results set `meta.truncation.truncated`, name the reasons (`max_pages`, `max_items`, or `max_bytes`), and preserve `next_cursor`.

NDJSON emits one versioned `record_type: "item"` envelope per item and always reserves room for a final `record_type: "summary"` envelope.

## Credentials

Private key material is never accepted as a CLI flag. Use either:

```sh
export KALSHI_API_KEY_ID='your-key-id'
export KALSHI_PRIVATE_KEY_FILE='/absolute/path/to/kalshi-private-key.pem'
```

or a credentials file referenced by `KALSHI_CREDENTIALS_FILE` or `--credentials-file`:

```json
{
  "key_id": "your-key-id",
  "private_key_file": "kalshi-private-key.pem"
}
```

The private key path may be relative to the credentials file. Both files must be regular, non-symlink files with mode `0600` or stricter and are capped at 64 KiB. RSA keys must be at least 2048 bits. Signed requests use RSA-PSS/SHA-256 over `timestamp + METHOD + full path`, excluding the query string.

Production and demo credentials are not interchangeable. The CLI defaults to Kalshi's recommended demo REST base URL. Select production reads with `--environment production`.

## Write workflow

Writes default to `--write-policy deny`. A real order needs a reviewed dry-run digest, an allowed environment policy, and finite count/notional caps.

```sh
PARAMS='{"ticker":"EXAMPLE-26","client_order_id":"019-example-stable-id","side":"bid","count":"1.00","price":"0.250000"}'

./bin/kalshi orders create \
  --params "$PARAMS" \
  --write-policy demo-only \
  --max-order-count 2.00 \
  --max-order-notional-dollars 1.00 \
  --dry-run
```

Review the emitted plan, then repeat the identical invocation without `--dry-run` and add its `confirmation_digest`:

```sh
./bin/kalshi orders create \
  --params "$PARAMS" \
  --write-policy demo-only \
  --max-order-count 2.00 \
  --max-order-notional-dollars 1.00 \
  --confirm 'sha256:...'
```

Any change to command, environment, path, query, body, effect, or policy caps changes the digest. Production writes additionally require explicit `--environment production --write-policy allow`.

`client_order_id` is mandatory at the CLI layer even though the current V2 API marks it optional. After a timeout, connection loss, `409`, or uncertain create result, do not invent a new ID. Reconcile first:

```sh
./bin/kalshi orders reconcile \
  --client-order-id '019-example-stable-id' \
  --ticker 'EXAMPLE-26' \
  --max-pages 10 \
  --max-items 1000
```

Cancel ambiguity is not treated as success. Fetch the order and inspect its status before deciding whether another cancellation attempt is appropriate.

## Exit codes

| Code | Meaning |
|---:|---|
| 0 | success, including dry-run |
| 2 | usage or schema validation |
| 3 | write policy or confirmation |
| 4 | credentials, signing, or upstream authentication |
| 5 | network or timeout |
| 6 | upstream rejection or schema/protocol mismatch |
| 7 | output bound failure |
| 10 | internal invariant failure |

String error codes in the versioned error envelope are the stable branch keys; upstream message text is not stable.

See [CONTEXT.md](CONTEXT.md) for API decisions and [SECURITY.md](SECURITY.md) for the threat model.
