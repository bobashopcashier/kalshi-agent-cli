# kalshi-agent-cli

`kalshi` is a JSON-first, agent-native Go CLI for Kalshi's Predictions Trade API. It exposes a small, current V2 surface through one authoritative offline registry, strict schemas, bounded execution, and explicit mutation gates.

| Path | Output bytes | Output tokens | Command + output tokens | Median time |
|---|---:|---:|---:|---:|
| Raw `curl` | 6,916 | 2,193 | 2,255 | 223.2 ms |
| CLI with `--fields` | 1,362 | 451 | 494 | 224.7 ms |
| Observed reduction | **80.3%** | **79.4%** | **78.1%** | −0.7% |

## Install

Install the latest release from the public Homebrew tap on macOS or Linux:

```sh
brew install bobashopcashier/tap/kalshi-agent-cli
kalshi --version
```

The fully qualified formula name adds the tap automatically and limits Homebrew trust to this formula. Upgrade later with:

```sh
brew upgrade bobashopcashier/tap/kalshi-agent-cli
```

See the [Homebrew tap](https://github.com/bobashopcashier/homebrew-tap) and [v0.1.0 release](https://github.com/bobashopcashier/kalshi-agent-cli/releases/tag/v0.1.0) for the published formula and source archive.

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

## Build from source and verify

Requires Go 1.26 or newer.

```sh
go build -o ./bin/kalshi ./cmd/kalshi
make check
```

`make check` verifies formatting, runs `go vet`, normal tests, and the race detector.

## Discover the contract offline

```sh
./bin/kalshi commands list \
  --fields registry_version,commands.name,commands.summary \
  --compact

./bin/kalshi commands describe orders.create \
  --fields command.name,command.summary,command.effect,command.params_schema \
  --compact
```

These commands make no network requests. Their schemas are the same compiled registry used for argument parsing, planning, effect metadata, and request construction.
Add `command.response_schema,command.docs_url` only when response semantics or upstream documentation are needed. To discover valid projection paths without returning the whole schema, select `command.response_schema.x-projectable-fields`.

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

Use `--fields` to keep model context focused:

```sh
./bin/kalshi markets list \
  --params '{"status":"open","limit":100}' \
  --fields ticker,title,close_time,yes_bid_dollars,yes_ask_dollars \
  --max-pages 2 \
  --max-items 150 \
  --compact
```

Fields are comma-separated, case-sensitive dotted member paths. For paginated commands they are relative to each collection item; the collection wrapper, upstream cursor, and pagination metadata are always retained. For non-paginated reads and discovery they are relative to `data`, so a single market uses `--fields market.ticker,market.title`. Dotted traversal applies elementwise through arrays: `price_ranges.start` means `price_ranges[*].start`. Projection is local and is never sent to Kalshi.

Network-command selectors are checked before execution against the offline registry's `x-projectable-fields`; typos fail without a request. Valid optional fields that are absent from a particular response are simply omitted. The path grammar supports identifier-like JSON keys, hyphens, and `$schema`; keys containing literal dots are not projectable.

`--fields` is available for successful reads and discovery. It is rejected for mutations, dry-runs, and command help so it cannot hide a write result, plan, digest, or contract.

List commands default to one page, 100 items, and a 1 MiB final output budget. Hard flag ceilings are 10 pages, 1,000 items, and 8 MiB. Successful partial results caused by page or item ceilings set `meta.truncation.truncated`, name the reasons (`max_pages` or `max_items`), and preserve `next_cursor`.

The final result must fit `--max-bytes` atomically. If it does not, the CLI returns `OUTPUT_LIMIT` and no partial success or post-page cursor. Add or narrow fields, or raise the cap. This fail-closed behavior is a safety correction from the earlier successful render-time truncation contract: an opaque upstream cursor cannot safely resume midway through omitted records.

Prefer compact JSON for model consumption. For network list commands, NDJSON is an atomically buffered, line-oriented interchange format; it emits one versioned `record_type: "item"` envelope per item and a final `record_type: "summary"` envelope, so its repeated metadata usually costs more tokens. Local discovery emits one `record_type: "result"` record.

### Agent-context benchmark: raw `curl` versus `--fields`

A live production benchmark on 2026-07-31 fetched the same four open `KXFED` markets. Raw `curl` returned every market field; the CLI retained only `ticker`, `title`, and `close_time` while preserving its versioned safety and pagination envelope.



```sh
curl -sS --fail-with-body --compressed \
  --get https://external-api.kalshi.com/trade-api/v2/markets \
  --data-urlencode status=open \
  --data-urlencode series_ticker=KXFED \
  --data-urlencode limit=4
```

```sh
./bin/kalshi markets list \
  --environment production \
  --status open \
  --series-ticker KXFED \
  --limit 4 \
  --max-items 4 \
  --fields ticker,title,close_time \
  --compact
```

The benchmark used two warmups followed by 15 serial, randomized runs per path. Token counts use the `o200k_base` tokenizer. All measured rounds returned the same projected market values.

`--fields` projects locally after downloading the response, so it reduces the JSON presented to the model rather than Kalshi's network payload. Median request speed was effectively unchanged: 223.2 ms for raw `curl` and 224.7 ms for the CLI. The gain is substantially smaller model context, not faster transport.

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
