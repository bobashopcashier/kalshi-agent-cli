package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/bobashopcashier/kalshi-cli/internal/api"
	"github.com/bobashopcashier/kalshi-cli/internal/registry"
)

type plan struct {
	SchemaVersion string            `json:"schema_version"`
	Command       string            `json:"command"`
	Environment   string            `json:"environment"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Query         map[string]string `json:"query"`
	Body          any               `json:"body,omitempty"`
	Effect        registry.Effect   `json:"effect"`
	Policy        policySummary     `json:"policy"`
}

type policySummary struct {
	Mode                    string `json:"mode"`
	Allowed                 bool   `json:"allowed"`
	MaxOrderCount           string `json:"max_order_count"`
	MaxOrderNotionalDollars string `json:"max_order_notional_dollars"`
}

func makePlan(cmd registry.Command, params map[string]any, opts options) (plan, string, error) {
	if err := validateReadBounds(cmd, params, opts); err != nil {
		return plan{}, "", err
	}
	req, err := buildRequest(cmd, params, "", 0)
	if err != nil {
		return plan{}, "", err
	}
	policy, err := evaluatePolicy(cmd, params, opts)
	if err != nil {
		return plan{}, "", err
	}
	query := map[string]string{}
	for key, values := range req.Query {
		if len(values) > 0 {
			query[key] = values[0]
		}
	}
	p := plan{
		SchemaVersion: "kalshi.plan/v1", Command: cmd.Name, Environment: opts.Environment,
		Method: req.Method, Path: req.Path, Query: query, Body: req.Body, Effect: cmd.Effect, Policy: policy,
	}
	canonical, err := json.Marshal(p)
	if err != nil {
		return plan{}, "", err
	}
	hash := sha256.Sum256(canonical)
	return p, "sha256:" + hex.EncodeToString(hash[:]), nil
}

func validateReadBounds(cmd registry.Command, params map[string]any, opts options) error {
	if cmd.Name != "candlesticks.get" && cmd.Name != "candlesticks.historical" {
		return nil
	}
	maximum, ok := candlestickRangeMaximum(params)
	if !ok {
		return nil // Cross-field schema validation reports these first.
	}
	if maximum > int64(opts.MaxItems) {
		return fmt.Errorf("requested range can contain %d candlesticks and exceeds --max-items=%d; narrow the range or raise the limit", maximum, opts.MaxItems)
	}
	return nil
}

func candlestickRangeMaximum(params map[string]any) (int64, bool) {
	start, startOK := params["start_ts"].(int64)
	end, endOK := params["end_ts"].(int64)
	periodMinutes, periodOK := params["period_interval"].(int64)
	if !startOK || !endOK || !periodOK || start > end || periodMinutes <= 0 {
		return 0, false
	}
	maximum := (end-start)/(periodMinutes*60) + 1
	if include, _ := params["include_latest_before_start"].(bool); include {
		maximum++
	}
	return maximum, true
}

func evaluatePolicy(cmd registry.Command, params map[string]any, opts options) (policySummary, error) {
	policy := policySummary{
		Mode: opts.WritePolicy, MaxOrderCount: opts.MaxOrderCount,
		MaxOrderNotionalDollars: opts.MaxOrderNotionalDollars,
	}
	if !cmd.Effect.Mutation {
		policy.Allowed = true
		return policy, nil
	}
	policy.Allowed = opts.WritePolicy == "allow" || (opts.WritePolicy == "demo-only" && opts.Environment == "demo")
	if opts.Environment == "production" && !opts.EnvironmentExplicit {
		policy.Allowed = false
	}
	if cmd.Name != "orders.create" {
		return policy, nil
	}
	maxCount, ok := parsePositiveDecimal(opts.MaxOrderCount)
	if !ok {
		return policy, errors.New("--max-order-count must be a positive plain decimal")
	}
	maxNotional, ok := parsePositiveDecimal(opts.MaxOrderNotionalDollars)
	if !ok {
		return policy, errors.New("--max-order-notional-dollars must be a positive plain decimal")
	}
	count, _ := new(big.Rat).SetString(params["count"].(string))
	price, _ := new(big.Rat).SetString(params["price"].(string))
	if count.Cmp(maxCount) > 0 {
		return policy, fmt.Errorf("order count %s exceeds policy maximum %s", params["count"], opts.MaxOrderCount)
	}
	notional := new(big.Rat).Mul(count, price)
	if notional.Cmp(maxNotional) > 0 {
		return policy, fmt.Errorf("order notional exceeds policy maximum %s dollars", opts.MaxOrderNotionalDollars)
	}
	return policy, nil
}

func parsePositiveDecimal(raw string) (*big.Rat, bool) {
	if raw == "" || strings.HasPrefix(raw, "+") || strings.ContainsAny(raw, "eE/") {
		return nil, false
	}
	for _, r := range raw {
		if (r < '0' || r > '9') && r != '.' {
			return nil, false
		}
	}
	if strings.Count(raw, ".") > 1 {
		return nil, false
	}
	value, ok := new(big.Rat).SetString(raw)
	return value, ok && value.Sign() > 0
}

func buildRequest(cmd registry.Command, params map[string]any, cursor string, pageLimit int) (api.Request, error) {
	path := cmd.Path
	query := url.Values{}
	body := map[string]any{}
	for name, value := range params {
		field := cmd.ParamsSchema.Properties[name]
		switch field.Location {
		case "path":
			path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(fmt.Sprint(value)))
		case "query":
			query.Set(name, scalarString(value))
		case "body":
			body[name] = value
		case "local":
		default:
			return api.Request{}, fmt.Errorf("unsupported parameter location %q", field.Location)
		}
	}
	if cursor != "" {
		query.Set(cmd.ResponseSchema.CursorField, cursor)
	}
	if pageLimit > 0 {
		query.Set("limit", fmt.Sprint(pageLimit))
	}
	if strings.Contains(path, "{") {
		return api.Request{}, errors.New("unresolved path parameter")
	}
	var requestBody any
	if len(body) > 0 {
		requestBody = body
	}
	return api.Request{Method: cmd.Method, Path: path, Query: query, Body: requestBody, Auth: cmd.Effect.AuthRequired}, nil
}

func scalarString(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v)
	}
}
