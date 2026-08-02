import { describe, expect, it } from "vitest";

import { processOutcomeToEnvelope } from "./runner.js";

describe("Kalshi process results", () => {
  it("preserves a successful CLI envelope", () => {
    const envelope = {
      schema_version: "kalshi.agent/v1",
      output_contract_version: "kalshi.output/markets.list/v1",
      ok: true,
      command: "markets.list",
    };
    expect(
      processOutcomeToEnvelope(
        { error: null, stdout: JSON.stringify(envelope), stderr: "" },
        "markets.list",
      ),
    ).toEqual(envelope);
  });

  it("preserves structured CLI errors from stderr", () => {
    const envelope = {
      schema_version: "kalshi.agent/v1",
      ok: false,
      command: "markets.list",
      error: { code: "UPSTREAM_SCHEMA_MISMATCH" },
    };
    expect(
      processOutcomeToEnvelope(
        {
          error: Object.assign(new Error("exit 4"), { code: 4 }),
          stdout: "",
          stderr: JSON.stringify(envelope),
        },
        "markets.list",
      ),
    ).toEqual(envelope);
  });

  it("returns a stable missing-binary error", () => {
    const result = processOutcomeToEnvelope(
      {
        error: Object.assign(new Error("missing"), { code: "ENOENT" }),
        stdout: "",
        stderr: "",
      },
      "exchange.status",
    );
    expect(result).toMatchObject({
      schema_version: "kalshi.openclaw/v1",
      ok: false,
      command: "exchange.status",
      error: { code: "CLI_NOT_FOUND" },
    });
  });

  it("does not expose malformed child output", () => {
    const result = processOutcomeToEnvelope(
      {
        error: Object.assign(new Error("exit 1"), { code: 1 }),
        stdout: "not json",
        stderr: "private internal detail",
      },
      "markets.list",
    );
    expect(result).toMatchObject({
      ok: false,
      error: { code: "INVALID_CLI_OUTPUT" },
    });
    expect(JSON.stringify(result)).not.toContain("private internal detail");
  });
});
