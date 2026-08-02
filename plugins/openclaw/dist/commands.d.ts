export declare const ALLOWED_COMMANDS: readonly ["candlesticks.get", "candlesticks.historical", "commands.describe", "commands.list", "events.get", "events.list", "exchange.status", "markets.get", "markets.list", "markets.search", "orderbook.get", "orders.get", "orders.list", "orders.reconcile", "portfolio.balance", "portfolio.fills", "portfolio.pnl", "portfolio.positions", "series.get", "series.list", "trades.list"];
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
export declare function buildKalshiArgs(input: KalshiQueryInput): string[];
