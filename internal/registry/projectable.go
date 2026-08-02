package registry

var marketProjectableFields = []string{
	"ticker", "event_ticker", "market_type", "title", "subtitle", "yes_sub_title", "no_sub_title",
	"created_time", "updated_time", "open_time", "close_time", "expected_expiration_time", "expiration_time",
	"latest_expiration_time", "settlement_timer_seconds", "status", "yes_bid_dollars", "yes_bid_size_fp",
	"yes_ask_dollars", "yes_ask_size_fp", "no_bid_dollars", "no_ask_dollars", "last_price_dollars",
	"volume_fp", "volume_24h_fp", "result", "can_close_early", "open_interest_fp", "notional_value_dollars",
	"previous_yes_bid_dollars", "previous_yes_ask_dollars", "previous_price_dollars", "liquidity_dollars",
	"settlement_value_dollars", "settlement_ts", "expiration_value", "occurrence_datetime",
	"fee_waiver_expiration_time", "early_close_condition", "strike_type", "floor_strike", "cap_strike",
	"functional_strike", "custom_strike", "rules_primary", "rules_secondary", "mve_collection_ticker",
	"mve_selected_legs", "mve_selected_legs.event_ticker", "mve_selected_legs.market_ticker",
	"mve_selected_legs.side", "mve_selected_legs.yes_settlement_value_dollars", "primary_participant_key",
	"price_level_structure", "price_ranges", "price_ranges.start", "price_ranges.end", "price_ranges.step",
	"is_provisional", "exchange_index",
}

var eventProjectableFields = append([]string{
	"event_ticker", "series_ticker", "sub_title", "title", "collateral_return_type", "mutually_exclusive",
	"category", "strike_date", "strike_period", "available_on_brokers", "product_metadata", "settlement_sources",
	"settlement_sources.name", "settlement_sources.url", "last_updated_ts", "fee_type_override",
	"fee_multiplier_override", "exchange_index",
}, prefixedFields("markets", marketProjectableFields)...)

var seriesProjectableFields = []string{
	"ticker", "frequency", "title", "category", "tags", "settlement_sources", "settlement_sources.name",
	"settlement_sources.url", "contract_url", "contract_terms_url", "fee_type", "fee_multiplier",
	"additional_prohibitions", "product_metadata", "volume_fp", "last_updated_ts", "exchange_index",
}

var tradeProjectableFields = []string{
	"trade_id", "ticker", "count_fp", "yes_price_dollars", "no_price_dollars", "taker_side",
	"taker_outcome_side", "taker_book_side", "created_time", "is_block_trade",
}

var orderProjectableFields = []string{
	"order_id", "user_id", "client_order_id", "ticker", "side", "action", "outcome_side", "book_side",
	"type", "status", "yes_price_dollars", "no_price_dollars", "fill_count_fp", "remaining_count_fp",
	"initial_count_fp", "taker_fill_cost_dollars", "maker_fill_cost_dollars", "taker_fees_dollars",
	"maker_fees_dollars", "expiration_time", "created_time", "last_update_time", "self_trade_prevention_type",
	"order_group_id", "cancel_order_on_pause", "subaccount_number", "exchange_index",
}

var positionProjectableFields = []string{
	"ticker", "total_traded_dollars", "position_fp", "market_exposure_dollars",
	"realized_pnl_dollars", "fees_paid_dollars", "last_updated_ts",
}

var pnlProjectableFields = []string{
	"ticker", "position_fp", "market_exposure_dollars", "realized_pnl_dollars",
	"fees_paid_dollars", "last_updated_ts",
}

var fillProjectableFields = []string{
	"fill_id", "order_id", "ticker", "outcome_side", "book_side", "count_fp", "yes_price_dollars",
	"no_price_dollars", "is_taker", "fee_cost", "created_time", "subaccount_number",
}

var candlestickProjectableFields = []string{
	"end_period_ts", "yes_bid", "yes_bid.open_dollars", "yes_bid.low_dollars", "yes_bid.high_dollars",
	"yes_bid.close_dollars", "yes_ask", "yes_ask.open_dollars", "yes_ask.low_dollars",
	"yes_ask.high_dollars", "yes_ask.close_dollars", "price", "price.open_dollars",
	"price.low_dollars", "price.high_dollars", "price.close_dollars", "price.mean_dollars",
	"price.previous_dollars", "price.min_dollars", "price.max_dollars", "volume_fp", "open_interest_fp",
}

var historicalCandlestickProjectableFields = []string{
	"end_period_ts", "yes_bid", "yes_bid.open", "yes_bid.low", "yes_bid.high", "yes_bid.close",
	"yes_ask", "yes_ask.open", "yes_ask.low", "yes_ask.high", "yes_ask.close", "price",
	"price.open", "price.low", "price.high", "price.close", "price.mean", "price.previous",
	"volume", "open_interest",
}
