# Security model

The CLI assumes API responses, market text, cursors, command arguments, environment variables, and local file paths may be hostile.

Implemented controls:

- strict JSON object decoding with duplicate-key, trailing-value, unknown-key, type, range, enum, pattern, control, and bidi rejection;
- schema-derived convenience flags with disjoint merge semantics;
- path identifiers that reject query fragments, slashes, percent escapes, traversal, controls, and bidi overrides;
- no private key or raw API secret flag;
- owner-only, regular, non-symlink credential files with open/stat identity checks and size caps;
- RSA-PSS/SHA-256 request signing with query strings excluded exactly as Kalshi specifies;
- redirects disabled in the default HTTP client so auth headers cannot be forwarded to another origin;
- upstream response caps before JSON decoding;
- type, Unicode-safety, cycle, and 64 KiB bounds on upstream pagination cursors before reuse or metadata emission;
- local response projection before sanitization and byte accounting, with strict bounded field-path syntax;
- atomic output caps that fail closed rather than dropping fetched records while exposing a later cursor;
- recursive terminal/C0/C1/ANSI/bidi sanitization of upstream data and error details;
- policy evaluation and digest confirmation before credential loading, with confirmation digests emitted only by explicit dry-runs;
- an architectural dry-run branch before credentials, DNS, HTTP, or mutation;
- bounded, context-aware HTTP `429` retries only for registry-declared reads;
- no generic write retries, and explicit unknown-outcome reconciliation guidance.

The CLI does not protect a compromised host, malicious Go toolchain, or a process with permission to read the user's credential files. Operators should use scoped demo keys during development, rotate exposed keys, and prefer narrow read scopes where Kalshi supports them.

Report security issues privately to the repository owner. Do not include credentials, private keys, signatures, or live order IDs in reports.
