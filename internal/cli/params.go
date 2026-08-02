package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bobashopcashier/kalshi-cli/internal/registry"
	"github.com/bobashopcashier/kalshi-cli/internal/sanitize"
)

const maxParamsBytes = 64 << 10

type violation struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type validationError struct{ Violations []violation }

func (e *validationError) Error() string {
	if len(e.Violations) == 0 {
		return "schema validation failed"
	}
	return fmt.Sprintf("%s: %s", e.Violations[0].Field, e.Violations[0].Reason)
}

func normalizeParams(cmd registry.Command, raw string, flags map[string]string) (map[string]any, error) {
	params := map[string]any{}
	if raw != "" {
		if len(raw) > maxParamsBytes {
			return nil, errors.New("--params exceeds 64 KiB")
		}
		if !utf8.ValidString(raw) {
			return nil, errors.New("--params is not valid UTF-8")
		}
		decoded, err := decodeStrictJSON([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid --params JSON: %w", err)
		}
		obj, ok := decoded.(map[string]any)
		if !ok {
			return nil, errors.New("--params must be a JSON object")
		}
		params = obj
	}
	for name, value := range flags {
		if _, exists := params[name]; exists {
			return nil, fmt.Errorf("parameter %q was supplied by both --params and --%s", name, strings.ReplaceAll(name, "_", "-"))
		}
		field, ok := cmd.ParamsSchema.Properties[name]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
		parsed, err := parseFlagValue(field, value)
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", strings.ReplaceAll(name, "_", "-"), err)
		}
		params[name] = parsed
	}
	for name, field := range cmd.ParamsSchema.Properties {
		if _, ok := params[name]; !ok && field.Default != nil {
			params[name] = field.Default
		}
	}
	if err := validateSchema(cmd.ParamsSchema, params); err != nil {
		return nil, err
	}
	if err := validateCrossFields(cmd.Name, params); err != nil {
		return nil, err
	}
	return params, nil
}

func parseFlagValue(field registry.Field, raw string) (any, error) {
	switch field.Type {
	case "string":
		return raw, nil
	case "integer":
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errors.New("must be an integer")
		}
		return value, nil
	case "boolean":
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, errors.New("must be true or false")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported schema type %q", field.Type)
	}
}

func validateSchema(schema registry.Schema, params map[string]any) error {
	var problems []violation
	for name := range params {
		if _, ok := schema.Properties[name]; !ok {
			problems = append(problems, violation{Field: name, Reason: "unknown property"})
		}
	}
	for _, name := range schema.Required {
		if _, ok := params[name]; !ok {
			problems = append(problems, violation{Field: name, Reason: "is required"})
		}
	}
	for name, value := range params {
		field, ok := schema.Properties[name]
		if !ok {
			continue
		}
		switch field.Type {
		case "string":
			s, ok := value.(string)
			if !ok {
				problems = append(problems, violation{Field: name, Reason: "must be a string"})
				continue
			}
			if sanitize.ContainsUnsafe(s) {
				problems = append(problems, violation{Field: name, Reason: "contains terminal, control, invalid UTF-8, or bidi characters"})
			}
			if strings.ContainsRune(s, utf8.RuneError) {
				problems = append(problems, violation{Field: name, Reason: "contains invalid or replacement Unicode"})
			}
			if field.MinLength > 0 && utf8.RuneCountInString(s) < field.MinLength {
				problems = append(problems, violation{Field: name, Reason: fmt.Sprintf("must contain at least %d characters", field.MinLength)})
			}
			if field.MaxLength > 0 && utf8.RuneCountInString(s) > field.MaxLength {
				problems = append(problems, violation{Field: name, Reason: fmt.Sprintf("must contain at most %d characters", field.MaxLength)})
			}
			if field.Pattern != "" {
				matched, err := regexp.MatchString(field.Pattern, s)
				if err != nil || !matched {
					problems = append(problems, violation{Field: name, Reason: "does not match the required format"})
				}
			}
			if len(field.Enum) > 0 && !contains(field.Enum, s) {
				problems = append(problems, violation{Field: name, Reason: "must be one of: " + strings.Join(field.Enum, ", ")})
			}
		case "integer":
			n, ok := asInt64(value)
			if !ok {
				problems = append(problems, violation{Field: name, Reason: "must be an integer"})
				continue
			}
			params[name] = n
			if field.Minimum != nil && n < *field.Minimum {
				problems = append(problems, violation{Field: name, Reason: fmt.Sprintf("must be at least %d", *field.Minimum)})
			}
			if field.Maximum != nil && n > *field.Maximum {
				problems = append(problems, violation{Field: name, Reason: fmt.Sprintf("must be at most %d", *field.Maximum)})
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				problems = append(problems, violation{Field: name, Reason: "must be a boolean"})
			}
		default:
			problems = append(problems, violation{Field: name, Reason: "has an unsupported schema type"})
		}
	}
	if len(problems) > 0 {
		return &validationError{Violations: problems}
	}
	return nil
}

func validateCrossFields(command string, params map[string]any) error {
	var problems []violation
	switch command {
	case "orders.create":
		count, countOK := new(big.Rat).SetString(params["count"].(string))
		price, priceOK := new(big.Rat).SetString(params["price"].(string))
		if !countOK || count.Sign() <= 0 {
			problems = append(problems, violation{Field: "count", Reason: "must be greater than zero"})
		}
		if !priceOK || price.Sign() <= 0 || price.Cmp(big.NewRat(1, 1)) >= 0 {
			problems = append(problems, violation{Field: "price", Reason: "must be greater than 0 and less than 1 dollar"})
		}
		tif, _ := params["time_in_force"].(string)
		_, hasExpiration := params["expiration_time"]
		if hasExpiration && tif != "good_till_canceled" {
			problems = append(problems, violation{Field: "expiration_time", Reason: "requires time_in_force=good_till_canceled"})
		}
		postOnly, _ := params["post_only"].(bool)
		if postOnly && (tif == "fill_or_kill" || tif == "immediate_or_cancel") {
			problems = append(problems, violation{Field: "post_only", Reason: "cannot be true for fill_or_kill or immediate_or_cancel"})
		}
	case "orders.cancel":
		if index, ok := params["exchange_index"].(int64); ok && index == -1 {
			if _, ok := params["market_ticker"]; !ok {
				problems = append(problems, violation{Field: "market_ticker", Reason: "is required when exchange_index is -1"})
			}
		}
	case "markets.list", "markets.search":
		if _, ok := params["min_updated_ts"]; ok {
			for _, incompatible := range []string{"event_ticker", "status", "tickers", "min_created_ts", "max_created_ts", "min_close_ts", "max_close_ts", "min_settled_ts", "max_settled_ts"} {
				if _, exists := params[incompatible]; exists {
					problems = append(problems, violation{Field: "min_updated_ts", Reason: "is incompatible with " + incompatible})
				}
			}
			if _, hasSeries := params["series_ticker"]; hasSeries && params["mve_filter"] != "exclude" {
				problems = append(problems, violation{Field: "mve_filter", Reason: "must be exclude when min_updated_ts and series_ticker are combined"})
			}
		}
		if command == "markets.search" {
			query, _ := params["query"].(string)
			trimmed := strings.TrimSpace(query)
			if trimmed == "" {
				problems = append(problems, violation{Field: "query", Reason: "must contain a non-whitespace keyword"})
			} else if trimmed != query {
				problems = append(problems, violation{Field: "query", Reason: "must not have leading or trailing whitespace"})
			}
		}
	case "portfolio.fills":
		if min, minOK := params["min_ts"].(int64); minOK {
			if max, maxOK := params["max_ts"].(int64); maxOK && min > max {
				problems = append(problems, violation{Field: "min_ts", Reason: "must be less than or equal to max_ts"})
			}
		}
	case "candlesticks.get", "candlesticks.historical":
		start := params["start_ts"].(int64)
		end := params["end_ts"].(int64)
		if start > end {
			problems = append(problems, violation{Field: "start_ts", Reason: "must be less than or equal to end_ts"})
		}
		period := params["period_interval"].(int64)
		if period != 1 && period != 60 && period != 1440 {
			problems = append(problems, violation{Field: "period_interval", Reason: "must be one of: 1, 60, 1440"})
		}
	}
	if len(problems) > 0 {
		return &validationError{Violations: problems}
	}
	return nil
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case json.Number:
		value, err := n.Int64()
		return value, err == nil
	case float64:
		value := int64(n)
		return value, float64(value) == n
	default:
		return 0, false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func decodeStrictJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := decodeJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, err
	}
	return value, nil
}

func decodeJSONValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key is not a string")
			}
			if _, exists := obj[key]; exists {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, err := decodeJSONValue(dec)
			if err != nil {
				return nil, err
			}
			obj[key] = value
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return obj, nil
	case '[':
		var items []any
		for dec.More() {
			value, err := decodeJSONValue(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}
