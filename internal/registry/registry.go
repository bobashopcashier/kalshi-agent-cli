package registry

import "sort"

const (
	Version              = "kalshi.registry/v4"
	commandSchemaVersion = "kalshi.command/v1"
	tickerPattern        = `^[A-Z0-9][A-Z0-9._-]{0,127}$`
	idPattern            = `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`
	countPattern         = `^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`
	pricePattern         = `^(0|[1-9][0-9]*)(\.[0-9]{1,6})?$`
)

var outputContractRevisions = map[string]string{
	"candlesticks.get":        "v1",
	"candlesticks.historical": "v1",
	"events.get":              "v1",
	"events.list":             "v1",
	"exchange.status":         "v1",
	"markets.get":             "v1",
	"markets.list":            "v1",
	"markets.search":          "v1",
	"orderbook.get":           "v1",
	"orders.cancel":           "v1",
	"orders.create":           "v1",
	"orders.get":              "v1",
	"orders.list":             "v1",
	"orders.reconcile":        "v1",
	"portfolio.balance":       "v1",
	"portfolio.fills":         "v1",
	"portfolio.pnl":           "v1",
	"portfolio.positions":     "v1",
	"series.get":              "v1",
	"series.list":             "v1",
	"trades.list":             "v1",
}

func withOutputContractVersion(command Command) Command {
	if revision := outputContractRevisions[command.Name]; revision != "" {
		command.OutputContractVersion = "kalshi.output/" + command.Name + "/" + revision
	}
	return command
}

var commands = []Command{
	{
		SchemaVersion: commandSchemaVersion, Name: "exchange.status", CLIPath: []string{"exchange", "status"},
		Summary: "Get current exchange and trading status.", Method: "GET", Path: "/exchange/status",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{}),
		ResponseSchema: withProjectable(responseObject(map[string]ResponseField{"exchange_active": responseField("boolean", "Whether the exchange accepts state changes."), "trading_active": responseField("boolean", "Whether trading is active.")}, "exchange_active", "trading_active"), "exchange_active", "trading_active"), DocsURL: "https://docs.kalshi.com/api-reference/exchange/get-exchange-status",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "markets.list", CLIPath: []string{"markets", "list"},
		Summary: "List live-tier markets with bounded cursor pagination.", Method: "GET", Path: "/markets",
		Effect: Effect{Class: "read", Network: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"limit":          integer("query", "Results per upstream page.", 1, 1000),
			"cursor":         str("query", "Opaque pagination cursor."),
			"event_ticker":   withPattern(str("query", "Filter by one event ticker."), tickerPattern),
			"series_ticker":  withPattern(str("query", "Filter by series ticker."), tickerPattern),
			"min_created_ts": integer("query", "Minimum creation Unix timestamp in seconds.", 0, 4102444800),
			"max_created_ts": integer("query", "Maximum creation Unix timestamp in seconds.", 0, 4102444800),
			"min_updated_ts": integer("query", "Minimum metadata update Unix timestamp in seconds.", 0, 4102444800),
			"min_close_ts":   integer("query", "Minimum close Unix timestamp in seconds.", 0, 4102444800),
			"max_close_ts":   integer("query", "Maximum close Unix timestamp in seconds.", 0, 4102444800),
			"min_settled_ts": integer("query", "Minimum settlement Unix timestamp in seconds.", 0, 4102444800),
			"max_settled_ts": integer("query", "Maximum settlement Unix timestamp in seconds.", 0, 4102444800),
			"status":         withEnum(str("query", "Filter by market status."), "unopened", "open", "paused", "closed", "settled"),
			"tickers":        withPattern(str("query", "Comma-separated market tickers."), `^[A-Z0-9._,-]{1,2048}$`),
			"mve_filter":     withEnum(str("query", "Include only or exclude multivariate events."), "only", "exclude"),
		}),
		ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(paginatedResponse("markets", nil), marketProjectableFields...), "ticker"), map[string]ResponseField{
			"title":      responseField("string", "Market title when supplied by Kalshi."),
			"close_time": responseFieldWithFormat("string", "date-time", "RFC 3339 market close time."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/market/get-markets",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "markets.get", CLIPath: []string{"markets", "get"},
		Summary: "Get one market by ticker.", Method: "GET", Path: "/markets/{ticker}",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{
			"ticker": withPattern(str("path", "Market ticker."), tickerPattern),
		}, "ticker"), ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(responseObject(map[string]ResponseField{"market": responseField("object", "Market object.")}, "market"), prefixedFields("market", marketProjectableFields)...), "market.ticker"), map[string]ResponseField{
			"market.title":      responseField("string", "Market title when supplied by Kalshi."),
			"market.close_time": responseFieldWithFormat("string", "date-time", "RFC 3339 market close time."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/market/get-market",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "markets.search", CLIPath: []string{"markets", "search"},
		Summary: "Search a bounded market scan by stable keyword fields.", Method: "GET", Path: "/markets",
		Effect: Effect{Class: "read", Network: true}, Paginated: true,
		LocalMatch: &LocalMatch{Parameter: "query", Mode: "contains_case_insensitive", Fields: []string{"ticker", "event_ticker", "yes_sub_title", "no_sub_title"}},
		ParamsSchema: objectSchema(map[string]Field{
			"query":          withLength(str("local", "Case-insensitive substring matched locally against stable market identity and side-title fields."), 1, 128),
			"limit":          integer("query", "Results per upstream page before local matching.", 1, 1000),
			"cursor":         str("query", "Opaque pagination cursor."),
			"event_ticker":   withPattern(str("query", "Filter by one event ticker."), tickerPattern),
			"series_ticker":  withPattern(str("query", "Filter by series ticker."), tickerPattern),
			"min_created_ts": integer("query", "Minimum creation Unix timestamp in seconds.", 0, 4102444800),
			"max_created_ts": integer("query", "Maximum creation Unix timestamp in seconds.", 0, 4102444800),
			"min_updated_ts": integer("query", "Minimum metadata update Unix timestamp in seconds.", 0, 4102444800),
			"min_close_ts":   integer("query", "Minimum close Unix timestamp in seconds.", 0, 4102444800),
			"max_close_ts":   integer("query", "Maximum close Unix timestamp in seconds.", 0, 4102444800),
			"min_settled_ts": integer("query", "Minimum settlement Unix timestamp in seconds.", 0, 4102444800),
			"max_settled_ts": integer("query", "Maximum settlement Unix timestamp in seconds.", 0, 4102444800),
			"status":         withEnum(str("query", "Filter by market status."), "unopened", "open", "paused", "closed", "settled"),
			"tickers":        withPattern(str("query", "Comma-separated market tickers."), `^[A-Z0-9._,-]{1,2048}$`),
			"mve_filter":     withEnum(str("query", "Include only or exclude multivariate events."), "only", "exclude"),
		}, "query"),
		ResponseSchema: withProjectedContracts(withRequiredFields(withProjectable(paginatedResponse("markets", nil), marketProjectableFields...), map[string]string{
			"ticker": "string", "event_ticker": "string", "yes_sub_title": "string", "no_sub_title": "string",
		}), map[string]ResponseField{
			"title":      responseField("string", "Deprecated market title when supplied by Kalshi; not used for matching."),
			"close_time": responseFieldWithFormat("string", "date-time", "RFC 3339 market close time."),
		}), DefaultFields: []string{"ticker", "event_ticker", "yes_sub_title", "no_sub_title"}, DocsURL: "https://docs.kalshi.com/api-reference/market/get-markets",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "events.list", CLIPath: []string{"events", "list"},
		Summary: "List events with bounded cursor pagination.", Method: "GET", Path: "/events",
		Effect: Effect{Class: "read", Network: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"limit":               integer("query", "Results per upstream page.", 1, 200),
			"cursor":              str("query", "Opaque pagination cursor."),
			"with_nested_markets": boolean("query", "Include live-tier nested markets."),
			"with_milestones":     boolean("query", "Include related milestones."),
			"status":              withEnum(str("query", "Filter by event status."), "unopened", "open", "closed", "settled"),
			"series_ticker":       withPattern(str("query", "Filter by series ticker."), tickerPattern),
			"tickers":             withPattern(str("query", "Comma-separated event tickers."), `^[A-Z0-9._,-]{1,2048}$`),
			"min_close_ts":        integer("query", "Minimum close Unix timestamp in seconds.", 0, 4102444800),
			"min_updated_ts":      integer("query", "Minimum metadata update Unix timestamp in seconds.", 0, 4102444800),
		}), ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(paginatedResponse("events", map[string]ResponseField{"milestones": responseField("array", "Optional related milestones.")}), eventProjectableFields...), "event_ticker"), map[string]ResponseField{
			"title": responseField("string", "Event title when supplied by Kalshi."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/events/get-events",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "events.get", CLIPath: []string{"events", "get"},
		Summary: "Get one event by ticker.", Method: "GET", Path: "/events/{event_ticker}",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{
			"event_ticker":        withPattern(str("path", "Event ticker."), tickerPattern),
			"with_nested_markets": boolean("query", "Include live-tier nested markets."),
		}, "event_ticker"), ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(responseObject(map[string]ResponseField{"event": responseField("object", "Event object."), "markets": responseField("array", "Associated markets; deprecated in favor of nested markets.")}, "event", "markets"), append(prefixedFields("event", eventProjectableFields), prefixedFields("markets", marketProjectableFields)...)...), "event.event_ticker"), map[string]ResponseField{
			"event.title": responseField("string", "Event title when supplied by Kalshi."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/events/get-event",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "series.list", CLIPath: []string{"series", "list"},
		Summary: "List series with exact tag and category filters.", Method: "GET", Path: "/series",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{
			"category":                 str("query", "Filter by category."),
			"tags":                     str("query", "Comma-separated exact tags; spaces inside a tag are preserved."),
			"include_product_metadata": boolean("query", "Include product metadata."),
			"include_volume":           boolean("query", "Include cumulative volume_fp for each series."),
			"min_updated_ts":           integer("query", "Filter series with metadata updated after this Unix timestamp in seconds.", 0, 4102444800),
		}), ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(collectionResponse("series"), seriesProjectableFields...), "ticker"), map[string]ResponseField{
			"title": responseField("string", "Series title when supplied by Kalshi."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/market/get-series-list",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "series.get", CLIPath: []string{"series", "get"},
		Summary: "Get one series by ticker.", Method: "GET", Path: "/series/{series_ticker}",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{
			"series_ticker":  withPattern(str("path", "Series ticker."), tickerPattern),
			"include_volume": boolean("query", "Include cumulative volume_fp for the series."),
		}, "series_ticker"), ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(responseObject(map[string]ResponseField{"series": responseField("object", "Series object.")}, "series"), prefixedFields("series", seriesProjectableFields)...), "series.ticker"), map[string]ResponseField{
			"series.title": responseField("string", "Series title when supplied by Kalshi."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/market/get-series",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "orderbook.get", CLIPath: []string{"orderbook", "get"},
		Summary: "Get a fixed-point market orderbook, anonymously by default or optionally signed.", Method: "GET", Path: "/markets/{ticker}/orderbook",
		Effect: Effect{Class: "read", Network: true, AuthOptional: true}, ParamsSchema: objectSchema(map[string]Field{
			"ticker": withPattern(str("path", "Market ticker."), tickerPattern),
			"depth":  integer("query", "Orderbook depth; 0 means all levels.", 0, 100),
		}, "ticker"), ResponseSchema: withProjectedContracts(withProjectable(responseObject(map[string]ResponseField{"orderbook_fp": responseField("object", "Fixed-point YES and NO bid levels.")}, "orderbook_fp"), "orderbook_fp", "orderbook_fp.yes_dollars", "orderbook_fp.no_dollars"), map[string]ResponseField{
			"orderbook_fp.yes_dollars": responseField("array", "Fixed-point YES bid levels."),
			"orderbook_fp.no_dollars":  responseField("array", "Fixed-point NO bid levels."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/market/get-market-orderbook",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "trades.list", CLIPath: []string{"trades", "list"},
		Summary: "List public trades with bounded cursor pagination.", Method: "GET", Path: "/markets/trades",
		Effect: Effect{Class: "read", Network: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"limit":          integer("query", "Results per upstream page.", 1, 1000),
			"cursor":         str("query", "Opaque pagination cursor."),
			"ticker":         withPattern(str("query", "Filter by market ticker."), tickerPattern),
			"min_ts":         integer("query", "Minimum trade Unix timestamp.", 0, 4102444800),
			"max_ts":         integer("query", "Maximum trade Unix timestamp.", 0, 4102444800),
			"is_block_trade": boolean("query", "Filter by block-trade status."),
		}), ResponseSchema: withProjectedContracts(withRequiredStringFields(withProjectable(paginatedResponse("trades", nil), tradeProjectableFields...), "trade_id"), map[string]ResponseField{
			"ticker":       responseField("string", "Market ticker for the trade."),
			"created_time": responseFieldWithFormat("string", "date-time", "RFC 3339 trade creation time."),
		}), DocsURL: "https://docs.kalshi.com/api-reference/market/get-trades",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "candlesticks.get", CLIPath: []string{"candlesticks", "get"},
		Summary: "Get bounded fixed-point candlesticks for one live-tier market.", Method: "GET", Path: "/series/{series_ticker}/markets/{ticker}/candlesticks",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{
			"series_ticker":               withPattern(str("path", "Series ticker containing the market."), tickerPattern),
			"ticker":                      withPattern(str("path", "Market ticker."), tickerPattern),
			"start_ts":                    integer("query", "Inclusive start Unix timestamp in seconds.", 0, 4102444800),
			"end_ts":                      integer("query", "Inclusive end Unix timestamp in seconds.", 0, 4102444800),
			"period_interval":             integer("query", "Candlestick interval in minutes: 1, 60, or 1440.", 1, 1440),
			"include_latest_before_start": withDefault(boolean("query", "Prepend the latest prior candle for price continuity."), false),
		}, "series_ticker", "ticker", "start_ts", "end_ts", "period_interval"),
		ResponseSchema: currentCandlestickResponse(), DocsURL: "https://docs.kalshi.com/api-reference/market/get-market-candlesticks",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "candlesticks.historical", CLIPath: []string{"candlesticks", "historical"},
		Summary: "Get bounded candlesticks for one archived market.", Method: "GET", Path: "/historical/markets/{ticker}/candlesticks",
		Effect: Effect{Class: "read", Network: true}, ParamsSchema: objectSchema(map[string]Field{
			"ticker":          withPattern(str("path", "Archived market ticker."), tickerPattern),
			"start_ts":        integer("query", "Inclusive start Unix timestamp in seconds.", 0, 4102444800),
			"end_ts":          integer("query", "Inclusive end Unix timestamp in seconds.", 0, 4102444800),
			"period_interval": integer("query", "Candlestick interval in minutes: 1, 60, or 1440.", 1, 1440),
		}, "ticker", "start_ts", "end_ts", "period_interval"),
		ResponseSchema: historicalCandlestickResponse(), DocsURL: "https://docs.kalshi.com/api-reference/historical/get-historical-market-candlesticks",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "portfolio.balance", CLIPath: []string{"portfolio", "balance"},
		Summary: "Get authenticated cash balance and portfolio value.", Method: "GET", Path: "/portfolio/balance",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true}, ParamsSchema: objectSchema(map[string]Field{
			"subaccount":     integer("query", "Subaccount number; 0 is primary.", 0, 63),
			"exchange_index": integer("query", "Exchange shard index.", 0, 1024),
		}), ResponseSchema: withProjectable(responseObject(map[string]ResponseField{"balance": responseField("integer", "Available balance in cents."), "balance_dollars": responseField("string", "Available balance as fixed-point dollars."), "portfolio_value": responseField("integer", "Portfolio value in cents."), "updated_ts": responseField("integer", "Upstream update timestamp.")}, "balance", "balance_dollars", "portfolio_value", "updated_ts"), "balance", "balance_dollars", "portfolio_value", "updated_ts"), DocsURL: "https://docs.kalshi.com/api-reference/portfolio/get-balance",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "portfolio.positions", CLIPath: []string{"portfolio", "positions"},
		Summary: "List live-tier market positions with fixed-point exposure, realized P&L, and fees.", Method: "GET", Path: "/portfolio/positions",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"limit":        integer("query", "Results per upstream page.", 1, 1000),
			"cursor":       str("query", "Opaque pagination cursor."),
			"count_filter": withPattern(str("query", "Comma-separated non-zero filters: position and/or total_traded."), `^(position|total_traded)(,(position|total_traded))?$`),
			"ticker":       withPattern(str("query", "Filter by market ticker."), tickerPattern),
			"event_ticker": withPattern(str("query", "Filter by one event ticker."), tickerPattern),
			"subaccount":   integer("query", "Subaccount number; 0 is primary.", 0, 63),
		}), ResponseSchema: portfolioPositionResponse(positionProjectableFields), DefaultFields: append([]string(nil), positionProjectableFields...), DocsURL: "https://docs.kalshi.com/api-reference/portfolio/get-positions",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "portfolio.pnl", CLIPath: []string{"portfolio", "pnl"},
		Summary: "List upstream-reported realized P&L, exposure, and fees; does not estimate unrealized or net P&L.", Method: "GET", Path: "/portfolio/positions",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"limit":        integer("query", "Results per upstream page.", 1, 1000),
			"cursor":       str("query", "Opaque pagination cursor."),
			"count_filter": withPattern(str("query", "Comma-separated non-zero filters: position and/or total_traded."), `^(position|total_traded)(,(position|total_traded))?$`),
			"ticker":       withPattern(str("query", "Filter by market ticker."), tickerPattern),
			"event_ticker": withPattern(str("query", "Filter by one event ticker."), tickerPattern),
			"subaccount":   integer("query", "Subaccount number; 0 is primary.", 0, 63),
		}), ResponseSchema: portfolioPositionResponse(pnlProjectableFields), DefaultFields: append([]string(nil), pnlProjectableFields...), DocsURL: "https://docs.kalshi.com/api-reference/portfolio/get-positions",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "portfolio.fills", CLIPath: []string{"portfolio", "fills"},
		Summary: "List authenticated fills using fixed-point count, price, and fee fields.", Method: "GET", Path: "/portfolio/fills",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"ticker":     withPattern(str("query", "Filter by market ticker."), tickerPattern),
			"order_id":   withPattern(str("query", "Filter by exchange order ID."), idPattern),
			"min_ts":     integer("query", "Minimum fill Unix timestamp.", 0, 4102444800),
			"max_ts":     integer("query", "Maximum fill Unix timestamp.", 0, 4102444800),
			"limit":      integer("query", "Results per upstream page.", 1, 1000),
			"cursor":     str("query", "Opaque pagination cursor."),
			"subaccount": integer("query", "Subaccount number; omit for all.", 0, 63),
		}), ResponseSchema: portfolioFillsResponse(), DefaultFields: []string{"fill_id", "order_id", "ticker", "outcome_side", "book_side", "count_fp", "yes_price_dollars", "no_price_dollars", "is_taker", "fee_cost"}, DocsURL: "https://docs.kalshi.com/api-reference/portfolio/get-fills",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "orders.list", CLIPath: []string{"orders", "list"},
		Summary: "List authenticated orders with bounded cursor pagination.", Method: "GET", Path: "/portfolio/orders",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true}, Paginated: true,
		ParamsSchema: objectSchema(map[string]Field{
			"ticker":       withPattern(str("query", "Filter by market ticker."), tickerPattern),
			"event_ticker": withPattern(str("query", "Comma-separated event tickers, maximum 10."), `^[A-Z0-9._,-]{1,1280}$`),
			"min_ts":       integer("query", "Minimum order Unix timestamp.", 0, 4102444800),
			"max_ts":       integer("query", "Maximum order Unix timestamp.", 0, 4102444800),
			"status":       withEnum(str("query", "Order status."), "resting", "canceled", "executed"),
			"limit":        integer("query", "Results per upstream page.", 1, 1000),
			"cursor":       str("query", "Opaque pagination cursor."),
			"subaccount":   integer("query", "Subaccount number; omit for all.", 0, 63),
		}), ResponseSchema: withRequiredStringFields(withProjectable(paginatedResponse("orders", nil), orderProjectableFields...), "order_id"), DocsURL: "https://docs.kalshi.com/api-reference/orders/get-orders",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "orders.get", CLIPath: []string{"orders", "get"},
		Summary: "Get one authenticated order by exchange order ID.", Method: "GET", Path: "/portfolio/orders/{order_id}",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true}, ParamsSchema: objectSchema(map[string]Field{
			"order_id": withPattern(str("path", "Exchange order ID."), idPattern),
		}, "order_id"), ResponseSchema: withRequiredStringFields(withProjectable(responseObject(map[string]ResponseField{"order": responseField("object", "Order object.")}, "order"), prefixedFields("order", orderProjectableFields)...), "order.order_id"), DocsURL: "https://docs.kalshi.com/api-reference/orders/get-order",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "orders.reconcile", CLIPath: []string{"orders", "reconcile"},
		Summary: "Find an order by caller-supplied client_order_id after an ambiguous create result.", Method: "GET", Path: "/portfolio/orders",
		Effect: Effect{Class: "read", Network: true, AuthRequired: true, Idempotency: "client_order_id", Reconciliation: "Scans bounded order pages and returns exact client_order_id matches."}, Paginated: true, LocalMatch: &LocalMatch{Parameter: "client_order_id", Mode: "exact", Fields: []string{"client_order_id"}},
		ParamsSchema: objectSchema(map[string]Field{
			"client_order_id": withPattern(withLength(str("local", "Caller-supplied idempotency key to reconcile."), 1, 64), idPattern),
			"ticker":          withPattern(str("query", "Optional market ticker to bound the scan."), tickerPattern),
			"status":          withEnum(str("query", "Optional order status to bound the scan."), "resting", "canceled", "executed"),
			"limit":           integer("query", "Results per upstream page.", 1, 1000),
			"cursor":          str("query", "Opaque pagination cursor."),
			"subaccount":      integer("query", "Subaccount number; omit for all.", 0, 63),
		}, "client_order_id"), ResponseSchema: withRequiredStringFields(withProjectable(paginatedResponse("orders", nil), orderProjectableFields...), "client_order_id", "order_id"), DocsURL: "https://docs.kalshi.com/api-reference/orders/get-orders",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "orders.create", CLIPath: []string{"orders", "create"},
		Summary: "Create one V2 event order under explicit policy and confirmation gates.", Method: "POST", Path: "/portfolio/events/orders",
		Effect: Effect{Class: "write", Network: true, Mutation: true, AuthRequired: true, ConfirmationRequired: true, Idempotency: "client_order_id required; reuse it when reconciling an uncertain response", Reconciliation: "Run orders reconcile with the same client_order_id before any retry."},
		ParamsSchema: objectSchema(map[string]Field{
			"ticker":                     withPattern(str("body", "Market ticker."), tickerPattern),
			"client_order_id":            withPattern(withLength(str("body", "Caller-supplied idempotency key."), 1, 64), idPattern),
			"side":                       withEnum(str("body", "YES book side."), "bid", "ask"),
			"count":                      withPattern(str("body", "Fixed-point contract count with at most 2 decimals."), countPattern),
			"price":                      withPattern(str("body", "Fixed-point dollar price with at most 6 decimals; market tick rules still apply upstream."), pricePattern),
			"time_in_force":              withDefault(withEnum(str("body", "Order time in force."), "fill_or_kill", "good_till_canceled", "immediate_or_cancel"), "good_till_canceled"),
			"self_trade_prevention_type": withDefault(withEnum(str("body", "Self-trade prevention behavior."), "taker_at_cross", "maker"), "taker_at_cross"),
			"expiration_time":            integer("body", "Optional expiration Unix timestamp in seconds.", 1, 4102444800),
			"post_only":                  withDefault(boolean("body", "Reject instead of taking liquidity."), true),
			"cancel_order_on_pause":      withDefault(boolean("body", "Cancel if trading or exchange pauses."), true),
			"reduce_only":                withDefault(boolean("body", "Cap count by current position."), false),
			"subaccount":                 integer("body", "Subaccount number; 0 is primary.", 0, 63),
			"order_group_id":             withPattern(str("body", "Optional order-group ID."), idPattern),
			"exchange_index":             integer("body", "Exchange shard index; -1 auto-routes.", -1, 1024),
		}, "ticker", "client_order_id", "side", "count", "price"), ResponseSchema: responseObject(map[string]ResponseField{"order_id": responseField("string", "Exchange order ID."), "client_order_id": responseField("string", "Caller idempotency key."), "fill_count": responseField("string", "Immediately filled contracts."), "remaining_count": responseField("string", "Remaining contracts."), "ts_ms": responseField("integer", "Matching engine timestamp in milliseconds.")}, "order_id", "fill_count", "remaining_count", "ts_ms"), DocsURL: "https://docs.kalshi.com/api-reference/orders/create-order-v2",
	},
	{
		SchemaVersion: commandSchemaVersion, Name: "orders.cancel", CLIPath: []string{"orders", "cancel"},
		Summary: "Cancel all remaining contracts on one V2 event order under explicit gates.", Method: "DELETE", Path: "/portfolio/events/orders/{order_id}",
		Effect: Effect{Class: "write", Network: true, Mutation: true, AuthRequired: true, ConfirmationRequired: true, Reconciliation: "On an ambiguous response, fetch orders get before retrying cancellation."},
		ParamsSchema: objectSchema(map[string]Field{
			"order_id":       withPattern(str("path", "Exchange order ID."), idPattern),
			"subaccount":     integer("query", "Subaccount number; 0 is primary.", 0, 63),
			"exchange_index": integer("query", "Exchange shard index; -1 auto-routes.", -1, 1024),
			"market_ticker":  withPattern(str("query", "Required when exchange_index is -1."), tickerPattern),
		}, "order_id"), ResponseSchema: responseObject(map[string]ResponseField{"order_id": responseField("string", "Exchange order ID."), "client_order_id": responseField("string", "Caller idempotency key when present."), "reduced_by": responseField("string", "Contracts canceled at processing time."), "ts_ms": responseField("integer", "Matching engine timestamp in milliseconds.")}, "order_id", "reduced_by", "ts_ms"), DocsURL: "https://docs.kalshi.com/api-reference/orders/cancel-order-v2",
	},
}

func currentCandlestickResponse() ResponseSchema {
	schema := collectionResponse("candlesticks")
	schema.Properties["ticker"] = responseField("string", "Market ticker.")
	schema.Required = append(schema.Required, "ticker")
	schema = withProjectable(schema, candlestickProjectableFields...)
	schema = withRequiredFields(schema, map[string]string{
		"end_period_ts": "integer", "yes_bid": "object", "yes_bid.open_dollars": "string",
		"yes_bid.low_dollars": "string", "yes_bid.high_dollars": "string", "yes_bid.close_dollars": "string",
		"yes_ask": "object", "yes_ask.open_dollars": "string", "yes_ask.low_dollars": "string",
		"yes_ask.high_dollars": "string", "yes_ask.close_dollars": "string", "price": "object",
		"volume_fp": "string", "open_interest_fp": "string",
	})
	contracts := map[string]ResponseField{
		"end_period_ts":    responseField("integer", "Inclusive candle period end timestamp."),
		"yes_bid":          responseField("object", "YES bid OHLC distribution."),
		"yes_ask":          responseField("object", "YES ask OHLC distribution."),
		"price":            responseField("object", "Trade price distribution."),
		"volume_fp":        responseFieldWithFormat("string", "fixed-point-count", "Candle volume with exactly two decimal places."),
		"open_interest_fp": responseFieldWithFormat("string", "fixed-point-count", "Open interest with exactly two decimal places."),
	}
	for _, field := range []string{
		"yes_bid.open_dollars", "yes_bid.low_dollars", "yes_bid.high_dollars", "yes_bid.close_dollars",
		"yes_ask.open_dollars", "yes_ask.low_dollars", "yes_ask.high_dollars", "yes_ask.close_dollars",
	} {
		contracts[field] = responseFieldWithFormat("string", "fixed-point-dollars", "Fixed-point dollar price with up to six decimal places.")
	}
	for _, field := range []string{
		"price.open_dollars", "price.low_dollars", "price.high_dollars", "price.close_dollars",
		"price.mean_dollars", "price.previous_dollars", "price.min_dollars", "price.max_dollars",
	} {
		contracts[field] = nullableResponseFieldWithFormat("string", "fixed-point-dollars", "Nullable fixed-point trade price with up to six decimal places.")
	}
	return withProjectedContracts(schema, contracts)
}

func historicalCandlestickResponse() ResponseSchema {
	schema := collectionResponse("candlesticks")
	schema.Properties["ticker"] = responseField("string", "Archived market ticker.")
	schema.Required = append(schema.Required, "ticker")
	schema = withProjectable(schema, historicalCandlestickProjectableFields...)
	schema = withRequiredFields(schema, map[string]string{
		"end_period_ts": "integer", "yes_bid": "object", "yes_bid.open": "string", "yes_bid.low": "string",
		"yes_bid.high": "string", "yes_bid.close": "string", "yes_ask": "object", "yes_ask.open": "string",
		"yes_ask.low": "string", "yes_ask.high": "string", "yes_ask.close": "string", "price": "object",
		"price.open": "string", "price.low": "string", "price.high": "string", "price.close": "string",
		"price.mean": "string", "price.previous": "string", "volume": "string", "open_interest": "string",
	})
	contracts := map[string]ResponseField{
		"end_period_ts": responseField("integer", "Inclusive candle period end timestamp."),
		"yes_bid":       responseField("object", "YES bid OHLC distribution."),
		"yes_ask":       responseField("object", "YES ask OHLC distribution."),
		"price":         responseField("object", "Trade price distribution."),
		"volume":        responseFieldWithFormat("string", "fixed-point-count", "Candle volume with exactly two decimal places."),
		"open_interest": responseFieldWithFormat("string", "fixed-point-count", "Open interest with exactly two decimal places."),
	}
	for _, field := range []string{
		"yes_bid.open", "yes_bid.low", "yes_bid.high", "yes_bid.close",
		"yes_ask.open", "yes_ask.low", "yes_ask.high", "yes_ask.close",
	} {
		contracts[field] = responseFieldWithFormat("string", "fixed-point-dollars", "Fixed-point dollar price with up to six decimal places.")
	}
	for _, field := range []string{"price.open", "price.low", "price.high", "price.close", "price.mean", "price.previous"} {
		contracts[field] = nullableResponseFieldWithFormat("string", "fixed-point-dollars", "Required nullable fixed-point trade price with up to six decimal places.")
	}
	return withProjectedContracts(schema, contracts)
}

func portfolioPositionResponse(fields []string) ResponseSchema {
	schema := withRequiredStringFields(withProjectable(paginatedResponse("market_positions", nil), fields...), fields...)
	contracts := map[string]ResponseField{
		"ticker":          responseField("string", "Market ticker."),
		"last_updated_ts": responseFieldWithFormat("string", "date-time", "RFC 3339 position update time."),
	}
	for _, field := range fields {
		switch field {
		case "position_fp":
			contracts[field] = responseFieldWithFormat("string", "fixed-point-count", "Signed position with exactly two decimal places.")
		case "total_traded_dollars", "market_exposure_dollars", "realized_pnl_dollars", "fees_paid_dollars":
			contracts[field] = responseFieldWithFormat("string", "fixed-point-dollars", "Signed fixed-point dollar value with up to six decimal places.")
		}
	}
	return withProjectedContracts(schema, contracts)
}

func portfolioFillsResponse() ResponseSchema {
	schema := withRequiredFields(withProjectable(paginatedResponse("fills", nil), fillProjectableFields...), map[string]string{
		"fill_id": "string", "order_id": "string", "ticker": "string",
		"outcome_side": "string", "book_side": "string", "count_fp": "string", "yes_price_dollars": "string",
		"no_price_dollars": "string", "is_taker": "boolean", "fee_cost": "string",
	})
	schema = withRequiredCursorPresence(schema)
	return withProjectedContracts(schema, map[string]ResponseField{
		"fill_id":           responseField("string", "Canonical fill ID."),
		"order_id":          responseField("string", "Exchange order ID."),
		"ticker":            responseField("string", "Canonical market ticker."),
		"outcome_side":      {Type: "string", Enum: []string{"yes", "no"}, Description: "Directional outcome side."},
		"book_side":         {Type: "string", Enum: []string{"bid", "ask"}, Description: "Directional book side."},
		"count_fp":          responseFieldWithFormat("string", "fixed-point-count", "Fill count with exactly two decimal places."),
		"yes_price_dollars": responseFieldWithFormat("string", "fixed-point-dollars", "YES price with up to six decimal places."),
		"no_price_dollars":  responseFieldWithFormat("string", "fixed-point-dollars", "NO price with up to six decimal places."),
		"is_taker":          responseField("boolean", "Whether the fill removed liquidity."),
		"fee_cost":          responseFieldWithFormat("string", "fixed-point-dollars", "Fee cost with up to six decimal places."),
		"created_time":      nullableResponseFieldWithFormat("string", "date-time", "RFC 3339 fill time when supplied by Kalshi; null when explicitly projected but absent."),
		"subaccount_number": {Type: "integer", Nullable: true, Description: "Nullable subaccount number when supplied by Kalshi."},
	})
}

func withEnum(f Field, values ...string) Field  { f.Enum = values; return f }
func withPattern(f Field, pattern string) Field { f.Pattern = pattern; return f }
func withLength(f Field, min, max int) Field    { f.MinLength, f.MaxLength = min, max; return f }
func withDefault(f Field, value any) Field      { f.Default = value; return f }

func All() []Command {
	out := make([]Command, len(commands))
	for i, command := range commands {
		out[i] = withOutputContractVersion(command)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func ByName(name string) (Command, bool) {
	for _, c := range commands {
		if c.Name == name {
			return withOutputContractVersion(c), true
		}
	}
	return Command{}, false
}

func ByPath(path []string) (Command, bool) {
	for _, c := range commands {
		if len(path) != len(c.CLIPath) {
			continue
		}
		match := true
		for i := range path {
			if path[i] != c.CLIPath[i] {
				match = false
				break
			}
		}
		if match {
			return withOutputContractVersion(c), true
		}
	}
	return Command{}, false
}
