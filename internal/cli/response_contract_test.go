package cli

import (
	"errors"
	"reflect"
	"testing"

	"github.com/bobashopcashier/kalshi-cli/internal/registry"
)

func TestValidateOutputContractReportsDeterministicDrift(t *testing.T) {
	command, ok := registry.ByName("markets.list")
	if !ok {
		t.Fatal("markets.list is not registered")
	}
	err := validateOutputContract(command, map[string]any{
		"markets": []any{
			map[string]any{"ticker": "A"},
			map[string]any{"title": "missing identity"},
		},
		"cursor": 42,
	}, nil, nil)
	var contractErr *responseContractError
	if !errors.As(err, &contractErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if !reflect.DeepEqual(contractErr.MissingFields, []string{"markets[].ticker"}) {
		t.Fatalf("missing=%#v", contractErr.MissingFields)
	}
	wantMismatch := []responseTypeMismatch{{Field: "cursor", Expected: "string", Actual: "integer"}}
	if !reflect.DeepEqual(contractErr.TypeMismatches, wantMismatch) {
		t.Fatalf("mismatches=%#v", contractErr.TypeMismatches)
	}
}

func TestValidateOutputContractAllowsEmptyCollections(t *testing.T) {
	command, _ := registry.ByName("markets.list")
	if err := validateOutputContract(command, map[string]any{"markets": []any{}, "cursor": ""}, []string{"title"}, []string{"title"}); err != nil {
		t.Fatalf("empty collection failed contract validation: %v", err)
	}
}

func TestValidateOutputContractRejectsRequiredItemTypeDrift(t *testing.T) {
	command, _ := registry.ByName("markets.list")
	for name, test := range map[string]struct {
		ticker     any
		wantActual string
	}{
		"null":   {ticker: nil, wantActual: "null"},
		"number": {ticker: 42, wantActual: "integer"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateOutputContract(command, map[string]any{"markets": []any{map[string]any{"ticker": test.ticker}}, "cursor": ""}, nil, nil)
			var contractErr *responseContractError
			if !errors.As(err, &contractErr) || len(contractErr.TypeMismatches) != 1 {
				t.Fatalf("err=%T %v", err, err)
			}
			mismatch := contractErr.TypeMismatches[0]
			if mismatch.Field != "markets[].ticker" || mismatch.Expected != "string" || mismatch.Actual != test.wantActual {
				t.Fatalf("mismatch=%#v", mismatch)
			}
		})
	}
}

func TestValidateOutputContractRejectsProjectedTaskFieldDriftAcrossCommands(t *testing.T) {
	for _, test := range []struct {
		name       string
		command    string
		data       map[string]any
		fields     []string
		wantType   string
		wantFormat string
	}{
		{
			name: "events list title type", command: "events.list",
			data:   map[string]any{"events": []any{map[string]any{"event_ticker": "EVT", "title": 42}}},
			fields: []string{"title"}, wantType: "events[].title",
		},
		{
			name: "events get title type", command: "events.get",
			data:   map[string]any{"event": map[string]any{"event_ticker": "EVT", "title": 42}, "markets": []any{}},
			fields: []string{"event.title"}, wantType: "event.title",
		},
		{
			name: "series list title type", command: "series.list",
			data:   map[string]any{"series": []any{map[string]any{"ticker": "SER", "title": 42}}},
			fields: []string{"title"}, wantType: "series[].title",
		},
		{
			name: "series get title type", command: "series.get",
			data:   map[string]any{"series": map[string]any{"ticker": "SER", "title": 42}},
			fields: []string{"series.title"}, wantType: "series.title",
		},
		{
			name: "trades ticker type", command: "trades.list",
			data:   map[string]any{"trades": []any{map[string]any{"trade_id": "T1", "ticker": 42}}},
			fields: []string{"ticker"}, wantType: "trades[].ticker",
		},
		{
			name: "trades created time format", command: "trades.list",
			data:   map[string]any{"trades": []any{map[string]any{"trade_id": "T1", "created_time": "tomorrow"}}},
			fields: []string{"created_time"}, wantFormat: "trades[].created_time",
		},
		{
			name: "orderbook yes levels type", command: "orderbook.get",
			data:   map[string]any{"orderbook_fp": map[string]any{"yes_dollars": map[string]any{}}},
			fields: []string{"orderbook_fp.yes_dollars"}, wantType: "orderbook_fp.yes_dollars",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, ok := registry.ByName(test.command)
			if !ok {
				t.Fatalf("%s is not registered", test.command)
			}
			err := validateOutputContract(command, test.data, test.fields, test.fields)
			var contractErr *responseContractError
			if !errors.As(err, &contractErr) {
				t.Fatalf("err=%T %v", err, err)
			}
			if test.wantType != "" && (len(contractErr.TypeMismatches) != 1 || contractErr.TypeMismatches[0].Field != test.wantType) {
				t.Fatalf("type mismatches=%#v, want field %q", contractErr.TypeMismatches, test.wantType)
			}
			if test.wantFormat != "" && (len(contractErr.FormatMismatches) != 1 || contractErr.FormatMismatches[0].Field != test.wantFormat) {
				t.Fatalf("format mismatches=%#v, want field %q", contractErr.FormatMismatches, test.wantFormat)
			}
		})
	}
}

func TestValidateCursorAliasesIgnoresEmptyAliasAndRejectsNonemptyAlias(t *testing.T) {
	command, _ := registry.ByName("markets.list")
	if err := validateCursorAliases(command.ResponseSchema, map[string]any{"markets": []any{}, "next_cursor": ""}); err != nil {
		t.Fatalf("empty cursor alias should be terminal: %v", err)
	}
	err := validateCursorAliases(command.ResponseSchema, map[string]any{"markets": []any{}, "next_cursor": "page-2"})
	var contractErr *responseContractError
	if !errors.As(err, &contractErr) {
		t.Fatalf("err=%T %v", err, err)
	}
	if !reflect.DeepEqual(contractErr.MissingFields, []string{"cursor"}) || !reflect.DeepEqual(contractErr.UnexpectedFields, []string{"next_cursor"}) {
		t.Fatalf("contract error=%#v", contractErr)
	}
}

func TestMatchesRFC3339AcceptsStandardsValidCaseAndLeapSecond(t *testing.T) {
	for _, value := range []string{
		"2026-08-01T12:00:00Z",
		"2026-08-01t12:00:00z",
		"1990-12-31T23:59:60Z",
		"2026-08-01T12:00:00.123456-07:00",
	} {
		if !matchesRFC3339(value) {
			t.Errorf("valid RFC 3339 date-time rejected: %q", value)
		}
	}
	for _, value := range []string{"tomorrow", "2026-02-30T12:00:00Z", "2026-08-01 12:00:00Z", "2026-08-01T12:00:61Z"} {
		if matchesRFC3339(value) {
			t.Errorf("invalid RFC 3339 date-time accepted: %q", value)
		}
	}
}

func TestMatchesFixedPointFormats(t *testing.T) {
	for _, value := range []string{"0.00", "10.50", "-2.00"} {
		if !matchesResponseFormat(value, "fixed-point-count") {
			t.Errorf("valid fixed-point count rejected: %q", value)
		}
	}
	for _, value := range []string{"0", "0.5600", "-12.123456"} {
		if !matchesResponseFormat(value, "fixed-point-dollars") {
			t.Errorf("valid fixed-point dollars rejected: %q", value)
		}
	}
	for _, value := range []string{"2", "2.0", "2.000", "2e3", "NaN", "+2.00"} {
		if matchesResponseFormat(value, "fixed-point-count") {
			t.Errorf("invalid fixed-point count accepted: %q", value)
		}
	}
	for _, value := range []string{".5", "1.", "1.1234567", "2e3", "NaN", "+1.00"} {
		if matchesResponseFormat(value, "fixed-point-dollars") {
			t.Errorf("invalid fixed-point dollars accepted: %q", value)
		}
	}
}
