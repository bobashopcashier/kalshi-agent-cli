package registry

import "testing"

func TestRegistryUniqueAndIntrospectable(t *testing.T) {
	all := All()
	if len(all) != 13 {
		t.Fatalf("got %d commands, want 13", len(all))
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, cmd := range all {
		if cmd.SchemaVersion != "kalshi.command/v1" {
			t.Errorf("%s schema version=%q", cmd.Name, cmd.SchemaVersion)
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
		}
		for _, required := range cmd.ParamsSchema.Required {
			if _, ok := cmd.ParamsSchema.Properties[required]; !ok {
				t.Errorf("%s requires missing property %s", cmd.Name, required)
			}
		}
	}
	for _, required := range []string{"exchange.status", "markets.list", "markets.get", "events.list", "events.get", "orderbook.get", "trades.list", "portfolio.balance", "orders.list", "orders.get", "orders.reconcile", "orders.create", "orders.cancel"} {
		if !names[required] {
			t.Errorf("missing %s", required)
		}
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
