# kalshi-cli

`kalshi` is a JSON-first, agent-native Go CLI for Kalshi's Predictions Trade API. It exposes a small, current V2 surface through one authoritative offline registry, strict schemas, bounded execution, and explicit mutation gates.

Historical projection benchmark from `v0.1.0` on 2026-07-31 (before per-command output-contract metadata was added):

| Path | Output bytes | Output tokens | Command + output tokens | Median time |
|---|---:|---:|---:|---:|
| Raw `curl` | 6,916 | 2,193 | 2,255 | 223.2 ms |
| CLI with `--fields` | 1,362 | 451 | 494 | 224.7 ms |
| Observed reduction | **80.3%** | **79.4%** | **78.1%** | −0.7% |

## Install

Install the latest release from the public Homebrew tap on macOS or Linux:

```sh
brew install bobashopcashier/tap/kalshi-cli
kalshi --version
```

The fully qualified formula name adds the tap automatically and limits Homebrew trust to this formula. Upgrade later with:

```sh
brew upgrade bobashopcashier/tap/kalshi-cli
```

See the [Homebrew tap](https://github.com/bobashopcashier/homebrew-tap) and [releases](https://github.com/bobashopcashier/kalshi-cli/releases) for the published formula and source archives.

## MVP commands

| Command | Effect | Authentication |
|---|---|---|
| `exchange status` | read | public |
| `markets list`, `markets get` | read | public |
| `events list`, `events get` | read | public |
| `series list`, `series get` | read | public |
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
  --fields registry_version,commands.name,commands.output_contract_version,commands.summary \
  --compact

./bin/kalshi commands describe orders.create \
  --fields command.name,command.output_contract_version,command.summary,command.effect,command.params_schema \
  --compact
```

These commands make no network requests. Their schemas are the same compiled registry used for argument parsing, planning, effect metadata, and request construction.
Add `command.response_schema,command.docs_url` only when response semantics or upstream documentation are needed. To discover valid and required projection paths without returning the whole schema, select `command.response_schema.x-projectable-fields,command.response_schema.x-required-fields,command.response_schema.x-required-field-types`.

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

Series tags are exact and case-sensitive. Multiple tags are comma-separated and match either tag; spaces inside one tag are preserved:

```sh
./bin/kalshi series list \
  --environment production \
  --params '{"tags":"Fed","include_volume":true}' \
  --fields ticker,title,tags,volume_fp \
  --compact
```

Unknown keys, duplicate JSON keys, wrong types, trailing JSON, control/bidi characters, repeated flags, and supplying the same field in both forms are rejected before network access.

## Output contract

Every result uses the stable envelope `schema_version: "kalshi.agent/v1"` and names the command data contract in `output_contract_version`, for example `kalshi.output/markets.list/v1`. The same output-contract version appears on success and error envelopes. JSON is compact by default when stdout is not a TTY and pretty on a TTY. Override with `--compact`, `--pretty`, or `--ndjson`.

The offline response schema declares unconditional data paths in `x-required-fields` and their types in `x-required-field-types`. After a successful HTTP response and before projection, the CLI checks every required wrapper and field. A missing field fails atomically with exit 6, stable code `UPSTREAM_SCHEMA_MISMATCH`, and structured details that include the contract version and sorted field paths such as `missing_fields: ["markets[].ticker"]`. Null or wrong-type required values and declared top-level type changes are reported through `type_mismatches`. Additional upstream fields remain allowed.

Adding an optional field does not change an output-contract version. Removing or renaming a field, changing its type or requiredness, or changing a wrapper/collection shape requires a version bump for only the affected command.

Use `--fields` to keep model context focused:

```sh
./bin/kalshi markets list \
  --params '{"status":"open","limit":100}' \
  --fields ticker,title,close_time,yes_bid_dollars,yes_ask_dollars \
  --max-pages 2 \
  --max-items 150 \
  --compact
```

Fields are comma-separated, case-sensitive dotted member paths. For collection commands they are relative to each item; cursor-paginated commands also retain the upstream cursor and pagination metadata. For singleton reads and discovery they are relative to `data`, so a single market uses `--fields market.ticker,market.title`. Dotted traversal applies elementwise through arrays: `price_ranges.start` means `price_ranges[*].start`. Projection is local and is never sent to Kalshi.

Network-command selectors are checked before execution against the offline registry's `x-projectable-fields`; typos fail without a request. Valid optional fields that are absent from a particular response are materialized as `null`, so every non-empty projected item has the requested shape. Empty collections remain valid. The path grammar supports identifier-like JSON keys, hyphens, and `$schema`; keys containing literal dots are not projectable.

`--fields` is available for successful reads and discovery. It is rejected for mutations, dry-runs, and command help so it cannot hide a write result, plan, digest, or contract.

Cursor-paginated list commands default to one page, 100 items, and a 1 MiB final output budget. Hard flag ceilings are 10 pages, 1,000 items, and 8 MiB. Successful partial results caused by page or item ceilings set `meta.truncation.truncated`, name the reasons (`max_pages` or `max_items`), and preserve `next_cursor`. Kalshi's `series list` endpoint is a single-response collection with no cursor; use a tag or category filter to keep the upstream response within the CLI's fixed 8 MiB response cap.

The final result must fit `--max-bytes` atomically. If it does not, the CLI returns `OUTPUT_LIMIT` and no partial success or post-page cursor. Add or narrow fields, or raise the cap. This fail-closed behavior is a safety correction from the earlier successful render-time truncation contract: an opaque upstream cursor cannot safely resume midway through omitted records.

Prefer compact JSON for model consumption. For network list commands, NDJSON is an atomically buffered, line-oriented interchange format; it emits one versioned `record_type: "item"` envelope per item and a final `record_type: "summary"` envelope, with the same `output_contract_version` on every record, so its repeated metadata usually costs more tokens. Local discovery and errors emit one `record_type: "result"` record.

### Agent-context benchmark: raw `curl` versus `--fields`

A live production benchmark on 2026-07-31 using `v0.1.0` fetched the same four open `KXFED` markets. Raw `curl` returned every market field; the CLI retained only `ticker`, `title`, and `close_time`. Treat the measurements as a historical projection baseline: current envelopes also carry `output_contract_version`, so rerun the benchmark before citing current byte or token counts.



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

## Rate limits and retries

Registry-declared reads automatically retry HTTP `429` responses up to five times per command, including across all pagination pages. The delay uses bounded exponential backoff with equal jitter: nominal caps rise from 250 ms to 4 seconds, and actual fallback waits range from half to all of each cap. A valid `Retry-After` delta or HTTP date is treated as the minimum delay. Retry waits and every pagination page share the command-wide `--timeout` budget. A rate-limited attempt does not increment `pages_fetched` or consume item/page limits.

When a rate limit is encountered, `meta.retry` reports total HTTP attempts, completed retries, exhaustion, the last status, and any final `Retry-After` delay in milliseconds. Other HTTP statuses and transport errors are not automatically retried. If all rate-limit retries are exhausted, the CLI preserves the final `UPSTREAM_REJECTED` envelope with HTTP `429` and `retryable: true`. Writes are always single-attempt, including on `429`; follow their reconciliation guidance instead of blindly retrying.

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
