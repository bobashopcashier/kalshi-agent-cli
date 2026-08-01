---
name: kalshi-cli
description: Use the local kalshi CLI for bounded Kalshi market research, portfolio reads, order reconciliation, and explicitly confirmed demo or production order operations.
---

# Kalshi CLI

Use the compiled `kalshi` binary or `go run ./cmd/kalshi` from this repository.

## Required workflow

1. If the command is not already known from the same cached registry/binary version, run `kalshi commands list --fields registry_version,commands.name,commands.summary --compact` once and cache the result by `registry_version` for the task.
2. Before constructing unfamiliar parameters, run `kalshi commands describe <command.name> --fields command.name,command.summary,command.effect,command.params_schema --compact`. Add `command.response_schema,command.docs_url` only when response semantics or upstream documentation are needed.
3. Prefer one strict `--params` JSON object for generated calls. Convenience flags are safe but must not duplicate fields in `--params`.
4. Add the narrowest useful `--fields` selection to reads. Paths are item-relative for paginated collections and data-root-relative otherwise. Discover allowed paths offline with `kalshi commands describe <command.name> --fields command.response_schema.x-projectable-fields --compact`.
5. Set explicit `--max-pages`, `--max-items`, and `--max-bytes` when the defaults do not fit the task. Never emulate an unbounded `--all` loop.
6. Parse `schema_version`, `ok`, stable `error.code`, `effect`, and `meta.truncation`. Do not scrape prose.
7. Treat a non-empty `meta.pagination.next_cursor` plus truncation reasons as an explicit continuation decision.

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

Prefer compact JSON for model consumption. Use NDJSON only for external line-oriented processors; it is atomically buffered and repeats the envelope for each collection item, so it is usually more token-expensive. NDJSON's final summary record contains authoritative pagination/truncation metadata. Strings are sanitized so C0/C1 terminal controls, ANSI escapes, invalid UTF-8, and bidi controls appear as visible Unicode escape text.

Projection happens before sanitization and byte accounting. If the result still exceeds `--max-bytes`, the command fails atomically with `OUTPUT_LIMIT`; add or narrow fields, or increase the cap. Never treat an output-limit error as a partial page or continue from a cursor copied from error text.

Branch on stable error codes and exit categories documented in `README.md`. A network error is retryable only for reads. Writes are never automatically retried.
