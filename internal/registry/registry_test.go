package registry

import "testing"

func TestRegistryUniqueAndIntrospectable(t *testing.T) {
	all := All()
	if len(all) != 15 {
		t.Fatalf("got %d commands, want 15", len(all))
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
			for path, fieldContract := range cmd.ResponseSchema.ProjectedContracts {
				if !seenFields[path] {
					t.Errorf("%s constrains non-projectable response field %s", cmd.Name, path)
				}
				switch fieldContract.Type {
				case "object", "array", "string", "boolean", "integer":
				default:
					t.Errorf("%s projected response field %s has unsupported type %q", cmd.Name, path, fieldContract.Type)
				}
				if fieldContract.Format != "" && (fieldContract.Type != "string" || fieldContract.Format != "date-time") {
					t.Errorf("%s projected response field %s has unsupported type/format %q/%q", cmd.Name, path, fieldContract.Type, fieldContract.Format)
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
		}
		for _, required := range cmd.ParamsSchema.Required {
			if _, ok := cmd.ParamsSchema.Properties[required]; !ok {
				t.Errorf("%s requires missing property %s", cmd.Name, required)
			}
		}
	}
	for _, required := range []string{"exchange.status", "markets.list", "markets.get", "events.list", "events.get", "series.list", "series.get", "orderbook.get", "trades.list", "portfolio.balance", "orders.list", "orders.get", "orders.reconcile", "orders.create", "orders.cancel"} {
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
	if Version != "kalshi.registry/v3" {
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
