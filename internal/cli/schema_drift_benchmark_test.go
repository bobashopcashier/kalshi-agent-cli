package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bobashopcashier/kalshi-cli/internal/api"
	"github.com/bobashopcashier/kalshi-cli/internal/contract"
	"github.com/bobashopcashier/kalshi-cli/internal/registry"
)

type driftTruth string

const (
	truthCompatible driftTruth = "compatible"
	truthBreaking   driftTruth = "breaking"
)

type driftProfile string

const (
	profileEventsGet    driftProfile = "events.get"
	profileEventsList   driftProfile = "events.list"
	profileExchange     driftProfile = "exchange.status"
	profileMarketsList  driftProfile = "markets.list"
	profileMarketsGet   driftProfile = "markets.get"
	profileOrderbookGet driftProfile = "orderbook.get"
	profileSeriesGet    driftProfile = "series.get"
	profileSeriesList   driftProfile = "series.list"
	profileTradesList   driftProfile = "trades.list"
)

type taskField struct {
	Path   string
	Type   string
	Format string
}

type driftTaskProfile struct {
	RequiredFields []taskField
}

var driftTaskProfiles = map[driftProfile]driftTaskProfile{
	profileEventsGet: {
		RequiredFields: []taskField{{Path: "event", Type: "object"}, {Path: "event.event_ticker", Type: "string"}},
	},
	profileEventsList: {
		RequiredFields: []taskField{{Path: "event_ticker", Type: "string"}},
	},
	profileExchange: {
		RequiredFields: []taskField{{Path: "exchange_active", Type: "boolean"}, {Path: "trading_active", Type: "boolean"}},
	},
	profileMarketsGet: {
		RequiredFields: []taskField{{Path: "market", Type: "object"}, {Path: "market.ticker", Type: "string"}},
	},
	profileMarketsList: {
		RequiredFields: []taskField{{Path: "ticker", Type: "string"}},
	},
	profileOrderbookGet: {
		RequiredFields: []taskField{{Path: "orderbook_fp", Type: "object"}},
	},
	profileSeriesGet: {
		RequiredFields: []taskField{{Path: "series", Type: "object"}, {Path: "series.ticker", Type: "string"}},
	},
	profileSeriesList: {
		RequiredFields: []taskField{{Path: "ticker", Type: "string"}},
	},
	profileTradesList: {
		RequiredFields: []taskField{{Path: "trade_id", Type: "string"}},
	},
}

type driftScenario struct {
	Name                      string
	Mutation                  string
	Truth                     driftTruth
	Profile                   driftProfile
	Body                      string
	Bodies                    []string
	Args                      []string
	ExpectedPath              string
	ExpectedProjectedItemKeys []string
	TaskFields                []taskField
	RequireNextPage           bool
	KnownGap                  bool
	AfterPartialPage          bool
}

type armResult struct {
	Accepted        bool
	TaskCorrect     bool
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
				if result.Accepted && result.TaskCorrect && result.ValidOutput {
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
			} else if result.Accepted && (!result.TaskCorrect || !result.ValidOutput) {
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
	if len(summaries) != 2 {
		t.Fatalf("benchmark arms=%d, want 2", len(summaries))
	}
	for _, summary := range summaries {
		if summary.CompatibleCases != 16 || summary.BreakingCases != 32 || summary.ContractBreakingCases != 28 || summary.KnownGapCases != 4 {
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

	raw, cliSummary := summaries[0], summaries[1]
	if raw.CorrectCompatible != 16 || raw.DetectedBreaking != 0 || raw.SilentWrongBreaking != 32 || raw.FalsePositives != 0 || raw.InvalidCompatible != 0 {
		t.Fatalf("unexpected unvalidated decoder result: %+v", raw)
	}
	if cliSummary.CorrectCompatible != 16 || cliSummary.FalsePositives != 0 || cliSummary.InvalidCompatible != 0 {
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
	if cliSummary.AtomicPartialFailures != cliSummary.PartialPageBreaks || cliSummary.PartialPageBreaks != 2 {
		t.Fatalf("CLI atomic partial-page failures=%d/%d", cliSummary.AtomicPartialFailures, cliSummary.PartialPageBreaks)
	}
	if cliSummary.VersionedOutputs != scenarioCount {
		t.Fatalf("CLI exact versioned envelopes=%d/%d", cliSummary.VersionedOutputs, scenarioCount)
	}
}

func schemaDriftScenarios() []driftScenario {
	eventsList := []string{"events", "list", "--max-pages", "2", "--max-items", "10", "--compact"}
	eventsGet := []string{"events", "get", "--event-ticker", "EVT", "--compact"}
	marketList := []string{"markets", "list", "--max-pages", "2", "--max-items", "10", "--compact"}
	marketGet := []string{"markets", "get", "--ticker", "A", "--compact"}
	orderbookGet := []string{"orderbook", "get", "--ticker", "A", "--compact"}
	seriesList := []string{"series", "list", "--compact"}
	seriesGet := []string{"series", "get", "--series-ticker", "SER", "--compact"}
	tradesList := []string{"trades", "list", "--max-pages", "2", "--max-items", "10", "--compact"}
	exchange := []string{"exchange", "status", "--compact"}
	return []driftScenario{
		{Name: "markets-valid", Mutation: "none", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":"Alpha"}],"cursor":""}`, Args: marketList},
		{Name: "markets-additive-field", Mutation: "add optional field", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":"Alpha","new_field":true}],"cursor":"","new_top_level":"ok"}`, Args: marketList},
		{Name: "markets-additive-field-projected", Mutation: "add field outside requested projection", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":"Alpha","new_field":true}],"cursor":"","new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(marketList, "ticker,title"), "title"), ExpectedProjectedItemKeys: []string{"ticker", "title"}, TaskFields: []taskField{{Path: "title", Type: "string"}}},
		{Name: "markets-key-reorder", Mutation: "reorder keys", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"cursor":"","markets":[{"title":"Alpha","ticker":"A"}]}`, Args: marketList},
		{Name: "markets-empty", Mutation: "empty collection", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[],"cursor":""}`, Args: marketList},
		{Name: "markets-cursor-null", Mutation: "terminal cursor null", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"cursor":null}`, Args: marketList},
		{Name: "markets-cursor-absent-terminal", Mutation: "terminal cursor absent", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}]}`, Args: marketList},
		{Name: "markets-close-time-valid", Mutation: "valid same-type semantic value", Truth: truthCompatible, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","close_time":"2026-08-01T12:00:00Z"}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,close_time"), "close_time"), TaskFields: []taskField{{Path: "close_time", Type: "string", Format: "date-time"}}},
		{Name: "market-get-valid", Mutation: "none", Truth: truthCompatible, Profile: profileMarketsGet, Body: `{"market":{"ticker":"A","title":"Alpha"}}`, Args: marketGet},
		{Name: "exchange-valid", Mutation: "none", Truth: truthCompatible, Profile: profileExchange, Body: `{"exchange_active":true,"trading_active":true}`, Args: exchange},
		{Name: "events-list-valid", Mutation: "project event identity and title with additive fields", Truth: truthCompatible, Profile: profileEventsList, Body: `{"cursor":"","events":[{"title":"Event","event_ticker":"EVT","new_field":true}],"new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(eventsList, "event_ticker,title"), "title"), ExpectedProjectedItemKeys: []string{"event_ticker", "title"}, TaskFields: []taskField{{Path: "title", Type: "string"}}},
		{Name: "events-get-valid", Mutation: "project one event with additive fields", Truth: truthCompatible, Profile: profileEventsGet, Body: `{"event":{"event_ticker":"EVT","title":"Event","new_field":true},"markets":[],"new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(eventsGet, "event.event_ticker,event.title"), "event.title"), TaskFields: []taskField{{Path: "event.title", Type: "string"}}},
		{Name: "series-list-valid", Mutation: "project series identity and title with additive fields", Truth: truthCompatible, Profile: profileSeriesList, Body: `{"series":[{"title":"Series","ticker":"SER","new_field":true}],"new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(seriesList, "ticker,title"), "title"), ExpectedProjectedItemKeys: []string{"ticker", "title"}, TaskFields: []taskField{{Path: "title", Type: "string"}}},
		{Name: "series-get-valid", Mutation: "project one series with additive fields", Truth: truthCompatible, Profile: profileSeriesGet, Body: `{"series":{"ticker":"SER","title":"Series","new_field":true},"new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(seriesGet, "series.ticker,series.title"), "series.title"), TaskFields: []taskField{{Path: "series.title", Type: "string"}}},
		{Name: "trades-list-valid", Mutation: "project trade identity ticker and timestamp", Truth: truthCompatible, Profile: profileTradesList, Body: `{"trades":[{"trade_id":"T1","ticker":"A","created_time":"2026-08-01T12:00:00Z","new_field":true}],"cursor":"","new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(tradesList, "trade_id,ticker,created_time"), "ticker,created_time"), ExpectedProjectedItemKeys: []string{"created_time", "ticker", "trade_id"}, TaskFields: []taskField{{Path: "ticker", Type: "string"}, {Path: "created_time", Type: "string", Format: "date-time"}}},
		{Name: "orderbook-valid", Mutation: "project required YES and NO levels", Truth: truthCompatible, Profile: profileOrderbookGet, Body: `{"orderbook_fp":{"yes_dollars":[["0.4000","10.00"]],"no_dollars":[],"new_field":true},"new_top_level":"ok"}`, Args: appendRequiredFields(appendFields(orderbookGet, "orderbook_fp.yes_dollars,orderbook_fp.no_dollars"), "orderbook_fp.yes_dollars,orderbook_fp.no_dollars"), TaskFields: []taskField{{Path: "orderbook_fp.yes_dollars", Type: "array"}, {Path: "orderbook_fp.no_dollars", Type: "array"}}},

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
		{Name: "events-list-identity-number", Mutation: "change event identity type", Truth: truthBreaking, Profile: profileEventsList, Body: `{"events":[{"event_ticker":42,"title":"Event"}],"cursor":""}`, Args: eventsList, ExpectedPath: "events[].event_ticker"},
		{Name: "events-list-title-missing", Mutation: "remove task-required event title", Truth: truthBreaking, Profile: profileEventsList, Body: `{"events":[{"event_ticker":"EVT"}],"cursor":""}`, Args: appendRequiredFields(appendFields(eventsList, "event_ticker,title"), "title"), ExpectedPath: "events[].title", TaskFields: []taskField{{Path: "title", Type: "string"}}},
		{Name: "events-get-wrapper-missing", Mutation: "remove event wrapper", Truth: truthBreaking, Profile: profileEventsGet, Body: `{"markets":[]}`, Args: eventsGet, ExpectedPath: "event"},
		{Name: "events-get-title-number", Mutation: "change projected event title type", Truth: truthBreaking, Profile: profileEventsGet, Body: `{"event":{"event_ticker":"EVT","title":42},"markets":[]}`, Args: appendRequiredFields(appendFields(eventsGet, "event.event_ticker,event.title"), "event.title"), ExpectedPath: "event.title", TaskFields: []taskField{{Path: "event.title", Type: "string"}}},
		{Name: "series-list-wrapper-missing", Mutation: "remove series collection", Truth: truthBreaking, Profile: profileSeriesList, Body: `{}`, Args: seriesList, ExpectedPath: "series"},
		{Name: "series-list-title-missing", Mutation: "remove task-required title from one series", Truth: truthBreaking, Profile: profileSeriesList, Body: `{"series":[{"ticker":"SER-A","title":"Series A"},{"ticker":"SER-B"}]}`, Args: appendRequiredFields(appendFields(seriesList, "ticker,title"), "title"), ExpectedPath: "series[].title", TaskFields: []taskField{{Path: "title", Type: "string"}}},
		{Name: "series-get-wrapper-array", Mutation: "change series wrapper type", Truth: truthBreaking, Profile: profileSeriesGet, Body: `{"series":[]}`, Args: seriesGet, ExpectedPath: "series"},
		{Name: "series-get-identity-missing", Mutation: "remove series identity", Truth: truthBreaking, Profile: profileSeriesGet, Body: `{"series":{"title":"Series"}}`, Args: seriesGet, ExpectedPath: "series.ticker"},
		{Name: "trades-list-created-time-format", Mutation: "break projected trade timestamp semantics", Truth: truthBreaking, Profile: profileTradesList, Body: `{"trades":[{"trade_id":"T1","ticker":"A","created_time":"tomorrow"}],"cursor":""}`, Args: appendRequiredFields(appendFields(tradesList, "trade_id,ticker,created_time"), "ticker,created_time"), ExpectedPath: "trades[].created_time", TaskFields: []taskField{{Path: "ticker", Type: "string"}, {Path: "created_time", Type: "string", Format: "date-time"}}},
		{Name: "trades-page-two-identity-missing", Mutation: "remove trade identity after a valid page", Truth: truthBreaking, Profile: profileTradesList, Bodies: []string{`{"trades":[{"trade_id":"T1","ticker":"A","created_time":"2026-08-01T12:00:00Z"}],"cursor":"page-2"}`, `{"trades":[{"ticker":"B","created_time":"2026-08-01T12:01:00Z"}],"cursor":""}`}, Args: appendRequiredFields(appendFields(tradesList, "trade_id,ticker,created_time"), "ticker,created_time"), ExpectedPath: "trades[].trade_id", TaskFields: []taskField{{Path: "ticker", Type: "string"}, {Path: "created_time", Type: "string", Format: "date-time"}}, AfterPartialPage: true},
		{Name: "orderbook-wrapper-null", Mutation: "set orderbook wrapper null", Truth: truthBreaking, Profile: profileOrderbookGet, Body: `{"orderbook_fp":null}`, Args: orderbookGet, ExpectedPath: "orderbook_fp"},
		{Name: "orderbook-yes-wrong-type", Mutation: "change projected YES levels type", Truth: truthBreaking, Profile: profileOrderbookGet, Body: `{"orderbook_fp":{"yes_dollars":{},"no_dollars":[]}}`, Args: appendRequiredFields(appendFields(orderbookGet, "orderbook_fp.yes_dollars,orderbook_fp.no_dollars"), "orderbook_fp.yes_dollars,orderbook_fp.no_dollars"), ExpectedPath: "orderbook_fp.yes_dollars", TaskFields: []taskField{{Path: "orderbook_fp.yes_dollars", Type: "array"}, {Path: "orderbook_fp.no_dollars", Type: "array"}}},

		{Name: "markets-title-missing", Mutation: "remove task-required optional field", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,title"), "title"), ExpectedPath: "markets[].title", TaskFields: []taskField{{Path: "title", Type: "string"}}, KnownGap: true},
		{Name: "markets-title-number", Mutation: "change task-required optional type", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","title":42}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,title"), "title"), ExpectedPath: "markets[].title", TaskFields: []taskField{{Path: "title", Type: "string"}}, KnownGap: true},
		{Name: "markets-cursor-renamed-with-more-pages", Mutation: "rename continuation cursor", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A"}],"next_cursor":"page-2"}`, Args: marketList, ExpectedPath: "cursor", RequireNextPage: true, KnownGap: true},
		{Name: "markets-close-time-semantic-drift", Mutation: "same JSON type with invalid semantics", Truth: truthBreaking, Profile: profileMarketsList, Body: `{"markets":[{"ticker":"A","close_time":"tomorrow"}],"cursor":""}`, Args: appendRequiredFields(appendFields(marketList, "ticker,close_time"), "close_time"), ExpectedPath: "markets[].close_time", TaskFields: []taskField{{Path: "close_time", Type: "string", Format: "date-time"}}, KnownGap: true},
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
	taskCorrect := false
	if accepted {
		_, taskCorrect = scoreTaskResult(scenario, data)
	}
	return armResult{Accepted: accepted, TaskCorrect: taskCorrect, ValidOutput: accepted, Atomic: true, OutputBytes: rawBytes}
}

func directAPIRead(scenario driftScenario) (map[string]any, int, error) {
	command, ok := registry.ByName(string(scenario.Profile))
	if !ok {
		return nil, 0, fmt.Errorf("benchmark profile %s is not registered", scenario.Profile)
	}
	collection := command.ResponseSchema.CollectionField
	cursorField := command.ResponseSchema.CursorField
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
		if combined == nil || collection == "" {
			combined = page
			continue
		}
		pageItems, pageExists := page[collection]
		combinedItems, combinedOK := combined[collection].([]any)
		newItems, pageOK := pageItems.([]any)
		if !pageExists {
			delete(combined, collection)
		} else if combinedOK && pageOK {
			combined[collection] = append(combinedItems, newItems...)
		} else {
			combined[collection] = pageItems
		}
		if cursorField != "" {
			if cursor, exists := page[cursorField]; exists {
				combined[cursorField] = cursor
			} else {
				delete(combined, cursorField)
			}
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
		_, taskCorrect := scoreTaskResult(scenario, data)
		if validOutput && len(scenario.ExpectedProjectedItemKeys) > 0 {
			command, _ := registry.ByName(string(scenario.Profile))
			validOutput = projectedItemKeysMatch(data, command.ResponseSchema.CollectionField, scenario.ExpectedProjectedItemKeys)
		}
		return armResult{
			Accepted:    true,
			TaskCorrect: taskCorrect,
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

func scoreTaskResult(scenario driftScenario, data map[string]any) (string, bool) {
	profile, ok := driftTaskProfiles[scenario.Profile]
	if !ok {
		return "profile", false
	}
	command, ok := registry.ByName(string(scenario.Profile))
	if !ok {
		return "profile", false
	}
	fields := append(append([]taskField(nil), profile.RequiredFields...), scenario.TaskFields...)
	collection := command.ResponseSchema.CollectionField
	if collection == "" {
		if path, ok := scoreTaskFields(data, fields, ""); !ok {
			return path, false
		}
	} else {
		rawItems, exists := data[collection]
		items, isArray := rawItems.([]any)
		if !exists || !isArray {
			return collection, false
		}
		for _, item := range items {
			if path, ok := scoreTaskFields(item, fields, collection+"[]."); !ok {
				return path, false
			}
		}
	}
	cursorField := command.ResponseSchema.CursorField
	if cursorField == "" {
		return "", true
	}
	cursor, cursorExists := data[cursorField]
	if scenario.RequireNextPage {
		if !cursorExists || !isString(cursor) || cursor == "" {
			return cursorField, false
		}
		return "", true
	}
	if cursorExists && cursor != nil && !isString(cursor) {
		return cursorField, false
	}
	return "", true
}

func scoreTaskFields(root any, fields []taskField, prefix string) (string, bool) {
	for _, field := range fields {
		rendered := prefix + field.Path
		values, exists := responsePathValues(root, strings.Split(field.Path, "."))
		if !exists {
			return rendered, false
		}
		for _, value := range values {
			if !matchesResponseType(value, field.Type) {
				return rendered, false
			}
			if field.Format != "" && !matchesResponseFormat(value, field.Format) {
				return rendered, false
			}
		}
	}
	return "", true
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
	command, ok := registry.ByName(string(profile))
	if !ok {
		return ""
	}
	return command.OutputContractVersion
}

func projectedItemKeysMatch(data map[string]any, collection string, expected []string) bool {
	items, ok := data[collection].([]any)
	if !ok {
		return false
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || len(item) != len(expected) {
			return false
		}
		for _, key := range expected {
			if _, exists := item[key]; !exists {
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

func isString(value any) bool { _, ok := value.(string); return ok }

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
		if result.Accepted && result.TaskCorrect && result.ValidOutput {
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
	if result.Accepted && (!result.TaskCorrect || !result.ValidOutput) {
		return "silent_wrong_success"
	}
	if result.Accepted {
		return "unexpected_correct_success"
	}
	return "other_breaking_failure"
}
