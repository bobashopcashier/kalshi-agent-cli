export const ALLOWED_COMMANDS = [
  "candlesticks.get",
  "candlesticks.historical",
  "commands.describe",
  "commands.list",
  "events.get",
  "events.list",
  "exchange.status",
  "markets.get",
  "markets.list",
  "markets.search",
  "orderbook.get",
  "orders.get",
  "orders.list",
  "orders.reconcile",
  "portfolio.balance",
  "portfolio.fills",
  "portfolio.pnl",
  "portfolio.positions",
  "series.get",
  "series.list",
  "trades.list",
] as const;

export type AllowedCommand = (typeof ALLOWED_COMMANDS)[number];

export type KalshiQueryInput = {
  command: AllowedCommand;
  targetCommand?: string;
  params?: Record<string, unknown>;
  fields?: string[];
  requireFields?: string[];
  environment?: "demo" | "production";
  authenticated?: boolean;
  maxPages?: number;
  maxItems?: number;
  maxBytes?: number;
  timeoutSeconds?: number;
};

const COMMAND_NAME = /^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$/;
const MAX_PARAMS_BYTES = 65_536;

function appendListFlag(argv: string[], name: string, values: string[] | undefined): void {
  if (values && values.length > 0) {
    argv.push(name, values.join(","));
  }
}
export function buildKalshiArgs(input: KalshiQueryInput): string[] {
  const [group, action] = input.command.split(".");
  const argv = [group, action];

  if (input.command === "commands.describe") {
    if (!input.targetCommand || !COMMAND_NAME.test(input.targetCommand)) {
      throw new Error("targetCommand is required for commands.describe");
    }
    argv.push(input.targetCommand);
  } else if (input.targetCommand !== undefined) {
    throw new Error("targetCommand is only valid for commands.describe");
  }

  if (input.params !== undefined) {
    const encoded = JSON.stringify(input.params);
    if (Buffer.byteLength(encoded, "utf8") > MAX_PARAMS_BYTES) {
      throw new Error(`params exceeds ${MAX_PARAMS_BYTES} UTF-8 bytes`);
    }
    argv.push("--params", encoded);
  }

  appendListFlag(argv, "--fields", input.fields);
  appendListFlag(argv, "--require-fields", input.requireFields);

  if (input.environment) {
    argv.push("--environment", input.environment);
  }
  if (input.authenticated) {
    argv.push("--authenticated");
  }
  if (input.maxPages !== undefined) {
    argv.push("--max-pages", String(input.maxPages));
  }
  if (input.maxItems !== undefined) {
    argv.push("--max-items", String(input.maxItems));
  }
  if (input.maxBytes !== undefined) {
    argv.push("--max-bytes", String(input.maxBytes));
  }
  if (input.timeoutSeconds !== undefined) {
    argv.push("--timeout", `${input.timeoutSeconds}s`);
  }

  argv.push("--compact", "--output", "json");
  return argv;
}
