package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"kalshi-agent-cli/internal/registry"
)

type options struct {
	ParamsRaw               string
	Format                  string
	Pretty                  bool
	MaxPages                int
	MaxItems                int
	MaxBytes                int
	Timeout                 time.Duration
	DryRun                  bool
	Environment             string
	EnvironmentExplicit     bool
	CredentialsFile         string
	Authenticated           bool
	WritePolicy             string
	Confirm                 string
	MaxOrderCount           string
	MaxOrderNotionalDollars string
	Help                    bool
}

func defaultOptions(isTTY bool) options {
	return options{
		Format: "json", Pretty: isTTY, MaxPages: 1, MaxItems: 100,
		MaxBytes: 1 << 20, Timeout: 30 * time.Second, Environment: "demo",
		WritePolicy: "deny", MaxOrderCount: "10.00", MaxOrderNotionalDollars: "10.00",
	}
}

func parseOptions(cmd *registry.Command, args []string, isTTY bool) (options, map[string]string, error) {
	opts := defaultOptions(isTTY)
	params := map[string]string{}
	seenGlobal := map[string]bool{}
	prettySet, compactSet := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return opts, nil, fmt.Errorf("unexpected positional argument %q", arg)
		}
		nameValue := strings.TrimPrefix(arg, "--")
		if nameValue == "" {
			return opts, nil, errors.New("bare -- is not supported")
		}
		name, value, hasValue := nameValue, "", false
		if idx := strings.IndexByte(nameValue, '='); idx >= 0 {
			name, value, hasValue = nameValue[:idx], nameValue[idx+1:], true
		}
		canonical := strings.ReplaceAll(name, "-", "_")
		isBool := globalBool(name)
		if cmd != nil {
			if field, ok := cmd.ParamsSchema.Properties[canonical]; ok && field.Type == "boolean" {
				isBool = true
			}
		}
		if !hasValue {
			if isBool {
				value, hasValue = "true", true
			} else {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					return opts, nil, fmt.Errorf("--%s requires a value", name)
				}
				i++
				value, hasValue = args[i], true
			}
		}
		if key := canonicalGlobal(name); key != "" {
			if seenGlobal[key] {
				return opts, nil, fmt.Errorf("--%s was repeated", name)
			}
			seenGlobal[key] = true
		}
		switch name {
		case "params":
			opts.ParamsRaw = value
		case "output":
			opts.Format = value
		case "pretty":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return opts, nil, errors.New("--pretty must be true or false")
			}
			opts.Pretty, prettySet = v, v
		case "compact":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return opts, nil, errors.New("--compact must be true or false")
			}
			if v {
				opts.Pretty = false
			}
			compactSet = v
		case "ndjson":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return opts, nil, errors.New("--ndjson must be true or false")
			}
			if v {
				opts.Format = "ndjson"
			}
		case "max-pages":
			n, err := boundedInt(name, value, 1, 10)
			if err != nil {
				return opts, nil, err
			}
			opts.MaxPages = n
		case "max-items":
			n, err := boundedInt(name, value, 1, 1000)
			if err != nil {
				return opts, nil, err
			}
			opts.MaxItems = n
		case "max-bytes":
			n, err := boundedInt(name, value, 1024, 8<<20)
			if err != nil {
				return opts, nil, err
			}
			opts.MaxBytes = n
		case "timeout":
			d, err := time.ParseDuration(value)
			if err != nil || d < time.Second || d > 60*time.Second {
				return opts, nil, errors.New("--timeout must be between 1s and 60s")
			}
			opts.Timeout = d
		case "dry-run":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return opts, nil, errors.New("--dry-run must be true or false")
			}
			opts.DryRun = v
		case "environment", "env":
			opts.Environment, opts.EnvironmentExplicit = value, true
		case "credentials-file":
			opts.CredentialsFile = value
		case "authenticated":
			v, err := strconv.ParseBool(value)
			if err != nil {
				return opts, nil, errors.New("--authenticated must be true or false")
			}
			opts.Authenticated = v
		case "write-policy":
			opts.WritePolicy = value
		case "confirm":
			opts.Confirm = value
		case "max-order-count":
			opts.MaxOrderCount = value
		case "max-order-notional-dollars":
			opts.MaxOrderNotionalDollars = value
		case "help":
			opts.Help = true
		default:
			if cmd == nil {
				return opts, nil, fmt.Errorf("unknown flag --%s", name)
			}
			if _, ok := cmd.ParamsSchema.Properties[canonical]; !ok {
				return opts, nil, fmt.Errorf("unknown flag --%s", name)
			}
			if _, duplicate := params[canonical]; duplicate {
				return opts, nil, fmt.Errorf("flag --%s was repeated", name)
			}
			params[canonical] = value
		}
	}
	if opts.Format != "json" && opts.Format != "ndjson" {
		return opts, nil, errors.New("--output must be json or ndjson")
	}
	if prettySet && compactSet {
		return opts, nil, errors.New("--pretty and --compact are mutually exclusive")
	}
	if opts.Format == "ndjson" && (prettySet || compactSet) {
		return opts, nil, errors.New("--ndjson cannot be combined with --pretty or --compact")
	}
	if opts.Format == "ndjson" {
		opts.Pretty = false
	}
	if opts.Environment != "demo" && opts.Environment != "production" {
		return opts, nil, errors.New("--environment must be demo or production")
	}
	if opts.WritePolicy != "deny" && opts.WritePolicy != "demo-only" && opts.WritePolicy != "allow" {
		return opts, nil, errors.New("--write-policy must be deny, demo-only, or allow")
	}
	return opts, params, nil
}

func globalBool(name string) bool {
	switch name {
	case "pretty", "compact", "ndjson", "dry-run", "authenticated", "help":
		return true
	default:
		return false
	}
}

func canonicalGlobal(name string) string {
	switch name {
	case "env":
		return "environment"
	case "params", "output", "pretty", "compact", "ndjson", "max-pages", "max-items", "max-bytes", "timeout", "dry-run", "environment", "credentials-file", "authenticated", "write-policy", "confirm", "max-order-count", "max-order-notional-dollars", "help":
		return name
	default:
		return ""
	}
}

func boundedInt(name, raw string, min, max int) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("--%s must be between %d and %d", name, min, max)
	}
	return n, nil
}

func globalOptionSchema() map[string]any {
	return map[string]any{
		"params":                     map[string]any{"type": "string", "description": "Strict JSON object; unknown and duplicate keys are rejected."},
		"output":                     map[string]any{"type": "string", "enum": []string{"json", "ndjson"}, "default": "json"},
		"pretty":                     map[string]any{"type": "boolean", "description": "Pretty-print JSON."},
		"compact":                    map[string]any{"type": "boolean", "description": "Compact JSON."},
		"ndjson":                     map[string]any{"type": "boolean", "description": "Emit collection items plus a final summary record."},
		"max_pages":                  map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "default": 1},
		"max_items":                  map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "default": 100},
		"max_bytes":                  map[string]any{"type": "integer", "minimum": 1024, "maximum": 8 << 20, "default": 1 << 20},
		"timeout":                    map[string]any{"type": "string", "default": "30s", "minimum": "1s", "maximum": "60s"},
		"environment":                map[string]any{"type": "string", "enum": []string{"demo", "production"}, "default": "demo"},
		"credentials_file":           map[string]any{"type": "string", "description": "Path only; secret key material is never accepted in argv."},
		"authenticated":              map[string]any{"type": "boolean", "description": "Sign an auth-optional read such as orderbook.get."},
		"dry_run":                    map[string]any{"type": "boolean", "description": "Plan locally with zero credential loading and zero network."},
		"write_policy":               map[string]any{"type": "string", "enum": []string{"deny", "demo-only", "allow"}, "default": "deny"},
		"confirm":                    map[string]any{"type": "string", "description": "Exact sha256 plan digest emitted by --dry-run."},
		"max_order_count":            map[string]any{"type": "string", "default": "10.00"},
		"max_order_notional_dollars": map[string]any{"type": "string", "default": "10.00"},
	}
}
