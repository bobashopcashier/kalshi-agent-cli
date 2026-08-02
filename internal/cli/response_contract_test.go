package cli

import (
	"errors"
	"reflect"
	"testing"

	"kalshi-cli/internal/registry"
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
