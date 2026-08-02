package registry

import "testing"

func TestRegistryUniqueAndIntrospectable(t *testing.T) {
	all := All()
	if len(all) != 21 {
		t.Fatalf("got %d commands, want 21", len(all))
	}
	if len(outputContractRevisions) != len(all) {
		t.Fatalf("got %d output contract revisions for %d commands", len(outputContractRevisions), len(all))
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, cmd := range all {
		if cmd.SchemaVersion != "kalshi.command/v1" {
			t.Errorf("%s schema version=%q", cmd.Name, cmd.SchemaVersion)
		}
		if want := "kalshi.output/" + cmd.Name + "/v1"; cmd.OutputContractVersion != want {
			t.Errorf("%s output contract version=%q, want %q", cmd.Name, cmd.OutputContractVersion, want)
		}
		if names[cmd.Name] {
			t.Fatalf("duplicate name %s", cmd.Name)
		}
		names[cmd.Name] = true
		path := cmd.CLIPath[0] + " " + cmd.CLIPath[1]
		if paths[path] {
			t.Fatalf("duplicate path %s", path)
		}
		paths[path] = true
		if cmd.ParamsSchema.AdditionalProperties {
			t.Errorf("%s permits additional properties", cmd.Name)
		}
		if cmd.DocsURL == "" || cmd.Method == "" || cmd.Path == "" {
			t.Errorf("%s is missing contract metadata", cmd.Name)
		}
		if cmd.ResponseSchema.Dialect == "" || len(cmd.ResponseSchema.Properties) == 0 {
			t.Errorf("%s is missing machine-readable response schema", cmd.Name)
		}
		if cmd.Effect.Class == "read" {
			if len(cmd.ResponseSchema.ProjectableFields) == 0 {
				t.Errorf("%s is missing projectable response paths", cmd.Name)
			}
			seenFields := map[string]bool{}
			for i, field := range cmd.ResponseSchema.ProjectableFields {
				if seenFields[field] {
					t.Errorf("%s repeats projectable field %s", cmd.Name, field)
				}
				seenFields[field] = true
				if i > 0 && cmd.ResponseSchema.ProjectableFields[i-1] > field {
					t.Errorf("%s projectable fields are not sorted", cmd.Name)
				}
			}
			for i, field := range cmd.ResponseSchema.RequiredFields {
				if !seenFields[field] {
					t.Errorf("%s requires non-projectable response field %s", cmd.Name, field)
				}
				if i > 0 && cmd.ResponseSchema.RequiredFields[i-1] >= field {
					t.Errorf("%s required response fields are not sorted and unique", cmd.Name)
				}
				if cmd.ResponseSchema.RequiredFieldTypes[field] == "" {
					t.Errorf("%s required response field %s has no type", cmd.Name, field)
				}
			}
			if len(cmd.ResponseSchema.RequiredFieldTypes) != len(cmd.ResponseSchema.RequiredFields) {
				t.Errorf("%s required response field types do not match required fields", cmd.Name)
			}
			seenDefaults := map[string]bool{}
			for _, field := range cmd.DefaultFields {
				if !seenFields[field] {
					t.Errorf("%s defaults to non-projectable response field %s", cmd.Name, field)
				}
				if seenDefaults[field] {
					t.Errorf("%s repeats default response field %s", cmd.Name, field)
				}
				seenDefaults[field] = true
			}
			if cmd.LocalMatch != nil {
				for _, field := range cmd.LocalMatch.Fields {
					if !seenFields[field] || cmd.ResponseSchema.RequiredFieldTypes[field] != "string" {
						t.Errorf("%s locally matches field %s without a required string contract", cmd.Name, field)
					}
				}
			}
			for path, fieldContract := range cmd.ResponseSchema.ProjectedContracts {
				if !seenFields[path] {
					t.Errorf("%s constrains non-projectable response field %s", cmd.Name, path)
				}
				switch fieldContract.Type {
				case "object", "array", "string", "boolean", "integer":
				default:
					t.Errorf("%s projected response field %s has unsupported type %q", cmd.Name, path, fieldContract.Type)
				}
				if fieldContract.Format != "" {
					if fieldContract.Type != "string" {
						t.Errorf("%s projected response field %s has unsupported type/format %q/%q", cmd.Name, path, fieldContract.Type, fieldContract.Format)
					}
					switch fieldContract.Format {
					case "date-time", "fixed-point-count", "fixed-point-dollars":
					default:
						t.Errorf("%s projected response field %s has unsupported format %q", cmd.Name, path, fieldContract.Format)
					}
				}
				if len(fieldContract.Enum) > 0 {
					if fieldContract.Type != "string" {
						t.Errorf("%s projected response field %s has enum on non-string type %q", cmd.Name, path, fieldContract.Type)
					}
					seenValues := map[string]bool{}
					for _, value := range fieldContract.Enum {
						if value == "" || seenValues[value] {
							t.Errorf("%s projected response field %s has invalid enum value %q", cmd.Name, path, value)
						}
						seenValues[value] = true
					}
				}
			}
			seenAliases := map[string]bool{}
			for _, alias := range cmd.ResponseSchema.CursorAliases {
				if !cmd.Paginated || cmd.ResponseSchema.CursorField == "" {
					t.Errorf("%s declares cursor alias %s without pagination", cmd.Name, alias)
				}
				if alias == cmd.ResponseSchema.CursorField || seenAliases[alias] {
					t.Errorf("%s has invalid or duplicate cursor alias %s", cmd.Name, alias)
				}
				seenAliases[alias] = true
			}
			if cmd.ResponseSchema.RequireCursorPresence && (!cmd.Paginated || cmd.ResponseSchema.CursorField == "") {
				t.Errorf("%s requires cursor presence without pagination metadata", cmd.Name)
			}
		}
		if cmd.LocalMatch != nil {
			field, ok := cmd.ParamsSchema.Properties[cmd.LocalMatch.Parameter]
			if !ok || field.Location != "local" || field.Type != "string" {
				t.Errorf("%s has invalid local match parameter %s", cmd.Name, cmd.LocalMatch.Parameter)
			}
			if cmd.LocalMatch.Mode != "exact" && cmd.LocalMatch.Mode != "contains_case_insensitive" {
				t.Errorf("%s has invalid local match mode %s", cmd.Name, cmd.LocalMatch.Mode)
			}
			if len(cmd.LocalMatch.Fields) == 0 {
				t.Errorf("%s has no local match fields", cmd.Name)
			}
		}
		for _, required := range cmd.ParamsSchema.Required {
			if _, ok := cmd.ParamsSchema.Properties[required]; !ok {
				t.Errorf("%s requires missing property %s", cmd.Name, required)
			}
		}
	}
	for _, required := range []string{"exchange.status", "markets.list", "markets.get", "markets.search", "events.list", "events.get", "series.list", "series.get", "orderbook.get", "trades.list", "candlesticks.get", "candlesticks.historical", "portfolio.balance", "portfolio.positions", "portfolio.pnl", "portfolio.fills", "orders.list", "orders.get", "orders.reconcile", "orders.create", "orders.cancel"} {
		if !names[required] {
			t.Errorf("missing %s", required)
		}
	}
}

func TestSeriesCommandsExposeCurrentPublicContract(t *testing.T) {
	list, ok := ByName("series.list")
	if !ok || list.Method != "GET" || list.Path != "/series" || list.Paginated {
		t.Fatalf("unexpected series list contract: %#v", list)
	}
	if list.ResponseSchema.CollectionField != "series" || list.ResponseSchema.CursorField != "" {
		t.Fatalf("unexpected series collection metadata: %#v", list.ResponseSchema)
	}
	for _, name := range []string{"category", "tags", "include_product_metadata", "include_volume", "min_updated_ts"} {
		if _, ok := list.ParamsSchema.Properties[name]; !ok {
			t.Errorf("series.list missing parameter %s", name)
		}
	}
	for _, unsupported := range []string{"cursor", "limit"} {
		if _, ok := list.ParamsSchema.Properties[unsupported]; ok {
			t.Errorf("series.list must not expose ignored upstream parameter %s", unsupported)
		}
	}

	get, ok := ByName("series.get")
	if !ok || get.Method != "GET" || get.Path != "/series/{series_ticker}" {
		t.Fatalf("unexpected series get contract: %#v", get)
	}
}

func TestCommandsExposeProjectedContractsAndCursorAliases(t *testing.T) {
	list, ok := ByName("markets.list")
	if !ok {
		t.Fatal("markets.list is not registered")
	}
	if Version != "kalshi.registry/v4" {
		t.Fatalf("registry version=%q", Version)
	}
	if got := list.ResponseSchema.ProjectedContracts["title"]; got.Type != "string" || got.Format != "" {
		t.Fatalf("title contract=%#v", got)
	}
	if got := list.ResponseSchema.ProjectedContracts["close_time"]; got.Type != "string" || got.Format != "date-time" {
		t.Fatalf("close_time contract=%#v", got)
	}
	if len(list.ResponseSchema.CursorAliases) != 1 || list.ResponseSchema.CursorAliases[0] != "next_cursor" {
		t.Fatalf("cursor aliases=%#v", list.ResponseSchema.CursorAliases)
	}

	for _, test := range []struct {
		command string
		path    string
		typeOf  string
		format  string
	}{
		{command: "events.list", path: "title", typeOf: "string"},
		{command: "events.get", path: "event.title", typeOf: "string"},
		{command: "series.list", path: "title", typeOf: "string"},
		{command: "series.get", path: "series.title", typeOf: "string"},
		{command: "trades.list", path: "ticker", typeOf: "string"},
		{command: "trades.list", path: "created_time", typeOf: "string", format: "date-time"},
		{command: "orderbook.get", path: "orderbook_fp.yes_dollars", typeOf: "array"},
		{command: "orderbook.get", path: "orderbook_fp.no_dollars", typeOf: "array"},
	} {
		t.Run(test.command+"/"+test.path, func(t *testing.T) {
			command, ok := ByName(test.command)
			if !ok {
				t.Fatalf("%s is not registered", test.command)
			}
			got := command.ResponseSchema.ProjectedContracts[test.path]
			if got.Type != test.typeOf || got.Format != test.format {
				t.Fatalf("contract=%#v, want type=%q format=%q", got, test.typeOf, test.format)
			}
		})
	}
}

func TestPortfolioSearchAndCandlestickContracts(t *testing.T) {
	search, ok := ByName("markets.search")
	if !ok || search.Path != "/markets" || !search.Paginated || search.LocalMatch == nil {
		t.Fatalf("unexpected market search contract: %#v", search)
	}
	if search.LocalMatch.Parameter != "query" || search.LocalMatch.Mode != "contains_case_insensitive" {
		t.Fatalf("unexpected local match: %#v", search.LocalMatch)
	}

	for _, name := range []string{"portfolio.positions", "portfolio.pnl", "portfolio.fills"} {
		command, ok := ByName(name)
		if !ok || !command.Effect.AuthRequired || !command.Paginated {
			t.Fatalf("unexpected %s contract: %#v", name, command)
		}
	}
	positions, _ := ByName("portfolio.positions")
	for _, field := range []string{"position_fp", "market_exposure_dollars", "realized_pnl_dollars", "fees_paid_dollars"} {
		if positions.ResponseSchema.RequiredFieldTypes[field] != "string" {
			t.Errorf("positions missing fixed-point contract for %s", field)
		}
	}
	fills, _ := ByName("portfolio.fills")
	if fills.ResponseSchema.RequiredFieldTypes["count_fp"] != "string" || fills.ResponseSchema.RequiredFieldTypes["is_taker"] != "boolean" {
		t.Fatalf("unexpected fills field contracts: %#v", fills.ResponseSchema.RequiredFieldTypes)
	}
	if containsString(fills.ResponseSchema.RequiredFields, "trade_id") || containsString(fills.ResponseSchema.RequiredFields, "market_ticker") {
		t.Fatal("fills must not require deprecated aliases")
	}
	if containsString(fills.ResponseSchema.ProjectableFields, "trade_id") || containsString(fills.ResponseSchema.ProjectableFields, "market_ticker") || containsString(fills.ResponseSchema.ProjectableFields, "ts") {
		t.Fatal("fills must not project deprecated aliases")
	}
	if !fills.ResponseSchema.RequireCursorPresence || containsString(fills.DefaultFields, "created_time") || containsString(fills.DefaultFields, "subaccount_number") {
		t.Fatalf("unexpected fills cursor/default contract: %#v", fills)
	}
	if got := fills.ResponseSchema.ProjectedContracts["outcome_side"].Enum; len(got) != 2 || got[0] != "yes" || got[1] != "no" {
		t.Fatalf("outcome_side enum=%#v", got)
	}
	if got := fills.ResponseSchema.ProjectedContracts["book_side"].Enum; len(got) != 2 || got[0] != "bid" || got[1] != "ask" {
		t.Fatalf("book_side enum=%#v", got)
	}
	pnl, _ := ByName("portfolio.pnl")
	if containsString(pnl.ResponseSchema.ProjectableFields, "total_traded_dollars") {
		t.Fatal("portfolio.pnl must expose only its P&L projection")
	}

	current, _ := ByName("candlesticks.get")
	historical, _ := ByName("candlesticks.historical")
	if current.Path != "/series/{series_ticker}/markets/{ticker}/candlesticks" || historical.Path != "/historical/markets/{ticker}/candlesticks" {
		t.Fatalf("unexpected candlestick paths: %s %s", current.Path, historical.Path)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestWritesUseCurrentV2Paths(t *testing.T) {
	create, _ := ByName("orders.create")
	cancel, _ := ByName("orders.cancel")
	if create.Method != "POST" || create.Path != "/portfolio/events/orders" {
		t.Fatalf("unexpected create contract: %#v", create)
	}
	if cancel.Method != "DELETE" || cancel.Path != "/portfolio/events/orders/{order_id}" {
		t.Fatalf("unexpected cancel contract: %#v", cancel)
	}
	if !create.Effect.Mutation || !create.Effect.ConfirmationRequired {
		t.Fatal("create must be a confirmed mutation")
	}
}
