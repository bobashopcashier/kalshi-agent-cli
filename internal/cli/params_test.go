package cli

import (
	"strings"
	"testing"

	"kalshi-agent-cli/internal/registry"
)

func command(t *testing.T, name string) registry.Command {
	t.Helper()
	cmd, ok := registry.ByName(name)
	if !ok {
		t.Fatal("missing command")
	}
	return cmd
}

func TestParamsStrictJSON(t *testing.T) {
	cmd := command(t, "markets.list")
	cases := []struct {
		name, raw string
		flags     map[string]string
		want      string
	}{
		{"unknown", `{"wat":1}`, nil, "unknown property"},
		{"duplicate", `{"limit":1,"limit":2}`, nil, "duplicate object key"},
		{"trailing", `{"limit":1} {}`, nil, "multiple JSON values"},
		{"wrong type", `{"limit":"1"}`, nil, "must be an integer"},
		{"conflict", `{"limit":1}`, map[string]string{"limit": "2"}, "both --params"},
		{"control", `{"cursor":"bad\nvalue"}`, nil, "control"},
		{"bidi", `{"cursor":"bad\u202Evalue"}`, nil, "bidi"},
		{"unpaired surrogate", `{"cursor":"bad\ud800value"}`, nil, "Unicode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeParams(cmd, tc.raw, tc.flags)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestCreateDefaultsAndCrossFieldValidation(t *testing.T) {
	cmd := command(t, "orders.create")
	raw := `{"ticker":"TEST-1","client_order_id":"id-1","side":"bid","count":"1.25","price":"0.123456"}`
	params, err := normalizeParams(cmd, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if params["post_only"] != true || params["cancel_order_on_pause"] != true {
		t.Fatalf("safe defaults missing: %#v", params)
	}
	if params["time_in_force"] != "good_till_canceled" {
		t.Fatalf("unexpected TIF: %#v", params)
	}

	invalid := []string{
		`{"ticker":"TEST-1","client_order_id":"id-1","side":"bid","count":"0","price":"0.5"}`,
		`{"ticker":"TEST-1","client_order_id":"id-1","side":"bid","count":"1.001","price":"0.5"}`,
		`{"ticker":"TEST-1","client_order_id":"id-1","side":"bid","count":"1","price":"1.0"}`,
		`{"ticker":"TEST-1","client_order_id":"id-1","side":"bid","count":"1","price":"0.1234567"}`,
		`{"ticker":"TEST-1","client_order_id":"id-1","side":"bid","count":"1","price":"0.5","time_in_force":"immediate_or_cancel"}`,
	}
	for _, raw := range invalid {
		if _, err := normalizeParams(cmd, raw, nil); err == nil {
			t.Errorf("accepted invalid create params: %s", raw)
		}
	}
}

func TestCancelAutoRouteRequiresTicker(t *testing.T) {
	cmd := command(t, "orders.cancel")
	_, err := normalizeParams(cmd, `{"order_id":"order-1","exchange_index":-1}`, nil)
	if err == nil || !strings.Contains(err.Error(), "market_ticker") {
		t.Fatalf("error=%v", err)
	}
}

func TestInvalidUTF8Rejected(t *testing.T) {
	cmd := command(t, "markets.list")
	raw := string([]byte{'{', '"', 'c', 'u', 'r', 's', 'o', 'r', '"', ':', '"', 0xff, '"', '}'})
	if _, err := normalizeParams(cmd, raw, nil); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}
