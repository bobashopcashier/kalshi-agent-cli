package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"kalshi-cli/internal/api"
	"kalshi-cli/internal/auth"
	"kalshi-cli/internal/contract"
	"kalshi-cli/internal/registry"
	"kalshi-cli/internal/sanitize"
)

const (
	demoBaseURL       = "https://external-api.demo.kalshi.co/trade-api/v2"
	productionBaseURL = "https://external-api.kalshi.com/trade-api/v2"
	maxCursorBytes    = 64 << 10
)

type Config struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Version     string
	IsTTY       bool
	HTTP        api.Doer
	BaseURL     string
	Env         func(string) string
	Now         func() time.Time
	Credentials func() (auth.Credentials, error)
}

type App struct{ cfg Config }

func New(cfg Config) *App {
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	return &App{cfg: cfg}
}

type cliError struct {
	Exit           int
	Code           string
	Message        string
	Retryable      bool
	HTTPStatus     int
	Details        any
	Attempted      bool
	MutationStatus string
}

func (e *cliError) Error() string { return e.Message }

func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		opts := defaultOptions(a.cfg.IsTTY)
		env := a.successEnvelope("version", registry.Effect{Class: "local"}, opts, map[string]any{"version": a.cfg.Version}, "not_applicable")
		if err := render(a.cfg.Stdout, &env, opts, ""); err != nil {
			return a.emitRenderFailure("version", opts, registry.Effect{Class: "local"}, false, "not_applicable", err)
		}
		return contract.ExitOK
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help")) {
		return a.runHelp(args)
	}
	if len(args) >= 2 && args[0] == "commands" {
		return a.runCommands(args[1:])
	}
	if len(args) < 2 {
		return a.emitError("unknown", defaultOptions(a.cfg.IsTTY), registry.Effect{}, &cliError{Exit: contract.ExitUsage, Code: "UNKNOWN_COMMAND", Message: "commands use two words; run `kalshi commands list`"})
	}
	cmd, ok := registry.ByPath(args[:2])
	if !ok {
		return a.emitError(strings.Join(args[:2], "."), defaultOptions(a.cfg.IsTTY), registry.Effect{}, &cliError{Exit: contract.ExitUsage, Code: "UNKNOWN_COMMAND", Message: "unknown command; run `kalshi commands list`"})
	}
	opts, flagParams, err := parseOptions(&cmd, args[2:], a.cfg.IsTTY)
	if err != nil {
		return a.emitError(cmd.Name, defaultOptions(a.cfg.IsTTY), cmd.Effect, usageError(err))
	}
	if len(opts.Fields) > 0 && (opts.Help || opts.DryRun || cmd.Effect.Mutation) {
		return a.emitError(cmd.Name, opts, cmd.Effect, usageError(errors.New("--fields is available only for successful read results and discovery commands")))
	}
	if opts.Help {
		localEffect := registry.Effect{Class: "local"}
		env := a.successEnvelope(cmd.Name, localEffect, opts, cmd, "not_applicable")
		if err := render(a.cfg.Stdout, &env, opts, ""); err != nil {
			return a.emitRenderFailure(cmd.Name, opts, localEffect, false, "not_applicable", err)
		}
		return contract.ExitOK
	}
	if err := validateResponseProjection(cmd, opts.Fields); err != nil {
		return a.emitError(cmd.Name, opts, cmd.Effect, &cliError{Exit: contract.ExitUsage, Code: "PROJECTION_FAILED", Message: err.Error(), MutationStatus: "not_applicable"})
	}
	params, err := normalizeParams(cmd, opts.ParamsRaw, flagParams)
	if err != nil {
		details := any(nil)
		var ve *validationError
		if errors.As(err, &ve) {
			details = map[string]any{"violations": ve.Violations}
		}
		return a.emitError(cmd.Name, opts, cmd.Effect, &cliError{Exit: contract.ExitUsage, Code: "SCHEMA_VALIDATION_FAILED", Message: err.Error(), Details: details, MutationStatus: mutationStatus(cmd, false, false)})
	}
	p, digest, err := makePlan(cmd, params, opts)
	if err != nil {
		return a.emitError(cmd.Name, opts, cmd.Effect, &cliError{Exit: contract.ExitPolicy, Code: "POLICY_LIMIT_EXCEEDED", Message: err.Error(), MutationStatus: mutationStatus(cmd, false, false)})
	}
	if opts.DryRun {
		data := map[string]any{"plan": p, "confirmation_digest": digest}
		status := mutationStatus(cmd, false, false)
		env := a.successEnvelope(cmd.Name, cmd.Effect, opts, data, status)
		if err := render(a.cfg.Stdout, &env, opts, ""); err != nil {
			return a.emitRenderFailure(cmd.Name, opts, cmd.Effect, false, status, err)
		}
		return contract.ExitOK
	}
	if cmd.Effect.Mutation {
		if !p.Policy.Allowed {
			message := "writes are denied by policy"
			if opts.Environment == "production" && !opts.EnvironmentExplicit {
				message = "production writes require an explicit --environment production"
			}
			return a.emitError(cmd.Name, opts, cmd.Effect, &cliError{Exit: contract.ExitPolicy, Code: "POLICY_DENIED", Message: message, MutationStatus: "not_attempted"})
		}
		if opts.Confirm == "" || opts.Confirm != digest {
			return a.emitError(cmd.Name, opts, cmd.Effect, &cliError{Exit: contract.ExitPolicy, Code: "CONFIRMATION_MISMATCH", Message: "run the identical command with --dry-run, review it, then pass its exact confirmation_digest via --confirm", MutationStatus: "not_attempted"})
		}
	}
	data, page, truncation, runErr := a.execute(ctx, cmd, params, opts)
	if runErr != nil {
		return a.emitError(cmd.Name, opts, cmd.Effect, runErr)
	}
	if len(opts.Fields) > 0 {
		data, err = projectResponseData(data, opts.Fields, cmd.ResponseSchema.CollectionField, cmd.ResponseSchema.CursorField)
		if err != nil {
			return a.emitError(cmd.Name, opts, cmd.Effect, &cliError{Exit: contract.ExitUsage, Code: "PROJECTION_FAILED", Message: err.Error(), Attempted: true, MutationStatus: "not_applicable"})
		}
	}
	status := mutationStatus(cmd, true, true)
	env := a.successEnvelope(cmd.Name, cmd.Effect, opts, data, status)
	env.Meta.Pagination = page
	env.Meta.Truncation = truncation
	if err := render(a.cfg.Stdout, &env, opts, cmd.ResponseSchema.CollectionField); err != nil {
		return a.emitRenderFailure(cmd.Name, opts, cmd.Effect, true, status, err)
	}
	return contract.ExitOK
}

func (a *App) runHelp(args []string) int {
	opts := defaultOptions(a.cfg.IsTTY)
	data := map[string]any{
		"usage":     "kalshi <group> <command> [--params JSON] [flags]",
		"discovery": []string{"kalshi commands list", "kalshi commands describe <command.name>"},
		"safety":    "Writes default to denied. Dry-run never loads credentials or performs network I/O.",
	}
	env := a.successEnvelope("help", registry.Effect{Class: "local"}, opts, data, "not_applicable")
	if err := render(a.cfg.Stdout, &env, opts, ""); err != nil {
		return a.emitRenderFailure("help", opts, registry.Effect{Class: "local"}, false, "not_applicable", err)
	}
	return contract.ExitOK
}

func (a *App) runCommands(args []string) int {
	name := "commands." + args[0]
	switch args[0] {
	case "list":
		opts, _, err := parseOptions(nil, args[1:], a.cfg.IsTTY)
		if err != nil {
			return a.emitError(name, defaultOptions(a.cfg.IsTTY), registry.Effect{Class: "local"}, usageError(err))
		}
		data := map[string]any{"registry_version": "kalshi.registry/v1", "commands": registry.All(), "global_options": globalOptionSchema()}
		if len(opts.Fields) > 0 {
			normalized, projectionErr := projectionMap(data)
			if projectionErr != nil {
				return a.emitError(name, opts, registry.Effect{Class: "local"}, usageError(projectionErr))
			}
			data, projectionErr = projectData(normalized, opts.Fields, "", "")
			if projectionErr != nil {
				return a.emitError(name, opts, registry.Effect{Class: "local"}, &cliError{Exit: contract.ExitUsage, Code: "PROJECTION_FAILED", Message: projectionErr.Error(), MutationStatus: "not_applicable"})
			}
		}
		env := a.successEnvelope(name, registry.Effect{Class: "local"}, opts, data, "not_applicable")
		if err := render(a.cfg.Stdout, &env, opts, ""); err != nil {
			return a.emitRenderFailure(name, opts, registry.Effect{Class: "local"}, false, "not_applicable", err)
		}
		return contract.ExitOK
	case "describe":
		if len(args) < 2 {
			return a.emitError(name, defaultOptions(a.cfg.IsTTY), registry.Effect{Class: "local"}, usageError(errors.New("commands describe requires a command.name")))
		}
		cmd, ok := registry.ByName(args[1])
		if !ok {
			return a.emitError(name, defaultOptions(a.cfg.IsTTY), registry.Effect{Class: "local"}, usageError(errors.New("unknown command name")))
		}
		opts, _, err := parseOptions(nil, args[2:], a.cfg.IsTTY)
		if err != nil {
			return a.emitError(name, defaultOptions(a.cfg.IsTTY), registry.Effect{Class: "local"}, usageError(err))
		}
		data := map[string]any{"command": cmd, "global_options": globalOptionSchema()}
		if len(opts.Fields) > 0 {
			normalized, projectionErr := projectionMap(data)
			if projectionErr != nil {
				return a.emitError(name, opts, registry.Effect{Class: "local"}, usageError(projectionErr))
			}
			data, projectionErr = projectData(normalized, opts.Fields, "", "")
			if projectionErr != nil {
				return a.emitError(name, opts, registry.Effect{Class: "local"}, &cliError{Exit: contract.ExitUsage, Code: "PROJECTION_FAILED", Message: projectionErr.Error(), MutationStatus: "not_applicable"})
			}
		}
		env := a.successEnvelope(name, registry.Effect{Class: "local"}, opts, data, "not_applicable")
		if err := render(a.cfg.Stdout, &env, opts, ""); err != nil {
			return a.emitRenderFailure(name, opts, registry.Effect{Class: "local"}, false, "not_applicable", err)
		}
		return contract.ExitOK
	default:
		return a.emitError(name, defaultOptions(a.cfg.IsTTY), registry.Effect{Class: "local"}, usageError(errors.New("expected `commands list` or `commands describe <command.name>`")))
	}
}

func (a *App) execute(ctx context.Context, cmd registry.Command, params map[string]any, opts options) (map[string]any, *contract.Pagination, contract.Truncation, *cliError) {
	baseURL := a.cfg.BaseURL
	if baseURL == "" {
		if opts.Environment == "production" {
			baseURL = productionBaseURL
		} else {
			baseURL = demoBaseURL
		}
	}
	credentialLoader := a.cfg.Credentials
	if credentialLoader == nil {
		credentialLoader = func() (auth.Credentials, error) {
			return (auth.Source{CredentialsFile: opts.CredentialsFile, Env: a.cfg.Env}).Load()
		}
	}
	client := &api.Client{BaseURL: baseURL, HTTP: a.cfg.HTTP, Credentials: credentialLoader, Now: a.cfg.Now, MaxResponseBytes: 8 << 20}
	execCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if !cmd.Paginated {
		req, err := buildRequest(cmd, params, "", 0)
		if err != nil {
			return nil, nil, contract.Truncation{}, internalError(err)
		}
		if cmd.Effect.AuthOptional {
			req.Auth = opts.Authenticated
		}
		data, err := client.Do(execCtx, req)
		if err != nil {
			return nil, nil, contract.Truncation{}, classifyExecutionError(cmd, err)
		}
		return data, nil, contract.Truncation{Reasons: []string{}}, nil
	}
	return executePages(execCtx, client, cmd, params, opts)
}

func executePages(ctx context.Context, client *api.Client, cmd registry.Command, params map[string]any, opts options) (map[string]any, *contract.Pagination, contract.Truncation, *cliError) {
	collection, cursorField := cmd.ResponseSchema.CollectionField, cmd.ResponseSchema.CursorField
	result := map[string]any{}
	items := []any{}
	cursor, _ := params[cursorField].(string)
	seen := map[string]bool{}
	if cursor != "" {
		seen[cursor] = true
	}
	pages, scanned := 0, 0
	truncation := contract.Truncation{Reasons: []string{}}
	for pages < opts.MaxPages && scanned < opts.MaxItems {
		remaining := opts.MaxItems - scanned
		pageLimit := remaining
		if configured, ok := params["limit"].(int64); ok && int(configured) < pageLimit {
			pageLimit = int(configured)
		}
		req, err := buildRequest(cmd, params, cursor, pageLimit)
		if err != nil {
			return nil, nil, truncation, internalError(err)
		}
		page, err := client.Do(ctx, req)
		if err != nil {
			return nil, nil, truncation, classifyExecutionError(cmd, err)
		}
		pages++
		if pages == 1 {
			for key, value := range page {
				if key != collection && key != cursorField {
					result[key] = value
				}
			}
		}
		pageItems, ok := page[collection].([]any)
		if !ok {
			return nil, nil, truncation, &cliError{Exit: contract.ExitUpstream, Code: "UPSTREAM_SCHEMA_MISMATCH", Message: "upstream collection field is missing or not an array", Attempted: true, MutationStatus: "not_applicable"}
		}
		if len(pageItems) > pageLimit {
			return nil, nil, truncation, &cliError{Exit: contract.ExitUpstream, Code: "UPSTREAM_SCHEMA_MISMATCH", Message: "upstream returned more collection items than the requested page limit", Attempted: true, MutationStatus: "not_applicable"}
		}
		scanned += len(pageItems)
		if cmd.LocalFilter != "" {
			target := params[cmd.LocalFilter]
			for _, item := range pageItems {
				obj, ok := item.(map[string]any)
				if ok && obj[cmd.LocalFilter] == target {
					items = append(items, item)
				}
			}
		} else {
			items = append(items, pageItems...)
		}
		next, err := validatedPageCursor(page, cursorField)
		if err != nil {
			return nil, nil, truncation, &cliError{Exit: contract.ExitUpstream, Code: "UPSTREAM_SCHEMA_MISMATCH", Message: err.Error(), Attempted: true, MutationStatus: "not_applicable"}
		}
		cursor = next
		if cursor == "" {
			break
		}
		if seen[cursor] {
			return nil, nil, truncation, &cliError{Exit: contract.ExitUpstream, Code: "CURSOR_CYCLE", Message: "upstream repeated a pagination cursor", Attempted: true, MutationStatus: "not_applicable"}
		}
		seen[cursor] = true
	}
	if cursor != "" {
		if pages >= opts.MaxPages {
			addReason(&truncation, "max_pages")
		}
		if scanned >= opts.MaxItems {
			addReason(&truncation, "max_items")
		}
	}
	result[collection] = items
	result[cursorField] = cursor
	pageMeta := &contract.Pagination{PagesFetched: pages, ItemsScanned: scanned, ItemsReturned: len(items), NextCursor: cursor}
	return result, pageMeta, truncation, nil
}

func classifyExecutionError(cmd registry.Command, err error) *cliError {
	var credentials *api.CredentialError
	if errors.As(err, &credentials) {
		return &cliError{Exit: contract.ExitAuth, Code: "CREDENTIALS_INVALID", Message: credentials.Error(), MutationStatus: mutationStatus(cmd, false, false)}
	}
	var upstream *api.UpstreamError
	if errors.As(err, &upstream) {
		exit := contract.ExitUpstream
		code := "UPSTREAM_REJECTED"
		if upstream.Status == http.StatusUnauthorized {
			exit, code = contract.ExitAuth, "AUTHENTICATION_FAILED"
		}
		status := mutationStatus(cmd, true, false)
		if cmd.Effect.Mutation && upstream.Status < 500 && upstream.Status != http.StatusConflict && !(cmd.Name == "orders.cancel" && upstream.Status == http.StatusNotFound) {
			status = "not_attempted"
		}
		return &cliError{Exit: exit, Code: code, Message: upstream.Message, Retryable: upstream.Retryable && !cmd.Effect.Mutation, HTTPStatus: upstream.Status, Details: map[string]any{"upstream_code": upstream.Code, "upstream_details": upstream.Details}, Attempted: true, MutationStatus: status}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &cliError{Exit: contract.ExitNetwork, Code: "TIMEOUT", Message: "request timed out", Retryable: !cmd.Effect.Mutation, Attempted: true, MutationStatus: mutationStatus(cmd, true, false), Details: reconcileDetails(cmd)}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return &cliError{Exit: contract.ExitNetwork, Code: "NETWORK_ERROR", Message: "network request failed", Retryable: !cmd.Effect.Mutation, Attempted: true, MutationStatus: mutationStatus(cmd, true, false), Details: reconcileDetails(cmd)}
	}
	return &cliError{Exit: contract.ExitUpstream, Code: "UPSTREAM_PROTOCOL_ERROR", Message: err.Error(), Attempted: true, MutationStatus: mutationStatus(cmd, true, false), Details: reconcileDetails(cmd)}
}

func reconcileDetails(cmd registry.Command) any {
	if !cmd.Effect.Mutation {
		return nil
	}
	return map[string]any{"outcome": "unknown", "reconciliation": cmd.Effect.Reconciliation}
}

func mutationStatus(cmd registry.Command, attempted, success bool) string {
	if !cmd.Effect.Mutation {
		return "not_applicable"
	}
	if !attempted {
		return "not_attempted"
	}
	if success {
		return "confirmed"
	}
	return "unknown"
}

func (a *App) successEnvelope(command string, effect registry.Effect, opts options, data any, status string) contract.Envelope {
	return contract.Envelope{SchemaVersion: contract.SchemaVersion, OK: true, Command: command, Effect: contractEffect(effect, opts.DryRun, !opts.DryRun && effect.Network, status), Data: data, Meta: contract.Meta{RequestID: requestID(), Environment: opts.Environment, Truncation: contract.Truncation{Reasons: []string{}}}}
}

func (a *App) emitError(command string, opts options, effect registry.Effect, problem *cliError) int {
	message := problem.Message
	if len(message) > 2048 {
		message = message[:2048]
	}
	status := problem.MutationStatus
	if status == "" {
		status = mutationStatus(registry.Command{Effect: effect}, problem.Attempted, false)
	}
	env := contract.Envelope{SchemaVersion: contract.SchemaVersion, OK: false, Command: command, Effect: contractEffect(effect, opts.DryRun, problem.Attempted, status), Error: &contract.APIError{Code: problem.Code, Message: message, Retryable: problem.Retryable, HTTPStatus: problem.HTTPStatus, Details: problem.Details}, Meta: contract.Meta{RequestID: requestID(), Environment: opts.Environment, Truncation: contract.Truncation{Reasons: []string{}}}}
	if err := render(a.cfg.Stderr, &env, opts, ""); err != nil {
		return contract.ExitOutput
	}
	return problem.Exit
}

func (a *App) emitRenderFailure(command string, opts options, effect registry.Effect, attempted bool, status string, err error) int {
	return a.emitError(command, opts, effect, &cliError{Exit: contract.ExitOutput, Code: "OUTPUT_LIMIT", Message: err.Error(), Attempted: attempted, MutationStatus: status})
}

func validatedPageCursor(page map[string]any, cursorField string) (string, error) {
	raw, exists := page[cursorField]
	if !exists || raw == nil {
		return "", nil
	}
	cursor, ok := raw.(string)
	if !ok {
		return "", errors.New("upstream pagination cursor is not a string or null")
	}
	if len(cursor) > maxCursorBytes {
		return "", errors.New("upstream pagination cursor exceeds 64 KiB")
	}
	if !utf8.ValidString(cursor) || sanitize.ContainsUnsafe(cursor) || strings.ContainsRune(cursor, utf8.RuneError) {
		return "", errors.New("upstream pagination cursor contains unsafe or invalid Unicode")
	}
	return cursor, nil
}

func contractEffect(effect registry.Effect, dryRun, network bool, status string) contract.Effect {
	return contract.Effect{Class: effect.Class, Network: network, Mutation: network && effect.Mutation && status == "confirmed", PotentialMutation: effect.Mutation, AuthRequired: effect.AuthRequired, AuthOptional: effect.AuthOptional, ConfirmationRequired: effect.ConfirmationRequired, DryRun: dryRun, MutationStatus: status}
}

func usageError(err error) *cliError {
	return &cliError{Exit: contract.ExitUsage, Code: "INVALID_ARGUMENT", Message: err.Error(), MutationStatus: "not_applicable"}
}
func internalError(err error) *cliError {
	return &cliError{Exit: contract.ExitInternal, Code: "INTERNAL_ERROR", Message: err.Error(), MutationStatus: "not_applicable"}
}

func requestID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "local-unavailable"
	}
	return "local-" + hex.EncodeToString(raw)
}
