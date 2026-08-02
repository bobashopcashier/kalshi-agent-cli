# kalshi-cli

Stable, bounded JSON for agents using Kalshi.

`kalshi` wraps key Trade API v2 endpoints with versioned output contracts. If Kalshi changes a required field, type, format, or response shape, the CLI fails atomically with `UPSTREAM_SCHEMA_MISMATCH` and names the affected JSON path.

- Versioned, predictable output
- Task-specific required fields
- Compact field projection
- Bounded pagination and output
- Governed, deny-by-default writes
- Offline command and schema discovery

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
