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
	})
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
	if err := validateOutputContract(command, map[string]any{"markets": []any{}, "cursor": ""}); err != nil {
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
			err := validateOutputContract(command, map[string]any{"markets": []any{map[string]any{"ticker": test.ticker}}, "cursor": ""})
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
