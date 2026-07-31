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

Recommended bases are `https://external-api.kalshi.com/trade-api/v2` for production and `https://external-api.demo.kalshi.co/trade-api/v2` for demo.

## Deliberate contract choices

- The compiled registry is authoritative. No online discovery is required to parse or plan a command.
- V2 fixed-point strings are preserved. Counts accept up to two decimals; prices accept up to six decimals. The exchange remains authoritative for each market's `price_ranges[].step`, so a syntactically valid price can still be rejected upstream.
- `orders.create` requires `client_order_id` locally. Current V2 marks it optional, but Kalshi recommends it for duplicate prevention and documents duplicate rejection.
- Orderbook authentication is contradictory: current OpenAPI/reference declares auth, while public-market guides and a harmless anonymous production probe on 2026-07-31 returned data. The CLI defaults to anonymous and offers explicit `--authenticated` signing.
- Cursor termination accepts empty, null, or absent cursors. Non-string, unsafe-Unicode, replacement-character, or over-64-KiB cursors fail as `UPSTREAM_SCHEMA_MISMATCH`; repeated non-empty cursors fail with stable `CURSOR_CYCLE` instead of looping.
- `--fields` is a local response projection. The compiled registry exposes and pre-validates `x-projectable-fields`, so invalid network-command selectors fail before execution while valid absent optional values are omitted. Projection never changes the upstream request, pagination cursor, local reconciliation filtering, or write plan digest. Collection projections retain only the projected items plus the upstream cursor.
- Historical endpoints, WebSockets, batch operations, positions/fills/settlements, amendments, decreases, order groups, and dynamic account-limit discovery are outside the MVP.

## Envelopes and effects

All output uses `kalshi.agent/v1`. Static registry effects describe potential network/auth/mutation behavior. Runtime effects distinguish actual network use and carry `mutation_status`:

- `not_applicable`: read/local command
- `not_attempted`: dry-run, policy rejection, validation failure, or definite pre-mutation rejection
- `confirmed`: a write returned success
- `unknown`: a write timed out, lost its connection, returned a server error, hit duplicate conflict, or produced an ambiguous cancel `404`

An `unknown` result is deliberately non-retryable. Reconciliation is a separate bounded read.

## Pagination and output bounds

Defaults are one page, 100 items, 1 MiB final output, 30 seconds, and an 8 MiB upstream response cap per page. Flag hard ceilings are 10 pages, 1,000 items, 8 MiB output, and 60 seconds. Projection runs before sanitization and final byte accounting. A final result that exceeds `--max-bytes` fails atomically with `OUTPUT_LIMIT`; the CLI never removes fetched items while exposing the cursor after them. If an upstream page contains more items than the requested page limit, the CLI fails with `UPSTREAM_SCHEMA_MISMATCH` instead of slicing and advancing past omitted records.

## Remaining design gaps

- No durable local intent journal yet. The caller must persist the create params and `client_order_id` before submission if crash recovery across processes is required.
- Reconciliation scans bounded `GET /portfolio/orders` pages because Kalshi exposes no direct client-ID query filter.
- There is no automatic market metadata preflight for tick-step validation; doing so would violate zero-network dry-run unless provided as explicit offline input.
- Read retries/backoff and live account-limit discovery are not implemented. This avoids accidental write retry coupling in the MVP.
- Projectable response paths cover the supported read surface, but the compact response schemas are not a lossless local copy of every upstream OpenAPI type constraint.
