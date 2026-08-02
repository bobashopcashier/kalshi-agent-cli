# kalshi-cli

Stable, bounded JSON contracts for agents using Kalshi.

`kalshi` wraps a curated part of Kalshi's Predictions Trade API v2 with
versioned, per-command output contracts. If Kalshi removes or renames a declared
or task-required field, changes a declared JSON type or format, or breaks a
wrapper shape, the CLI fails atomically with `UPSTREAM_SCHEMA_MISMATCH` and
identifies the affected JSON path instead of letting an agent continue with a
silently malformed response.

Field projection keeps model context small. The contract makes the remaining
output predictable.

- **Versioned output:** `kalshi.agent/v1` envelopes and
  `kalshi.output/<command>/v1` command contracts.
- **Field-localized drift errors:** for example, `markets[].ticker` in
  `error.details.missing_fields`.
- **Task-required fields:** `--require-fields` turns selected optional data into
  an explicit presence contract without pretending every Kalshi field is always
  populated.
- **Bounded reads:** explicit page, item, byte, and timeout limits with
  pagination and truncation metadata.
- **Governed writes:** deny-by-default policies, reviewed confirmation digests,
  caller-stable idempotency keys, and no automatic write retries.
- **Offline discovery:** commands, parameters, effects, projectable fields, and
  response invariants come from one compiled registry.

## Install

### Current `main` with curl

The source installer requires Go 1.26 or newer. It downloads current `main`,
builds locally, and installs `kalshi` to `$HOME/.local/bin` without `sudo`:

```sh
curl -fsSL https://raw.githubusercontent.com/bobashopcashier/kalshi-cli/main/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
kalshi --version
```

Export `KALSHI_CLI_INSTALL_DIR` before running the installer to choose another
destination. The one-line form trusts mutable code on `main`. To inspect it
first:

```sh
KALSHI_INSTALLER_PATH="$(mktemp "${TMPDIR:-/tmp}/install-kalshi-cli.XXXXXX")"
trap 'rm -f "$KALSHI_INSTALLER_PATH"' EXIT
curl -fsSLo "$KALSHI_INSTALLER_PATH" \
  https://raw.githubusercontent.com/bobashopcashier/kalshi-cli/main/install.sh
less "$KALSHI_INSTALLER_PATH"
bash "$KALSHI_INSTALLER_PATH"
```

`KALSHI_CLI_VERSION` can pin a release tag once a contract-bearing release is
published. Pin the installer URL to the same tag for a repeatable install.

### Current `main` with Git

```sh
git clone https://github.com/bobashopcashier/kalshi-cli.git
cd kalshi-cli
make build
export PATH="$PWD/bin:$PATH"
kalshi --version
```

### Latest packaged release with Homebrew

```sh
brew install bobashopcashier/tap/kalshi-cli
kalshi --version
```

Packaged releases can lag `main`. The versioned output-contract work documented
below is currently on `main`; check the
[releases](https://github.com/bobashopcashier/kalshi-cli/releases) before assuming
an older package has the same contract.

## First public read

The CLI defaults to Kalshi's demo environment. Select production explicitly for
live public data:

```sh
kalshi markets list \
  --environment production \
  --status open \
  --limit 4 \
  --max-pages 1 \
  --max-items 4 \
  --fields ticker,title,close_time \
  --require-fields title,close_time \
  --compact
```

The request still goes to Kalshi's API. The CLI adds local schema validation,
projection, execution bounds, stable error codes, and explicit metadata around
the response; it is an interface layer, not a replacement for the broader API.

## Stable output contract

Known-command successes and errors name both the envelope and command contract.
Selected routing keys from a success look like:

```json
{
  "schema_version": "kalshi.agent/v1",
  "output_contract_version": "kalshi.output/markets.list/v1",
  "ok": true,
  "command": "markets.list"
}
```

After an HTTP 200 response and before field projection, the CLI checks declared
data wrappers, top-level types, required nested fields, selected field
type/format constraints, and any paths named by `--require-fields`. If an
upstream market loses its required `ticker`, the command exits 6, writes no
partial success to stdout, and returns a structured error on stderr. Selected
keys from that error look like:

```json
{
  "schema_version": "kalshi.agent/v1",
  "output_contract_version": "kalshi.output/markets.list/v1",
  "ok": false,
  "command": "markets.list",
  "error": {
    "code": "UPSTREAM_SCHEMA_MISMATCH",
    "retryable": false,
    "details": {
      "missing_fields": ["markets[].ticker"],
      "output_contract_version": "kalshi.output/markets.list/v1"
    }
  }
}
```

Wrong-type values appear in `type_mismatches`; malformed values with a declared
semantic format appear in `format_mismatches`. A nonempty declared cursor alias
without the canonical cursor appears in `unexpected_fields` while
`missing_fields` names `cursor`; the token itself is not echoed. Extra upstream
fields remain compatible. Changing the CLI's declared output shape by removing
or renaming a field, changing its type or requiredness, or changing a wrapper or
collection shape requires a version bump for the affected command. Upstream
drift instead causes the existing contract to fail closed.

This is a compatibility floor, not universal semantic validation:

- Absent optional fields selected only with `--fields` are materialized as
  `null`. Add them to `--require-fields` when the task cannot proceed without a
  non-null value.
- Same-type semantic changes are detected only for paths with a declared format
  contract. The registry currently declares RFC 3339 `date-time` validation for
  projected market `close_time` values.
- An absent or null pagination cursor is treated as end-of-pagination unless a
  declared alias such as `next_cursor` carries a nonempty continuation token.
- Version identifiers are present and stable, but the current regression matrix
  does not yet simulate a consumer rejecting an unknown future v2 contract.

## Discover contracts offline

List commands and cache the result by `registry_version`:

```sh
kalshi commands list \
  --fields registry_version,commands.name,commands.output_contract_version,commands.summary \
  --compact
```

Inspect one command before generating parameters:

```sh
kalshi commands describe markets.list \
  --fields command.name,command.output_contract_version,command.effect,command.params_schema,command.response_schema.x-projectable-fields,command.response_schema.x-required-fields,command.response_schema.x-required-field-types,command.response_schema.x-projected-field-contracts,command.response_schema.x-cursor-aliases \
  --compact
```

Discovery makes no network request and uses the same registry as parsing,
planning, validation, and request construction. See [SKILL.md](SKILL.md) for the
recommended agent workflow.

## Shape and bound reads

Pass one schema-checked parameter object:

```sh
kalshi markets list \
  --environment production \
  --params '{"status":"open","limit":100}' \
  --fields ticker,title,close_time,yes_bid_dollars,yes_ask_dollars \
  --require-fields title,close_time \
  --max-pages 2 \
  --max-items 150 \
  --max-bytes 1048576 \
  --compact
```

Convenience flags such as `--status open --limit 100` are derived from the same
schema. Unknown keys, duplicate JSON keys, wrong types, trailing JSON,
control/bidi characters, repeated flags, and supplying one parameter both ways
fail before network access.

`--fields` is local projection; it reduces model-visible JSON, not Kalshi's
network payload. Collection paths are item-relative. Singleton and discovery
paths are data-root-relative, such as
`--fields market.ticker,market.title`. Selector typos fail offline.
`--require-fields` uses the same projectable paths and requires each path to be
present and non-null in every returned record. If both flags are used, each
required path must be covered by the projection. Empty result collections remain
valid.

Cursor-paginated commands default to one page, 100 items, and a 1 MiB final
output budget. Hard flag ceilings are 10 pages, 1,000 items, and 8 MiB. Partial
results caused by page or item ceilings set `meta.truncation.truncated`, name the
reason, and retain `meta.pagination.next_cursor`. If the final result exceeds
`--max-bytes`, the CLI fails atomically with `OUTPUT_LIMIT`.

Compact JSON is best for agents. Pretty JSON is used on a TTY and can be forced
with `--pretty`. `--ndjson` is atomically buffered and useful for line-oriented
processors, but its repeated envelopes usually cost more tokens.

## Command surface

| Command | Effect | Authentication |
|---|---|---|
| `exchange status` | read | public |
| `markets list`, `markets get` | read | public |
| `events list`, `events get` | read | public |
| `series list`, `series get` | read | public |
| `orderbook get` | read | anonymous by default, optionally signed |
| `trades list` | read | public |
| `portfolio balance` | read | required |
| `orders list`, `orders get` | read | required |
| `orders reconcile` | bounded exact `client_order_id` search | required |
| `orders create` | write | required, policy and confirmation |
| `orders cancel` | write | required, policy and confirmation |

Run `kalshi commands list` for the authoritative machine-readable surface.

## Authentication

Private key material is never accepted as a CLI flag. Configure either:

```sh
export KALSHI_API_KEY_ID='your-key-id'
export KALSHI_PRIVATE_KEY_FILE='/absolute/path/to/kalshi-private-key.pem'
```

or point `KALSHI_CREDENTIALS_FILE` or `--credentials-file` at:

```json
{
  "key_id": "your-key-id",
  "private_key_file": "kalshi-private-key.pem"
}
```

Credential and key files must be regular, non-symlink files with mode `0600` or
stricter. RSA keys must be at least 2048 bits. Production and demo credentials
are not interchangeable.

## Governed writes

Writes default to `--write-policy deny`. A real order requires a reviewed
dry-run digest, an allowed environment policy, a caller-stable
`client_order_id`, and finite count and notional caps.

```sh
PARAMS='{"ticker":"EXAMPLE-26","client_order_id":"019-example-stable-id","side":"bid","count":"1.00","price":"0.250000"}'

kalshi orders create \
  --params "$PARAMS" \
  --write-policy demo-only \
  --max-order-count 2.00 \
  --max-order-notional-dollars 1.00 \
  --dry-run
```

Review the plan, then repeat the identical invocation without `--dry-run` and
add its `confirmation_digest`:

```sh
kalshi orders create \
  --params "$PARAMS" \
  --write-policy demo-only \
  --max-order-count 2.00 \
  --max-order-notional-dollars 1.00 \
  --confirm 'sha256:...'
```

Any change to the command, environment, request, effect, or policy caps changes
the digest. Production writes additionally require
`--environment production --write-policy allow`.

Writes are never automatically retried. After a timeout, connection loss,
`409`, or uncertain create result, reuse the same `client_order_id` and run:

```sh
kalshi orders reconcile \
  --client-order-id '019-example-stable-id' \
  --ticker 'EXAMPLE-26' \
  --max-pages 10 \
  --max-items 1000
```

For an ambiguous cancellation, inspect `orders get` before deciding whether to
retry.

## Reliability behavior

- Registry-declared reads retry HTTP `429` responses up to five times within the
  command timeout, honoring bounded `Retry-After` delays. `meta.retry` reports
  attempts, retries, exhaustion, and final status.
- Writes remain single-attempt, including on `429`.
- Output is buffered before emission, so schema and byte-limit failures do not
  leak partial success.
- Terminal controls, ANSI escapes, invalid UTF-8, and bidi controls are rendered
  visibly rather than executed or hidden.
- Branch on `error.code` and exit category; upstream prose is not stable.

## Benchmarks

### Schema-drift containment

The fixed regression matrix covers 30 cases over `exchange.status`,
`markets.get`, and `markets.list`. It is a conformance test, not an empirical
agent failure rate or a whole-CLI estimate.

| Arm | Compatible cases | Declared breaks detected | All breaks detected (higher is better) | Silently accepted breaks (lower is better) |
|---|---:|---:|---:|---:|
| Unvalidated 2xx JSON decoder | 10/10 | 0/16 | 0/20 | 20/20 |
| `kalshi-cli` with explicit task requirements | 10/10 | **16/16** | **20/20** | **0/20** |

The last two columns measure the same 20 injected breaking responses and are
complements: detected + silently accepted = 20. The target is therefore
**20/20 detected and 0/20 silently accepted**.

The CLI included the expected path in all 20 rejections, emitted exact v1 output
contract identifiers in all 30 cases, and withheld a valid first page when page
two violated the contract. The four former gaps are now covered by explicit
task-required paths, projected type/format contracts, and cursor-alias drift
detection. The offline registry is `kalshi.registry/v2`; per-command output
shapes remain `kalshi.output/.../v1`.

Run the matrix with:

```sh
go test ./internal/cli -run TestSchemaDriftBenchmark -count=1 -v
```

See [benchmarks/schema-drift/README.md](benchmarks/schema-drift/README.md) for
methodology and the protocol for a paired agent study.

### Historical projection baseline

On 2026-07-31, `v0.1.0` fetched the same four open `KXFED` markets through raw
API access and the CLI with `--fields ticker,title,close_time`. This predates
per-command output-contract metadata and measures context reduction, not
reliability:

| Path | Output bytes | Output tokens | Command + output tokens | Median time |
|---|---:|---:|---:|---:|
| Raw API response | 6,916 | 2,193 | 2,255 | 223.2 ms |
| CLI with `--fields` | 1,362 | 451 | 494 | 224.7 ms |
| Observed reduction | **80.3%** | **79.4%** | **78.1%** | -0.7% |

Projection happens after download, so the gain is smaller model context rather
than faster transport.

## Exit codes

| Code | Meaning |
|---:|---|
| 0 | success, including dry-run |
| 2 | usage or request-schema validation |
| 3 | write policy or confirmation |
| 4 | credentials, signing, or upstream authentication |
| 5 | network or timeout |
| 6 | upstream rejection or schema/protocol mismatch |
| 7 | output bound failure |
| 10 | internal invariant failure |

## Development

```sh
make build
make check
```

`make check` verifies formatting, runs `go vet`, the test suite, and the race
detector. See [CONTEXT.md](CONTEXT.md) for design decisions and
[SECURITY.md](SECURITY.md) for the threat model.
