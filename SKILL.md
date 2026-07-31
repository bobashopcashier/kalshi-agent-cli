---
name: kalshi-agent-cli
description: Use the local kalshi CLI for bounded Kalshi market research, portfolio reads, order reconciliation, and explicitly confirmed demo or production order operations.
---

# Kalshi agent CLI

Use the compiled `kalshi` binary or `go run ./cmd/kalshi` from this repository.

## Required workflow

1. Run `kalshi commands list --compact` for offline discovery.
2. Run `kalshi commands describe <command.name> --compact` before constructing unfamiliar parameters.
3. Prefer one strict `--params` JSON object for generated calls. Convenience flags are safe but must not duplicate fields in `--params`.
4. Set explicit `--max-pages`, `--max-items`, and `--max-bytes` when the defaults do not fit the task. Never emulate an unbounded `--all` loop.
5. Parse `schema_version`, `ok`, stable `error.code`, `effect`, and `meta.truncation`. Do not scrape prose.
6. Treat a non-empty `meta.pagination.next_cursor` plus truncation reasons as an explicit continuation decision.

## Authentication

Never place an API private key in argv, `--params`, output, logs, or prompts. Configure `KALSHI_CREDENTIALS_FILE`, or configure `KALSHI_API_KEY_ID` plus `KALSHI_PRIVATE_KEY_FILE`. Credential and PEM files must be mode `0600` or stricter.

`orderbook.get` is anonymous by default because current Kalshi public-data guidance and observed behavior allow it, despite the current endpoint schema also declaring authentication. If the server requires authentication, repeat with `--authenticated` and a configured credential source.

## Writes

Never call `orders.create` or `orders.cancel` directly on first construction.

1. Supply an explicit environment and a write policy. Prefer demo unless the operator explicitly requested production.
2. For create, supply a caller-stable `client_order_id`, count/notional policy caps, and complete params.
3. Add `--dry-run`. This performs no credential loading, DNS, HTTP, or mutation.
4. Review the canonical plan and its effect/policy metadata.
5. Repeat the identical command without `--dry-run` and pass the exact `confirmation_digest` to `--confirm`.
6. If a write returns `mutation_status: "unknown"`, do not blindly retry and do not switch client IDs. Run `orders.reconcile` for creates or `orders.get` for cancels.

`--write-policy demo-only` permits demo writes; `allow` is necessary for production. The default is `deny`. CLI flags may state finite count and notional caps; they are part of the confirmation digest.

## Output

Use JSON for single results and NDJSON for streaming collection consumption. NDJSON's final summary record contains authoritative pagination/truncation metadata. Strings are sanitized so C0/C1 terminal controls, ANSI escapes, invalid UTF-8, and bidi controls appear as visible Unicode escape text.

Branch on stable error codes and exit categories documented in `README.md`. A network error is retryable only for reads. Writes are never automatically retried.
