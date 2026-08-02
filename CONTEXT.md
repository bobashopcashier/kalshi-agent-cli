# Kalshi API and CLI context

Contract reviewed against Kalshi's current official OpenAPI through Context7 on 2026-07-31, with the official Kalshi sources below retained as primary references.

## Official sources

- API index: <https://docs.kalshi.com/llms.txt>
- OpenAPI: <https://docs.kalshi.com/openapi.yaml>
- Environments: <https://docs.kalshi.com/getting_started/api_environments>
- Authentication: <https://docs.kalshi.com/getting_started/api_keys>
- Pagination: <https://docs.kalshi.com/getting_started/pagination>
- Rate limits: <https://docs.kalshi.com/getting_started/rate_limits>
- V2 create: <https://docs.kalshi.com/api-reference/orders/create-order-v2>
- V2 cancel: <https://docs.kalshi.com/api-reference/orders/cancel-order-v2>
- Positions: <https://docs.kalshi.com/api-reference/portfolio/get-positions>
- Fills: <https://docs.kalshi.com/api-reference/portfolio/get-fills>
- Candlesticks: <https://docs.kalshi.com/api-reference/market/get-market-candlesticks>
- Historical candlesticks: <https://docs.kalshi.com/api-reference/historical/get-historical-market-candlesticks>

Recommended bases are `https://external-api.kalshi.com/trade-api/v2` for production and `https://external-api.demo.kalshi.co/trade-api/v2` for demo.

## Deliberate contract choices

- The compiled registry is authoritative. No online discovery is required to parse or plan a command.
- V2 fixed-point strings are preserved. Request counts accept up to two decimals; current responses are validated as exactly two decimals, while dollar strings permit up to six. The exchange remains authoritative for each market's `price_ranges[].step`, so a syntactically valid price can still be rejected upstream.
- `orders.create` requires `client_order_id` locally. Current V2 marks it optional, but Kalshi recommends it for duplicate prevention and documents duplicate rejection.
- Orderbook authentication is contradictory: current OpenAPI/reference declares auth, while public-market guides and a harmless anonymous production probe on 2026-07-31 returned data. The CLI defaults to anonymous and offers explicit `--authenticated` signing.
- Cursor termination accepts empty, null, or absent cursors unless a command contract declares cursor presence. `portfolio.fills` does because its current response schema requires `cursor`; a missing or null value names that path in `UPSTREAM_SCHEMA_MISMATCH`. If a declared alias such as `next_cursor` contains a nonempty token while canonical `cursor` is terminal, the CLI reports both the missing and unexpected paths without echoing the token. Non-string, unsafe-Unicode, replacement-character, or over-64-KiB cursors fail as `UPSTREAM_SCHEMA_MISMATCH`; repeated non-empty cursors fail with stable `CURSOR_CYCLE` instead of looping.
- `--fields` is a local response projection. The compiled registry exposes and pre-validates `x-projectable-fields`, so invalid network-command selectors fail before execution while valid absent optional values are materialized as `null`. `--require-fields` lets a task require a projectable path to be present and non-null in every record without globally promoting legitimately optional fields. Projection never changes the upstream request, pagination cursor, local reconciliation filtering, or write plan digest. Collection projections retain only the projected items plus the upstream cursor.
- `markets.search` does not call the undocumented legacy search service. It scans bounded official `/markets` pages and matches stable required identity and side-title strings locally, preserving upstream order and exposing scan completeness through pagination and truncation metadata.
- `portfolio.positions` intentionally returns only `market_positions`. Kalshi paginates `market_positions` and `event_positions` behind one cursor, while the CLI pager exposes one collection; returning page-one event aggregates beside fully paginated market positions would be silently incomplete. `portfolio.pnl` is a canonical projection of upstream realized P&L, exposure, and fees, not a calculated unrealized or net value.
- Commands with `x-default-fields` emit a canonical narrow view unless an explicit `--fields` overrides it. This keeps legacy aliases and unrelated wrapper fields out of versioned output without preventing deliberate projection.
- Candlestick ranges are rejected before network access when their maximum possible point count exceeds `--max-items`; returned cardinality and timestamps are checked against the same requested window. Live-tier and archived candles remain distinct commands because their official response field names and nullable price contracts differ.
- WebSockets, batch operations, event-position aggregation, settlements, amendments, decreases, order groups, and dynamic account-limit discovery remain outside the supported surface.

## Envelopes and effects

All output uses the `kalshi.agent/v1` envelope and a per-command `output_contract_version` such as `kalshi.output/markets.list/v1`. The offline discovery surface is versioned independently as `kalshi.registry/v4`. Each registry command exposes its output version alongside the response schema, top-level properties, projection paths, unconditional `x-required-fields`, `x-required-field-types`, selected-path type/format/enum contracts, cursor aliases, local-match behavior, and canonical default fields where applicable. Successful HTTP responses are checked before projection and sanitization; missing, null, wrong-type, malformed-format, invalid-enum, and recognized cursor-rename drift fails atomically as `UPSTREAM_SCHEMA_MISMATCH` with structured `missing_fields`, `type_mismatches`, `format_mismatches`, `value_mismatches`, or `unexpected_fields` details. Additional optional fields are tolerated. Removal, rename, type, requiredness, or structural changes bump only the affected command contract; additive optional fields do not.

Static registry effects describe potential network/auth/mutation behavior. Runtime effects distinguish actual network use and carry `mutation_status`:

- `not_applicable`: read/local command
- `not_attempted`: dry-run, policy rejection, validation failure, or definite pre-mutation rejection
- `confirmed`: a write returned success
- `unknown`: a write timed out, lost its connection, returned a server error, hit duplicate conflict, or produced an ambiguous cancel `404`

An `unknown` result is deliberately non-retryable. Reconciliation is a separate bounded read.

## Pagination and output bounds

Defaults are one page, 100 items, 1 MiB final output, 30 seconds, and an 8 MiB upstream response cap per page. Flag hard ceilings are 10 pages, 1,000 items, 8 MiB output, and 60 seconds. Projection runs before sanitization and final byte accounting. A final result that exceeds `--max-bytes` fails atomically with `OUTPUT_LIMIT`; the CLI never removes fetched items while exposing the cursor after them. If an upstream page contains more items than the requested page limit, the CLI fails with `UPSTREAM_SCHEMA_MISMATCH` instead of slicing and advancing past omitted records.

Registry-declared reads retry HTTP `429` up to five times per command, including across all pagination pages, with bounded equal-jitter exponential backoff. A valid `Retry-After` value is a minimum delay, and the existing command timeout bounds requests plus waits across all pages. Rate-limited attempts do not advance pagination counters. `meta.retry` exposes aggregate attempts/retries, exhaustion, the last rate-limit status, and a final server delay when present. Mutations, other HTTP statuses, and transport errors remain single-attempt; exhausted read rate limits retain the final retryable `429` error.

## Remaining design gaps

- No durable local intent journal yet. The caller must persist the create params and `client_order_id` before submission if crash recovery across processes is required.
- Reconciliation scans bounded `GET /portfolio/orders` pages because Kalshi exposes no direct client-ID query filter.
- There is no automatic market metadata preflight for tick-step validation; doing so would violate zero-network dry-run unless provided as explicit offline input.
- Cross-process rate-limit coordination and live account-limit discovery are not implemented. Read `429` retries are deliberately per process and remain separated from mutation execution.
- Projectable response paths cover the supported read surface, and required identity paths plus explicit task requirements protect collection items from silent structural drift. Projected type/format constraints are currently sparse rather than a lossless local copy of every upstream OpenAPI constraint.
