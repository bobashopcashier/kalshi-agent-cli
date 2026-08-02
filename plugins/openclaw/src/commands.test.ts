import { describe, expect, it } from "vitest";

import { ALLOWED_COMMANDS, buildKalshiArgs } from "./commands.js";

describe("Kalshi command mapping", () => {
  it("excludes every mutation command", () => {
    expect(ALLOWED_COMMANDS).not.toContain("orders.create");
    expect(ALLOWED_COMMANDS).not.toContain("orders.cancel");
  });

  it("builds bounded argv without a shell string", () => {
    expect(
      buildKalshiArgs({
        command: "markets.list",
        params: { status: "open", limit: 50 },
        fields: ["ticker", "title"],
        requireFields: ["ticker"],
        environment: "production",
        maxPages: 2,
        maxItems: 100,
        maxBytes: 65536,
        timeoutSeconds: 20,
      }),
    ).toEqual([
      "markets",
      "list",
      "--params",
      '{"status":"open","limit":50}',
      "--fields",
      "ticker,title",
      "--require-fields",
      "ticker",
      "--environment",
      "production",
      "--max-pages",
      "2",
      "--max-items",
      "100",
      "--max-bytes",
      "65536",
      "--timeout",
      "20s",
      "--compact",
      "--output",
      "json",
    ]);
  });

  it("requires a typed commands.describe target", () => {
    expect(() => buildKalshiArgs({ command: "commands.describe" })).toThrow(
      "targetCommand is required",
    );
    expect(
      buildKalshiArgs({ command: "commands.describe", targetCommand: "markets.list" }),
    ).toEqual(["commands", "describe", "markets.list", "--compact", "--output", "json"]);
  });

  it("rejects positional targets on other commands", () => {
    expect(() =>
      buildKalshiArgs({ command: "markets.list", targetCommand: "orders.create" }),
    ).toThrow("targetCommand is only valid");
  });
});
