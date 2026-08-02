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
HTTP 200 fixtures through two arms:

1. `direct-api-json`: the CLI's low-level API transport/parser accepting any
   syntactically valid HTTP 200 JSON object, with no response-contract checks.
2. `kalshi-cli`: the command's compiled response contract, explicit
   `--require-fields` task requirements where applicable, and versioned output
   envelope.

The matrix includes compatible changes, declared contract breaks, and four
extended cases that were previously known coverage gaps. Its primary unsafe
outcome is `silent_wrong_success`: an arm
accepts a response that fails the fixed task-correctness rules. Those rules are
used only to score results; they are not exposed as another benchmark arm. An
explicit schema error is a safe detection, not a completed task. This metric is a count of
breaking cases, so **0/20 is the desired result**; it complements 20/20 explicit
break detections. Path-presence accuracy checks
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
to isolate the response-contract layer. The CLI arm adds its discoverable,
versioned response-contract behavior.

The matrix is hand-enumerated around the three commands' currently declared
rules. Its contract result is a regression/conformance pass fraction, not an
independently sampled population rate. Expand it mechanically from the registry
before making a CLI-wide claim.

The current matrix verifies stable, exact v1 schema/contract/command identifiers
but does not simulate a v1-to-v2 transition or a consumer rejecting an unknown
contract version. Add that consumer-compatibility case before claiming a measured
benefit from version negotiation itself.

The historically named known-gap cases probe an optional task-required field
disappearing, an optional selected field changing type, a continuation cursor
being renamed where another page is known to exist, and a timestamp retaining
its JSON string type while losing RFC 3339 semantics. They now must be contained
by `--require-fields`, discoverable projected-field contracts, and cursor-alias
detection. The regression assertion requires 4/4 detections and zero silent
wrong successes so future changes cannot quietly reopen the gaps.

## From containment to agent failure rate

To measure agents, freeze the model/version, prompt, tool budget, fixtures, and
scorer; run matched isolated task/fixture pairs against CLI and neutral raw HTTP,
and randomize arm order. Score
`correct_success`, `detected_failure`, `silent_wrong_success`, and
`timeout_or_other` from artifacts rather than model self-report.

Predeclare silent incorrect completion as the primary binary endpoint. Use a
calibration run to estimate paired discordance, then choose sample size from a
power analysis for the smallest useful risk reduction. Use an exact McNemar test
for the paired comparison plus a scenario-cluster bootstrap. Report task
success and safe resolution separately, along with field localization, false
alarms, recovery turns, tool calls, HTTP attempts, latency, bytes, and tokens.
Only that stochastic study supports a claim about agent failure rates.
