package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"kalshi-cli/internal/contract"
	"kalshi-cli/internal/registry"
)

type responseTypeMismatch struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type responseContractError struct {
	MissingFields  []string
	TypeMismatches []responseTypeMismatch
}

func (e *responseContractError) Error() string {
	parts := make([]string, 0, 2)
	if len(e.MissingFields) > 0 {
		parts = append(parts, "missing field(s): "+strings.Join(e.MissingFields, ", "))
	}
	if len(e.TypeMismatches) > 0 {
		items := make([]string, len(e.TypeMismatches))
		for i, mismatch := range e.TypeMismatches {
			items[i] = fmt.Sprintf("%s (expected %s, got %s)", mismatch.Field, mismatch.Expected, mismatch.Actual)
		}
		parts = append(parts, "type mismatch(es): "+strings.Join(items, ", "))
	}
	return strings.Join(parts, "; ")
}

func validateOutputContract(command registry.Command, data map[string]any) error {
	schema := command.ResponseSchema
	missingSet := map[string]bool{}
	mismatchByField := map[string]responseTypeMismatch{}
	for _, name := range schema.Required {
		if _, exists := data[name]; !exists {
			missingSet[name] = true
		}
	}
	for name, field := range schema.Properties {
		value, exists := data[name]
		if !exists || matchesResponseType(value, field.Type) {
			continue
		}
		mismatchByField[name] = responseTypeMismatch{Field: name, Expected: field.Type, Actual: responseValueType(value)}
	}
	validateRequiredPath := func(root any, field, rendered string) {
		values, exists := responsePathValues(root, strings.Split(field, "."))
		if !exists {
			missingSet[rendered] = true
			return
		}
		expected := schema.RequiredFieldTypes[field]
		for _, value := range values {
			if matchesResponseType(value, expected) {
				continue
			}
			if _, recorded := mismatchByField[rendered]; !recorded {
				mismatchByField[rendered] = responseTypeMismatch{Field: rendered, Expected: expected, Actual: responseValueType(value)}
			}
		}
	}
	if schema.CollectionField == "" {
		for _, field := range schema.RequiredFields {
			validateRequiredPath(data, field, field)
		}
	} else if rawItems, ok := data[schema.CollectionField].([]any); ok {
		for _, field := range schema.RequiredFields {
			for _, item := range rawItems {
				rendered := schema.CollectionField + "[]." + field
				validateRequiredPath(item, field, rendered)
				if missingSet[rendered] {
					break
				}
			}
		}
	}
	missing := make([]string, 0, len(missingSet))
	for field := range missingSet {
		missing = append(missing, field)
	}
	sort.Strings(missing)

	mismatches := make([]responseTypeMismatch, 0, len(mismatchByField))
	for _, mismatch := range mismatchByField {
		mismatches = append(mismatches, mismatch)
	}
	sort.Slice(mismatches, func(i, j int) bool { return mismatches[i].Field < mismatches[j].Field })
	if len(missing) == 0 && len(mismatches) == 0 {
		return nil
	}
	return &responseContractError{MissingFields: missing, TypeMismatches: mismatches}
}

func responsePathValues(value any, segments []string) ([]any, bool) {
	if len(segments) == 0 {
		return []any{value}, true
	}
	switch source := value.(type) {
	case map[string]any:
		next, exists := source[segments[0]]
		if !exists {
			return nil, false
		}
		return responsePathValues(next, segments[1:])
	case []any:
		values := make([]any, 0)
		for _, item := range source {
			itemValues, exists := responsePathValues(item, segments)
			if !exists {
				return nil, false
			}
			values = append(values, itemValues...)
		}
		return values, true
	default:
		return nil, false
	}
}

func matchesResponseType(value any, expected string) bool {
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch number := value.(type) {
		case json.Number:
			_, err := strconv.ParseInt(number.String(), 10, 64)
			return err == nil
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func responseValueType(value any) string {
	if value == nil {
		return "null"
	}
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number, float32, float64:
		return "number"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func upstreamSchemaProblem(command registry.Command, err error, retry *contract.Retry) *cliError {
	details := map[string]any{"output_contract_version": command.OutputContractVersion}
	var contractErr *responseContractError
	if errors.As(err, &contractErr) {
		if len(contractErr.MissingFields) > 0 {
			details["missing_fields"] = contractErr.MissingFields
		}
		if len(contractErr.TypeMismatches) > 0 {
			details["type_mismatches"] = contractErr.TypeMismatches
		}
	}
	var missingErr *missingProjectionFieldsError
	if errors.As(err, &missingErr) {
		details["missing_fields"] = missingErr.Fields
	}
	if reconciliation, ok := reconcileDetails(command).(map[string]any); ok {
		for key, value := range reconciliation {
			details[key] = value
		}
	}
	return &cliError{
		Exit:           contract.ExitUpstream,
		Code:           "UPSTREAM_SCHEMA_MISMATCH",
		Message:        fmt.Sprintf("upstream response does not satisfy %s: %s", command.OutputContractVersion, err),
		Details:        details,
		Retry:          retry,
		Attempted:      true,
		MutationStatus: mutationStatus(command, true, false),
	}
}

func contractProgress(pages, scanned, returned int) *contract.Pagination {
	return &contract.Pagination{PagesFetched: pages, ItemsScanned: scanned, ItemsReturned: returned}
}

func withContractProgress(problem *cliError, pagination *contract.Pagination, truncation contract.Truncation) *cliError {
	if pagination != nil {
		copyPagination := *pagination
		copyPagination.NextCursor = ""
		problem.Pagination = &copyPagination
	}
	copyTruncation := truncation
	problem.Truncation = &copyTruncation
	return problem
}

func outputContractVersion(command string) string {
	if registered, ok := registry.ByName(command); ok {
		return registered.OutputContractVersion
	}
	switch command {
	case "version", "help", "commands.list", "commands.describe":
		return "kalshi.output/" + command + "/v1"
	default:
		return "kalshi.output/error/v1"
	}
}
