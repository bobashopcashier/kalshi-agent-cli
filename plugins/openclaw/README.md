# Kalshi CLI for OpenClaw

This plugin exposes the schema-safe `kalshi` CLI as one optional, read-only
OpenClaw tool named `kalshi_query`. It preserves the CLI's versioned JSON
envelopes, bounded pagination metadata, and field-localized
`UPSTREAM_SCHEMA_MISMATCH` errors.

## Requirements

- OpenClaw 2026.7.1-2 or newer.
- The `kalshi` executable on `PATH`, or an explicit `binaryPath` in the plugin
  configuration.

Install the CLI with Homebrew:

```sh
brew install bobashopcashier/tap/kalshi-cli
```

Or with Go 1.26 or newer:

```sh
go install github.com/bobashopcashier/kalshi-cli/cmd/kalshi@latest
```

## Install the plugin

```sh
openclaw plugins install clawhub:@bobashopcashier/kalshi-cli
```

The tool is optional because it launches a local executable. Allow it in the
OpenClaw tool policy:

```json5
{
  tools: {
    allow: ["kalshi_query"]
  }
}
```

The first plugin release intentionally excludes `orders.create` and
`orders.cancel`. Use the CLI's dry-run, confirmation digest, write policy,
idempotency, and reconciliation flow for trading operations.

Authenticated reads inherit the CLI's standard environment configuration:
`KALSHI_CREDENTIALS_FILE`, or `KALSHI_API_KEY_ID` plus
`KALSHI_PRIVATE_KEY_FILE`. Never put private-key material in tool parameters or
plugin configuration.

## Development

```sh
npm install
npm run plugin:validate
npm test
npm pack --dry-run
clawhub package validate .
clawhub package publish . --family code-plugin --owner bobashopcashier --dry-run
```
