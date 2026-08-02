package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"kalshi-cli/internal/api"
	"kalshi-cli/internal/contract"
)

type driftTruth string

const (
	truthCompatible driftTruth = "compatible"
	truthBreaking   driftTruth = "breaking"
)

type driftProfile string

const (
	profileExchange    driftProfile = "exchange.status"
	profileMarketsList driftProfile = "markets.list"
	profileMarketsGet  driftProfile = "markets.get"
)

type driftScenario struct {
	Name                        string
	Mutation                    string
	Truth                       driftTruth
	Profile                     driftProfile
	Body                        string
	Bodies                      []string
	Args                        []string
	ExpectedPath                string
	ExpectedProjectedMarketKeys []string
	RequireTitle                bool
	RequireCloseRFC3339         bool
	RequireNextPage             bool
	KnownGap                    bool
	AfterPartialPage            bool
}

type armResult struct {
	Accepted        bool
	OracleOK        bool
	ValidOutput     bool
	DriftDetected   bool
	DiagnosticPaths []string
	Atomic          bool
	Versioned       bool
	OutputBytes     int
}

type armSummary struct {
	Arm                    string  `json:"arm"`
	CompatibleCases        int     `json:"compatible_cases"`
	BreakingCases          int     `json:"breaking_cases"`
	ContractBreakingCases  int     `json:"contract_breaking_cases"`
	KnownGapCases          int     `json:"known_gap_cases"`
	CorrectCompatible      int     `json:"correct_compatible"`
	FalsePositives         int     `json:"false_positives"`
	InvalidCompatible      int     `json:"invalid_compatible_successes"`
	DetectedBreaking       int     `json:"detected_breaking"`
	DetectedContractBreaks int     `json:"detected_contract_breaks"`
	DetectedKnownGaps      int     `json:"detected_known_gaps"`
	SilentWrongBreaking    int     `json:"silent_wrong_breaking"`
	OtherBreakingFailures  int     `json:"other_breaking_failures"`
	UnexpectedCorrect      int     `json:"unexpected_correct_breaking"`
	ExpectedPathPresent    int     `json:"expected_path_present"`
	PartialPageBreaks      int     `json:"partial_page_breaks"`
	AtomicPartialFailures  int     `json:"atomic_partial_failures"`
	VersionedOutputs       int     `json:"versioned_outputs"`
	DetectionRate          float64 `json:"detection_rate"`
	ContractDetectionRate  float64 `json:"contract_detection_rate"`
	KnownGapDetectionRate  float64 `json:"known_gap_detection_rate"`
	SilentWrongRate        float64 `json:"silent_wrong_rate"`
	CompatibleSuccessRate  float64 `json:"compatible_success_rate"`
	FalsePositiveRate      float64 `json:"false_positive_rate"`
	PathPresenceRate       float64 `json:"path_presence_rate"`
}

func TestSchemaDriftBenchmark(t *testing.T) {
	scenarios := schemaDriftScenarios()
	arms := []struct {
		name string
		run  func(driftScenario) armResult
	}{
		{name: "direct-api-json", run: runDirectJSON},
		{name: "direct-api-task-validator", run: runDirectValidated},
		{name: "kalshi-cli", run: runCLIContract},
	}

	summaries := make([]armSummary, 0, len(arms))
	for _, arm := range arms {
		summary := armSummary{Arm: arm.name}
		for _, scenario := range scenarios {
			result := arm.run(scenario)
			if result.Versioned {
				summary.VersionedOutputs++
			}
			t.Logf("scenario=%s arm=%s outcome=%s diagnostics=%v bytes=%d versioned=%t",
				scenario.Name, arm.name, classifyOutcome(scenario, result), result.DiagnosticPaths, result.OutputBytes, result.Versioned)
			if scenario.Truth == truthCompatible {
				summary.CompatibleCases++
				if result.Accepted && result.OracleOK && result.ValidOutput {
					summary.CorrectCompatible++
				} else if !result.Accepted {
					summary.FalsePositives++
				} else {
					summary.InvalidCompatible++
				}
				continue
			}

			summary.BreakingCases++
			if scenario.KnownGap {
				summary.KnownGapCases++
			} else {
				summary.ContractBreakingCases++
			}
			if scenario.AfterPartialPage {
				summary.PartialPageBreaks++
			}
			if result.DriftDetected {
				summary.DetectedBreaking++
				if scenario.KnownGap {
					summary.DetectedKnownGaps++
				} else {
					summary.DetectedContractBreaks++
				}
				if containsDiagnostic(result.DiagnosticPaths, scenario.ExpectedPath) {
					summary.ExpectedPathPresent++
				}
				if scenario.AfterPartialPage && result.Atomic {
					summary.AtomicPartialFailures++
				}
			} else if result.Accepted && (!result.OracleOK || !result.ValidOutput) {
				summary.SilentWrongBreaking++
			} else if result.Accepted {
				summary.UnexpectedCorrect++
			} else {
				summary.OtherBreakingFailures++
			}
		}
		summary.DetectionRate = ratio(summary.DetectedBreaking, summary.BreakingCases)
		summary.ContractDetectionRate = ratio(summary.DetectedContractBreaks, summary.ContractBreakingCases)
		summary.KnownGapDetectionRate = ratio(summary.DetectedKnownGaps, summary.KnownGapCases)
		summary.SilentWrongRate = ratio(summary.SilentWrongBreaking, summary.BreakingCases)
		summary.CompatibleSuccessRate = ratio(summary.CorrectCompatible, summary.CompatibleCases)
		summary.FalsePositiveRate = ratio(summary.FalsePositives, summary.CompatibleCases)
		summary.PathPresenceRate = ratio(summary.ExpectedPathPresent, summary.DetectedBreaking)
		summaries = append(summaries, summary)
	}

	raw, err := json.Marshal(summaries)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SCHEMA_DRIFT_BENCHMARK_JSON=%s", raw)
	for _, summary := range summaries {
		t.Logf("arm=%s compatible=%d/%d contract_breaks_detected=%d/%d known_gaps_detected=%d/%d silent_wrong=%d/%d expected_path_present=%d/%d false_positives=%d/%d",
			summary.Arm,
			summary.CorrectCompatible, summary.CompatibleCases,
			summary.DetectedContractBreaks, summary.ContractBreakingCases,
			summary.DetectedKnownGaps, summary.KnownGapCases,
			summary.SilentWrongBreaking, summary.BreakingCases,
			summary.ExpectedPathPresent, summary.DetectedBreaking,
			summary.FalsePositives, summary.CompatibleCases,
		)
	}

	assertBenchmarkInvariants(t, summaries, len(scenarios))
}

func assertBenchmarkInvariants(t *testing.T, summaries []armSummary, scenarioCount int) {
	t.Helper()
	if len(summaries) != 3 {
		t.Fatalf("benchmark arms=%d, want 3", len(summaries))
	}
	for _, summary := range summaries {
		if summary.CompatibleCases != 10 || summary.BreakingCases != 20 || summary.ContractBreakingCases != 16 || summary.KnownGapCases != 4 {
			t.Fatalf("arm=%s unexpected corpus partition: %+v", summary.Arm, summary)
		}
		compatiblePartition := summary.CorrectCompatible + summary.FalsePositives + summary.InvalidCompatible
		if compatiblePartition != summary.CompatibleCases {
			t.Fatalf("arm=%s compatible partition=%d/%d", summary.Arm, compatiblePartition, summary.CompatibleCases)
		}
		breakingPartition := summary.DetectedBreaking + summary.SilentWrongBreaking + summary.OtherBreakingFailures + summary.UnexpectedCorrect
		if breakingPartition != summary.BreakingCases {
			t.Fatalf("arm=%s breaking partition=%d/%d", summary.Arm, breakingPartition, summary.BreakingCases)
		}
	}

	raw, validator, cliSummary := summaries[0], summaries[1], summaries[2]
	if raw.CorrectCompatible != 10 || raw.DetectedBreaking != 0 || raw.SilentWrongBreaking != 20 || raw.FalsePositives != 0 || raw.InvalidCompatible != 0 {
		t.Fatalf("unexpected unvalidated decoder result: %+v", raw)
	}
	if validator.CorrectCompatible != 10 || validator.DetectedBreaking != 20 || validator.ExpectedPathPresent != 20 || validator.FalsePositives != 0 || validator.InvalidCompatible != 0 {
		t.Fatalf("unexpected oracle-validator result: %+v", validator)
	}
	if cliSummary.CorrectCompatible != 10 || cliSummary.FalsePositives != 0 || cliSummary.InvalidCompatible != 0 {
		t.Fatalf("unexpected CLI compatible result: %+v", cliSummary)
	}
	if cliSummary.DetectedContractBreaks != cliSummary.ContractBreakingCases || cliSummary.OtherBreakingFailures != 0 || cliSummary.UnexpectedCorrect != 0 {
		t.Fatalf("unexpected CLI breaking result: %+v", cliSummary)
	}
	if cliSummary.DetectedKnownGaps != cliSummary.KnownGapCases || cliSummary.DetectedBreaking != cliSummary.BreakingCases || cliSummary.SilentWrongBreaking != 0 {
		t.Fatalf("CLI did not contain every extended drift case: %+v", cliSummary)
	}
	if cliSummary.ExpectedPathPresent != cliSummary.DetectedBreaking {
		t.Fatalf("CLI expected-path presence=%d/%d", cliSummary.ExpectedPathPresent, cliSummary.DetectedBreaking)
	}
	if cliSummary.AtomicPartialFailures != cliSummary.PartialPageBreaks || cliSummary.PartialPageBreaks != 1 {
		t.Fatalf("CLI atomic partial-page failures=%d/%d", cliSummary.AtomicPartialFailures, cliSummary.PartialPageBreaks)
	}
	if cliSummary.VersionedOutputs != scenarioCount {
		t.Fatalf("CLI exact versioned envelopes=%d/%d", cliSummary.VersionedOutputs, scenarioCount)
	}
}

func schemaDriftScenarios() []driftScenario {
	marketList := []string{"markets", "list", "--max-pages", "2", "--max-items", "10", "--compact"}
	marketGet := []string{"markets", "get", "--ticker", "A", "--compact"}
	exchange := []string{"exchange", "status", "--compact"}
	return []driftScenario{
		{Name: "markets-valid", Mutation: "none", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":"Alpha"}],"cursor":""}`, Args: marketList},
		{Name: "markets-additive-field", Mutation: "add optional field", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":"Alpha","new_field":true}],"cursor":"","new_top_level":"ok"}`, Args: marketList},
		{Name: "markets-additive-field-projected", Mutation: "add field outside requested projection", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":"Alpha","new_field":true}],"cursor":"","new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(marketList, "ticker,title"), "title"), ExpectedProjectedMarketKeys: []string{"ticker", "title"}},
		{Name: "markets-key-reorder", Mutation: "reorder keys", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"cursor":"","markets":[{"title":"Alpha","ticker":"A"}]}`, Args: marketList},
		{Name: "markets-empty", Mutation: "empty collection", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[],"cursor":""}`, Args: marketList},
		{Name: "markets-cursor-null", Mutation: "terminal cursor null", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"cursor":null}`, Args: marketList},
		{Name: "markets-cursor-absent-terminal", Mutation: "terminal cursor absent", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}]}`, Args: marketList},
		{Name: "markets-close-time-valid", Mutation: "valid same-type semantic value", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","close_time":"2026-08-01T12:00:00Z"}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,close_time"), "close_time"), RequireCloseRFC3339: true},
		{Name: "market-get-valid", Mutation: "none", Truth: truthCompatible, Profile: profileMarketsGet, Body: `{"market":{"ticker":"A","title":"Alpha"}}`, Args: marketGet},
		{Name: "exchange-valid", Mutation: "none", Truth: truthCompatible, Profile: profileExchange, Body: `{"exchange_active":true,"trading_active":true}`, Args: exchange},

		{Name: "markets-wrapper-missing", Mutation: "remove required wrapper", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"cursor":""}`, Args: marketList, ExpectedPath: "markets"},
		{Name: "markets-wrapper-wrong-type", Mutation: "change wrapper type", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":{},"cursor":""}`, Args: marketList, ExpectedPath: "markets"},
		{Name: "markets-ticker-missing", Mutation: "remove required item field", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"title":"Alpha"}],"cursor":""}`, Args: marketList, ExpectedPath: "markets[].ticker"},
		{Name: "markets-second-ticker-missing", Mutation: "remove required field from one item", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"},{"title":"Beta"}],"cursor":""}`, Args: marketList, ExpectedPath: "markets[].ticker"},
		{Name: "markets-ticker-null", Mutation: "set required item field null", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":null}],"cursor":""}`, Args: marketList, ExpectedPath: "markets[].ticker"},
		{Name: "markets-ticker-number", Mutation: "change required item type", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":42}],"cursor":""}`, Args: marketList, ExpectedPath: "markets[].ticker"},
		{Name: "markets-non-object-item", Mutation: "change item shape", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":["A"],"cursor":""}`, Args: marketList, ExpectedPath: "markets[].ticker"},
		{Name: "markets-cursor-number", Mutation: "change cursor type", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"cursor":42}`, Args: marketList, ExpectedPath: "cursor"},
		{Name: "market-wrapper-missing", Mutation: "remove singleton wrapper", Truth: truthBreaking, Profile: profileMarketsGet, Body: `{}`, Args: marketGet, ExpectedPath: "market"},
		{Name: "market-wrapper-null", Mutation: "set singleton wrapper null", Truth: truthBreaking, Profile: profileMarketsGet, Body: `{"market":null}`, Args: marketGet, ExpectedPath: "market"},
		{Name: "market-ticker-missing", Mutation: "remove singleton identity", Truth: truthBreaking, Profile: profileMarketsGet, Body: `{"market":{"title":"Alpha"}}`, Args: marketGet, ExpectedPath: "market.ticker"},
		{Name: "market-ticker-number", Mutation: "change singleton identity type", Truth: truthBreaking, Profile: profileMarketsGet, Body: `{"market":{"ticker":42}}`, Args: marketGet, ExpectedPath: "market.ticker"},
		{Name: "exchange-field-missing", Mutation: "remove required scalar", Truth: truthBreaking, Profile: profileExchange, Body: `{"exchange_active":true}`, Args: exchange, ExpectedPath: "trading_active"},
		{Name: "exchange-field-null", Mutation: "set required scalar null", Truth: truthBreaking, Profile: profileExchange, Body: `{"exchange_active":true,"trading_active":null}`, Args: exchange, ExpectedPath: "trading_active"},
		{Name: "exchange-field-string", Mutation: "change required scalar type", Truth: truthBreaking, Profile: profileExchange, Body: `{"exchange_active":"yes","trading_active":true}`, Args: exchange, ExpectedPath: "exchange_active"},
		{Name: "markets-page-two-ticker-missing", Mutation: "remove required item field after a valid page", Truth: truthBreaking, Profile: profileMarketsList, Bodies: []string{`{"markets":[{"ticker":"A"}],"cursor":"page-2"}`, `{"markets":[{"title":"Beta"}],"cursor":""}`}, Args: marketList, ExpectedPath: "markets[].ticker", AfterPartialPage: true},

		{Name: "markets-title-missing", Mutation: "remove task-required optional field", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,title"), "title"), ExpectedPath: "markets[].title", RequireTitle: true, KnownGap: true},
		{Name: "markets-title-number", Mutation: "change task-required optional type", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":42}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,title"), "title"), ExpectedPath: "markets[].title", RequireTitle: true, KnownGap: true},
		{Name: "markets-cursor-renamed-with-more-pages", Mutation: "rename continuation cursor", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"next_cursor":"page-2"}`, Args: marketList, ExpectedPath: "cursor", RequireNextPage: true, KnownGap: true},
		{Name: "markets-close-time-semantic-drift", Mutation: "same JSON type with invalid semantics", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","close_time":"tomorrow"}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,close_time"), "close_time"), ExpectedPath: "markets[].close_time", RequireCloseRFC3339: true, KnownGap: true},
	}
}

func appendFields(args []string, fields string) []string {
	out := append([]string(nil), args...)
	return append(out, "--fields", fields)
}

func appendRequiredFields(args []string, fields string) []string {
	out := append([]string(nil), args...)
	return append(out, "--require-fields", fields)
}

func runDirectJSON(scenario driftScenario) armResult {
	data, rawBytes, err := directAPIRead(scenario)
	accepted := err == nil
	oracleOK := false
	if accepted {
		_, oracleOK = validateStructuralTaskContract(scenario, data)
	}
	return armResult{Accepted: accepted, OracleOK: oracleOK, ValidOutput: accepted, Atomic: true, OutputBytes: rawBytes}
}

func runDirectValidated(scenario driftScenario) armResult {
	data, rawBytes, err := directAPIRead(scenario)
	if err != nil {
		return armResult{Atomic: true, OutputBytes: rawBytes}
	}
	path, ok := validateStructuralTaskContract(scenario, data)
	diagnostics := []string(nil)
	if path != "" {
		diagnostics = []string{path}
	}
	return armResult{Accepted: ok, OracleOK: ok, ValidOutput: true, DriftDetected: !ok, DiagnosticPaths: diagnostics, Atomic: true, OutputBytes: rawBytes}
}

func directAPIRead(scenario driftScenario) (map[string]any, int, error) {
	client := &api.Client{
		BaseURL: "https://example.test/trade-api/v2",
	}
	var combined map[string]any
	totalBytes := 0
	for _, body := range scenarioBodies(scenario) {
		client.HTTP = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, body), nil
		})
		page, err := client.Do(context.Background(), api.Request{Method: http.MethodGet, Path: "/fixture"})
		totalBytes += len(body)
		if err != nil {
			return nil, totalBytes, err
		}
		if combined == nil || scenario.Profile != profileMarketsList {
			combined = page
			continue
		}
		combinedMarkets, _ := combined["markets"].([]any)
		pageMarkets, _ := page["markets"].([]any)
		combined["markets"] = append(combinedMarkets, pageMarkets...)
		if cursor, exists := page["cursor"]; exists {
			combined["cursor"] = cursor
		} else {
			delete(combined, "cursor")
		}
	}
	return combined, totalBytes, nil
}

func runCLIContract(scenario driftScenario) armResult {
	var stdout, stderr bytes.Buffer
	bodies := scenarioBodies(scenario)
	call := 0
	app := New(Config{
		Stdout:  &stdout,
		Stderr:  &stderr,
		BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if call >= len(bodies) {
				return response(http.StatusInternalServerError, `{"error":"unexpected extra request"}`), nil
			}
			body := bodies[call]
			call++
			return response(http.StatusOK, body), nil
		}),
	})
	code := app.Run(context.Background(), scenario.Args)
	if code == contract.ExitOK {
		var envelope contract.Envelope
		parsed := json.Unmarshal(stdout.Bytes(), &envelope) == nil
		validOutput := parsed && validVersionedEnvelope(scenario, envelope) && envelope.OK && envelope.Error == nil
		data, _ := envelope.Data.(map[string]any)
		_, oracleOK := validateStructuralTaskContract(scenario, data)
		if validOutput && len(scenario.ExpectedProjectedMarketKeys) > 0 {
			validOutput = projectedMarketKeysMatch(data, scenario.ExpectedProjectedMarketKeys)
		}
		return armResult{
			Accepted:    true,
			OracleOK:    oracleOK,
			ValidOutput: validOutput,
			Atomic:      true,
			Versioned:   validVersionedEnvelope(scenario, envelope),
			OutputBytes: stdout.Len(),
		}
	}

	var envelope contract.Envelope
	parsed := json.Unmarshal(stderr.Bytes(), &envelope) == nil
	versioned := parsed && validVersionedEnvelope(scenario, envelope)
	driftDetected := code == contract.ExitUpstream && parsed && versioned && !envelope.OK && envelope.Error != nil && envelope.Error.Code == "UPSTREAM_SCHEMA_MISMATCH" && errorDetailsVersionMatches(envelope.Error, expectedOutputContract(scenario.Profile)) && stdout.Len() == 0
	return armResult{
		Accepted:        false,
		ValidOutput:     parsed,
		DriftDetected:   driftDetected,
		DiagnosticPaths: diagnosticPaths(envelope.Error),
		Atomic:          stdout.Len() == 0,
		Versioned:       versioned,
		OutputBytes:     stderr.Len(),
	}
}

func validateStructuralTaskContract(scenario driftScenario, data map[string]any) (string, bool) {
	switch scenario.Profile {
	case profileExchange:
		for _, field := range []string{"exchange_active", "trading_active"} {
			value, exists := data[field]
			if !exists || !isBool(value) {
				return field, false
			}
		}
		return "", true
	case profileMarketsGet:
		market, exists := data["market"]
		if !exists || !isObject(market) {
			return "market", false
		}
		obj := market.(map[string]any)
		if ticker, exists := obj["ticker"]; !exists || !isString(ticker) {
			return "market.ticker", false
		}
		return "", true
	case profileMarketsList:
		rawMarkets, exists := data["markets"]
		if !exists || !isArray(rawMarkets) {
			return "markets", false
		}
		for _, rawMarket := range rawMarkets.([]any) {
			market, ok := rawMarket.(map[string]any)
			if !ok {
				return "markets[].ticker", false
			}
			if ticker, exists := market["ticker"]; !exists || !isString(ticker) {
				return "markets[].ticker", false
			}
			if scenario.RequireTitle {
				if title, exists := market["title"]; !exists || !isString(title) {
					return "markets[].title", false
				}
			}
			if scenario.RequireCloseRFC3339 {
				closeTime, exists := market["close_time"]
				if !exists || !isString(closeTime) {
					return "markets[].close_time", false
				}
				if _, err := time.Parse(time.RFC3339, closeTime.(string)); err != nil {
					return "markets[].close_time", false
				}
			}
		}
		cursor, cursorExists := data["cursor"]
		if scenario.RequireNextPage {
			if !cursorExists || !isString(cursor) || cursor == "" {
				return "cursor", false
			}
			return "", true
		}
		if cursorExists && cursor != nil && !isString(cursor) {
			return "cursor", false
		}
		return "", true
	default:
		return "profile", false
	}
}

func scenarioBodies(scenario driftScenario) []string {
	if len(scenario.Bodies) > 0 {
		return scenario.Bodies
	}
	return []string{scenario.Body}
}

func validVersionedEnvelope(scenario driftScenario, envelope contract.Envelope) bool {
	return envelope.SchemaVersion == contract.SchemaVersion &&
		envelope.OutputContractVersion == expectedOutputContract(scenario.Profile) &&
		envelope.Command == string(scenario.Profile)
}

func expectedOutputContract(profile driftProfile) string {
	switch profile {
	case profileExchange:
		return "kalshi.output/exchange.status/v1"
	case profileMarketsList:
		return "kalshi.output/markets.list/v1"
	case profileMarketsGet:
		return "kalshi.output/markets.get/v1"
	default:
		return ""
	}
}

func projectedMarketKeysMatch(data map[string]any, expected []string) bool {
	markets, ok := data["markets"].([]any)
	if !ok {
		return false
	}
	for _, rawMarket := range markets {
		market, ok := rawMarket.(map[string]any)
		if !ok || len(market) != len(expected) {
			return false
		}
		for _, key := range expected {
			if _, exists := market[key]; !exists {
				return false
			}
		}
	}
	return true
}

func diagnosticPaths(apiError *contract.APIError) []string {
	if apiError == nil {
		return nil
	}
	details, ok := apiError.Details.(map[string]any)
	if !ok {
		return nil
	}
	paths := []string{}
	if missing, ok := details["missing_fields"].([]any); ok {
		for _, rawField := range missing {
			if field, ok := rawField.(string); ok {
				paths = append(paths, field)
			}
		}
	}
	if mismatches, ok := details["type_mismatches"].([]any); ok {
		for _, rawMismatch := range mismatches {
			if mismatch, ok := rawMismatch.(map[string]any); ok {
				if field, ok := mismatch["field"].(string); ok {
					paths = append(paths, field)
				}
			}
		}
	}
	if mismatches, ok := details["format_mismatches"].([]any); ok {
		for _, rawMismatch := range mismatches {
			if mismatch, ok := rawMismatch.(map[string]any); ok {
				if field, ok := mismatch["field"].(string); ok {
					paths = append(paths, field)
				}
			}
		}
	}
	return paths
}

func errorDetailsVersionMatches(apiError *contract.APIError, expected string) bool {
	if apiError == nil {
		return false
	}
	details, ok := apiError.Details.(map[string]any)
	if !ok {
		return false
	}
	version, ok := details["output_contract_version"].(string)
	return ok && version == expected
}

func isObject(value any) bool { _, ok := value.(map[string]any); return ok }
func isArray(value any) bool  { _, ok := value.([]any); return ok }
func isString(value any) bool { _, ok := value.(string); return ok }
func isBool(value any) bool   { _, ok := value.(bool); return ok }

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func containsDiagnostic(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func classifyOutcome(scenario driftScenario, result armResult) string {
	if scenario.Truth == truthCompatible {
		if result.Accepted && result.OracleOK && result.ValidOutput {
			return "correct_success"
		}
		if !result.Accepted {
			return "false_positive"
		}
		return "invalid_compatible_success"
	}
	if result.DriftDetected {
		return "detected_failure"
	}
	if result.Accepted && (!result.OracleOK || !result.ValidOutput) {
		return "silent_wrong_success"
	}
	if result.Accepted {
		return "unexpected_correct_success"
	}
	return "other_breaking_failure"
}
