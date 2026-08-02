package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"kalshi-cli/internal/contract"
	"kalshi-cli/internal/registry"
)

type responseTypeMismatch struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type responseFormatMismatch struct {
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type responseContractError struct {
	MissingFields    []string
	TypeMismatches   []responseTypeMismatch
	FormatMismatches []responseFormatMismatch
	UnexpectedFields []string
}

func (e *responseContractError) Error() string {
	parts := make([]string, 0, 4)
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
	if len(e.FormatMismatches) > 0 {
		items := make([]string, len(e.FormatMismatches))
		for i, mismatch := range e.FormatMismatches {
			items[i] = fmt.Sprintf("%s (expected %s, got %s)", mismatch.Field, mismatch.Expected, mismatch.Actual)
		}
		parts = append(parts, "format mismatch(es): "+strings.Join(items, ", "))
	}
	if len(e.UnexpectedFields) > 0 {
		parts = append(parts, "unexpected field(s): "+strings.Join(e.UnexpectedFields, ", "))
	}
	return strings.Join(parts, "; ")
}

func validateOutputContract(command registry.Command, data map[string]any, selectedFields, requiredFields []string) error {
	schema := command.ResponseSchema
	missingSet := map[string]bool{}
	mismatchByField := map[string]responseTypeMismatch{}
	formatMismatchByField := map[string]responseFormatMismatch{}
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

	requiredSet := make(map[string]bool, len(requiredFields))
	for _, field := range requiredFields {
		requiredSet[field] = true
	}
	contractFields := make([]string, 0, len(selectedFields)+len(requiredFields))
	seenContractFields := map[string]bool{}
	for _, fields := range [][]string{selectedFields, requiredFields} {
		for _, field := range fields {
			if !seenContractFields[field] {
				seenContractFields[field] = true
				contractFields = append(contractFields, field)
			}
		}
	}
	validateProjectedPath := func(root any, field, rendered string) {
		values, exists := responsePathValues(root, strings.Split(field, "."))
		if !exists {
			if requiredSet[field] {
				missingSet[rendered] = true
			}
			return
		}
		fieldContract, constrained := schema.ProjectedContracts[field]
		for _, value := range values {
			if requiredSet[field] && value == nil {
				missingSet[rendered] = true
				continue
			}
			if !constrained {
				continue
			}
			if !matchesResponseType(value, fieldContract.Type) {
				if _, recorded := mismatchByField[rendered]; !recorded {
					mismatchByField[rendered] = responseTypeMismatch{Field: rendered, Expected: fieldContract.Type, Actual: responseValueType(value)}
				}
				continue
			}
			if fieldContract.Format != "" && !matchesResponseFormat(value, fieldContract.Format) {
				if _, recorded := formatMismatchByField[rendered]; !recorded {
					formatMismatchByField[rendered] = responseFormatMismatch{Field: rendered, Expected: fieldContract.Format, Actual: "invalid"}
				}
			}
		}
	}
	if schema.CollectionField == "" {
		for _, field := range contractFields {
			validateProjectedPath(data, field, field)
		}
	} else if rawItems, ok := data[schema.CollectionField].([]any); ok {
		for _, field := range contractFields {
			rendered := schema.CollectionField + "[]." + field
			for _, item := range rawItems {
				validateProjectedPath(item, field, rendered)
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
	formatMismatches := make([]responseFormatMismatch, 0, len(formatMismatchByField))
	for _, mismatch := range formatMismatchByField {
		formatMismatches = append(formatMismatches, mismatch)
	}
	sort.Slice(formatMismatches, func(i, j int) bool { return formatMismatches[i].Field < formatMismatches[j].Field })
	if len(missing) == 0 && len(mismatches) == 0 && len(formatMismatches) == 0 {
		return nil
	}
	return &responseContractError{MissingFields: missing, TypeMismatches: mismatches, FormatMismatches: formatMismatches}
}

func validateCursorAliases(schema registry.ResponseSchema, data map[string]any) error {
	if schema.CursorField == "" || len(schema.CursorAliases) == 0 {
		return nil
	}
	if value, exists := data[schema.CursorField]; exists && value != nil && value != "" {
		return nil
	}
	unexpected := make([]string, 0)
	for _, alias := range schema.CursorAliases {
		value, exists := data[alias]
		if !exists || value == nil || value == "" {
			continue
		}
		unexpected = append(unexpected, alias)
	}
	if len(unexpected) == 0 {
		return nil
	}
	sort.Strings(unexpected)
	return &responseContractError{MissingFields: []string{schema.CursorField}, UnexpectedFields: unexpected}
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

func matchesResponseFormat(value any, expected string) bool {
	switch expected {
	case "date-time":
		raw, ok := value.(string)
		if !ok {
			return false
		}
		return matchesRFC3339(raw)
	default:
		return false
	}
}

func matchesRFC3339(raw string) bool {
	if len(raw) < len("2006-01-02T15:04:05Z") {
		return false
	}
	normalized := []byte(raw)
	if normalized[10] == 't' {
		normalized[10] = 'T'
	}
	if normalized[len(normalized)-1] == 'z' {
		normalized[len(normalized)-1] = 'Z'
	}
	if _, err := time.Parse(time.RFC3339, string(normalized)); err == nil {
		return true
	}
	if normalized[17] != '6' || normalized[18] != '0' {
		return false
	}
	normalized[17], normalized[18] = '5', '9'
	_, err := time.Parse(time.RFC3339, string(normalized))
	return err == nil
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
		if len(contractErr.FormatMismatches) > 0 {
			details["format_mismatches"] = contractErr.FormatMismatches
		}
		if len(contractErr.UnexpectedFields) > 0 {
			details["unexpected_fields"] = contractErr.UnexpectedFields
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
