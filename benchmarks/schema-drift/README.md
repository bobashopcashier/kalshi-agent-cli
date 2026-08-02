# Schema-drift containment benchmark

This fixed regression matrix measures whether an interface turns injected
upstream response drift into a correct result, an explicit failure, or a
silently wrong success. It currently covers `exchange.status`, `markets.get`,
and `markets.list`; it is not an estimate of Kalshi's production reliability,
an empirical agent failure rate, or the whole CLI's drift-detection rate.

Run it from the repository root:

```sh
go test ./internal/cli -run TestSchemaDriftBenchmark -count=1 -v
```

The test in `internal/cli/schema_drift_benchmark_test.go` sends the same frozen
HTTP 200 fixtures through three arms:

1. `direct-api-json`: the CLI's low-level API transport/parser accepting any
   syntactically valid HTTP 200 JSON object, with no response-contract checks.
2. `direct-api-task-validator`: the same decoded JSON plus a handwritten,
   oracle-equivalent structural validator with scenario knowledge.
3. `kalshi-cli`: the command's compiled response contract and versioned output
   envelope.

The matrix includes compatible changes, declared contract breaks, and known
coverage gaps. Its primary unsafe outcome is `silent_wrong_success`: an arm
accepts a response that fails the structural task oracle. An explicit schema
error is a safe detection, not a completed task. Path-presence accuracy checks
that an error includes the expected JSON path. A two-page case verifies that a
valid first page followed by a schema-broken second page produces no partial
stdout. Every CLI result must use the exact expected `kalshi.agent/v1` envelope
schema identifier and per-command `kalshi.output/.../v1` contract identifier.

The test logs every scenario-arm outcome and finishes with a
`SCHEMA_DRIFT_BENCHMARK_JSON=` summary for machine extraction. Repeating it
proves determinism; it does not create independent samples or justify a
confidence interval.

## Interpreting comparisons

`direct-api-json` is an unvalidated 2xx JSON decoder, not `curl`, an independent
network stack, or an agent. It deliberately shares the CLI's transport/parser
to isolate the response-contract layer. `direct-api-task-validator` is the
anti-strawman oracle baseline, not a generic API client. The CLI's value is
making its declared checks discoverable, versioned, and reusable by default.

The matrix is hand-enumerated around the three commands' currently declared
rules. Its contract result is a regression/conformance pass fraction, not an
independently sampled population rate. Expand it mechanically from the registry
before making a CLI-wide claim.

The current matrix verifies stable, exact v1 schema/contract/command identifiers
but does not simulate a v1-to-v2 transition or a consumer rejecting an unknown
contract version. Add that consumer-compatibility case before claiming a measured
benefit from version negotiation itself.

The known-gap cases deliberately probe beyond the current declared contract:
an optional selected field disappears, an optional selected field changes
type, a continuation cursor is renamed where another page is known to exist,
and a timestamp retains its JSON string type but loses RFC 3339 semantics.
Keep them in the matrix so improvements reduce silent failures rather than
quietly narrowing the benchmark.

## From containment to agent failure rate

To measure agents, freeze the model/version, prompt, tool budget, fixtures, and
scorer; run matched isolated task/fixture triplets against CLI, neutral raw HTTP,
and raw HTTP plus a documented validator; and randomize arm order. Score
`correct_success`, `detected_failure`, `silent_wrong_success`, and
`timeout_or_other` from artifacts rather than model self-report.

Predeclare silent incorrect completion as the primary binary endpoint. Use a
calibration run to estimate paired discordance, then choose sample size from a
power analysis for the smallest useful risk reduction. For three matched arms,
use an overall Cochran Q test or predeclared pairwise exact McNemar tests with a
multiple-comparison correction, plus a scenario-cluster bootstrap. Report task
success and safe resolution separately, along with field localization, false
alarms, recovery turns, tool calls, HTTP attempts, latency, bytes, and tokens.
Only that stochastic study supports a claim about agent failure rates.
