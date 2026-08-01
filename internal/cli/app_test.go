package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"kalshi-cli/internal/api"
	"kalshi-cli/internal/auth"
	"kalshi-cli/internal/contract"
	"kalshi-cli/internal/registry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestDryRunEveryCommandHasZeroCredentialsAndZeroNetwork(t *testing.T) {
	var network, credentials atomic.Int64
	for _, cmd := range registry.All() {
		t.Run(cmd.Name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := New(Config{
				Stdout: &stdout, Stderr: &stderr,
				HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) { network.Add(1); return nil, errors.New("must not run") }),
				Credentials: func() (auth.Credentials, error) {
					credentials.Add(1)
					return auth.Credentials{}, errors.New("must not load")
				},
			})
			args := append([]string{}, cmd.CLIPath...)
			if raw := validParams(cmd.Name); raw != "" {
				args = append(args, "--params", raw)
			}
			args = append(args, "--dry-run", "--compact")
			if code := app.Run(context.Background(), args); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), `"network":false`) || !strings.Contains(stdout.String(), `"dry_run":true`) {
				t.Fatalf("bad dry-run envelope: %s", stdout.String())
			}
		})
	}
	if network.Load() != 0 || credentials.Load() != 0 {
		t.Fatalf("network=%d credentials=%d", network.Load(), credentials.Load())
	}
}

func validParams(name string) string {
	switch name {
	case "markets.get", "orderbook.get":
		return `{"ticker":"TEST-1"}`
	case "events.get":
		return `{"event_ticker":"EVENT-1"}`
	case "series.get":
		return `{"series_ticker":"SERIES-1"}`
	case "orders.get":
		return `{"order_id":"order-1"}`
	case "orders.reconcile":
		return `{"client_order_id":"client-1"}`
	case "orders.create":
		return `{"ticker":"TEST-1","client_order_id":"client-1","side":"bid","count":"1.00","price":"0.25"}`
	case "orders.cancel":
		return `{"order_id":"order-1"}`
	default:
		return ""
	}
}

func TestSeriesListUsesExactTagFilterAndCollectionProjection(t *testing.T) {
	var calls atomic.Int64
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.Method != "GET" || req.URL.Scheme != "https" || req.URL.Host != "external-api.kalshi.com" || req.URL.Path != "/trade-api/v2/series" {
			t.Fatalf("request=%s %s", req.Method, req.URL)
		}
		if req.Header.Get("KALSHI-ACCESS-KEY") != "" || req.Header.Get("KALSHI-ACCESS-SIGNATURE") != "" {
			t.Fatalf("public series request was unexpectedly authenticated")
		}
		if req.URL.Query().Get("tags") != "Fed" || req.URL.Query().Get("include_volume") != "true" {
			t.Fatalf("query=%s", req.URL.RawQuery)
		}
		if req.URL.Query().Has("cursor") || req.URL.Query().Has("limit") {
			t.Fatalf("series list is not cursor-paginated: %s", req.URL.RawQuery)
		}
		return response(200, `{"series":[{"ticker":"KXFED","tags":["Fed"],"volume_fp":"10.00","title":"Fed decisions"}]}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, HTTP: doer})
	args := []string{"series", "list", "--environment", "production", "--params", `{"tags":"Fed","include_volume":true}`, "--fields", "ticker,tags,volume_fp", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || !strings.Contains(stdout.String(), `"ticker":"KXFED"`) || !strings.Contains(stdout.String(), `"tags":["Fed"]`) || !strings.Contains(stdout.String(), `"volume_fp":"10.00"`) {
		t.Fatalf("output=%s calls=%d", stdout.String(), calls.Load())
	}
	if strings.Contains(stdout.String(), `"title"`) || strings.Contains(stdout.String(), `"pagination"`) {
		t.Fatalf("unexpected series output=%s", stdout.String())
	}
}

func TestSeriesGetBuildsPathAndProjectsWrapper(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/trade-api/v2/series/KXFED" || req.URL.Query().Get("include_volume") != "true" {
			t.Fatalf("url=%s", req.URL)
		}
		return response(200, `{"series":{"ticker":"KXFED","tags":["Fed"],"volume_fp":"10.00","title":"omit"}}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	args := []string{"series", "get", "--params", `{"series_ticker":"KXFED","include_volume":true}`, "--fields", "series.ticker,series.tags,series.volume_fp", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"series":{"tags":["Fed"],"ticker":"KXFED","volume_fp":"10.00"}`) || strings.Contains(stdout.String(), `"title"`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestWriteRequiresPolicyAndDigestBeforeCredentialsOrNetwork(t *testing.T) {
	var calls atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return nil, errors.New("unexpected") }), Credentials: func() (auth.Credentials, error) { calls.Add(1); return auth.Credentials{}, errors.New("unexpected") }})
	args := []string{"orders", "cancel", "--order-id", "order-1", "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitPolicy {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 0 {
		t.Fatalf("side-effect boundary crossed %d times", calls.Load())
	}
	if !strings.Contains(stderr.String(), `"code":"POLICY_DENIED"`) {
		t.Fatalf("error=%s", stderr.String())
	}

	stderr.Reset()
	args = append(args[:len(args)-1], "--write-policy", "demo-only", "--confirm", "sha256:wrong", "--compact")
	if code := app.Run(context.Background(), args); code != contract.ExitPolicy {
		t.Fatalf("exit=%d", code)
	}
	if calls.Load() != 0 {
		t.Fatalf("side-effect boundary crossed %d times", calls.Load())
	}
	if !strings.Contains(stderr.String(), `"code":"CONFIRMATION_MISMATCH"`) {
		t.Fatalf("error=%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "expected_plan_digest") {
		t.Fatalf("confirmation digest leaked without dry-run: %s", stderr.String())
	}
}

func TestCreateDryRunDigestThenConfirmedSignedMutation(t *testing.T) {
	params := validParams("orders.create")
	dryArgs := []string{"orders", "create", "--params", params, "--write-policy", "demo-only", "--dry-run", "--compact"}
	var dryOut, dryErr bytes.Buffer
	dryApp := New(Config{Stdout: &dryOut, Stderr: &dryErr, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("dry-run performed network"); return nil, nil }), Credentials: func() (auth.Credentials, error) {
		t.Fatal("dry-run loaded credentials")
		return auth.Credentials{}, nil
	}})
	if code := dryApp.Run(context.Background(), dryArgs); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, dryErr.String())
	}
	digest := extractDigest(t, dryOut.Bytes())

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	var gotBody map[string]any
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.Method != "POST" || req.URL.Path != "/trade-api/v2/portfolio/events/orders" {
			t.Fatalf("request=%s %s", req.Method, req.URL)
		}
		if req.Header.Get("KALSHI-ACCESS-KEY") != "kid" || req.Header.Get("KALSHI-ACCESS-SIGNATURE") == "" {
			t.Fatalf("missing signed headers: %#v", req.Header)
		}
		if err := json.NewDecoder(req.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		return response(201, `{"order_id":"order-1","client_order_id":"client-1","fill_count":"0.00","remaining_count":"1.00","ts_ms":1}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer, Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil }})
	args := []string{"orders", "create", "--params", params, "--write-policy", "demo-only", "--confirm", digest, "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if gotBody["client_order_id"] != "client-1" || gotBody["post_only"] != true || gotBody["cancel_order_on_pause"] != true {
		t.Fatalf("body=%#v", gotBody)
	}
	if !strings.Contains(stdout.String(), `"mutation_status":"confirmed"`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestAmbiguousWriteIsUnknownAndNeverRetryable(t *testing.T) {
	params := validParams("orders.create")
	digest := dryRunDigest(t, params)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil }, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, &net.DNSError{Err: "uncertain", Name: "example.test"}
	})})
	args := []string{"orders", "create", "--params", params, "--write-policy", "demo-only", "--confirm", digest, "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitNetwork {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, generic retries are forbidden", calls.Load())
	}
	if !strings.Contains(stderr.String(), `"mutation_status":"unknown"`) || !strings.Contains(stderr.String(), `"retryable":false`) || !strings.Contains(stderr.String(), `orders reconcile`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestPaginationCapsAndTruncationMetadata(t *testing.T) {
	var calls atomic.Int64
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		switch req.URL.Query().Get("cursor") {
		case "":
			return response(200, `{"markets":[{"ticker":"A"},{"ticker":"B"}],"cursor":"c1"}`), nil
		case "c1":
			if req.URL.Query().Get("limit") != "1" {
				t.Fatalf("second-page limit=%s", req.URL.Query().Get("limit"))
			}
			return response(200, `{"markets":[{"ticker":"C"}],"cursor":"c2"}`), nil
		default:
			t.Fatalf("unexpected cursor %q", req.URL.Query().Get("cursor"))
			return nil, nil
		}
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"markets", "list", "--max-pages", "2", "--max-items", "3", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	var env contract.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Meta.Pagination == nil || env.Meta.Pagination.PagesFetched != 2 || env.Meta.Pagination.ItemsScanned != 3 || env.Meta.Pagination.NextCursor != "c2" {
		t.Fatalf("pagination=%#v", env.Meta.Pagination)
	}
	if !env.Meta.Truncation.Truncated || len(env.Meta.Truncation.Reasons) != 2 {
		t.Fatalf("truncation=%#v", env.Meta.Truncation)
	}
}

func TestNDJSONHasItemsAndFinalSummary(t *testing.T) {
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"trades":[{"trade_id":"1"},{"trade_id":"2"}],"cursor":""}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"trades", "list", "--ndjson"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines=%d output=%s", len(lines), stdout.String())
	}
	if !strings.Contains(lines[0], `"record_type":"item"`) || !strings.Contains(lines[2], `"record_type":"summary"`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestReconcileScansBoundedPagesAndExactMatchesLocally(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.Query().Has("client_order_id") {
			t.Fatal("local-only client_order_id leaked into unsupported upstream query")
		}
		if req.Header.Get("KALSHI-ACCESS-SIGNATURE") == "" {
			t.Fatal("reconcile request was not signed")
		}
		if req.URL.Query().Get("cursor") == "" {
			return response(200, `{"orders":[{"order_id":"other","client_order_id":"other-id"}],"cursor":"c1"}`), nil
		}
		return response(200, `{"orders":[{"order_id":"found","client_order_id":"client-1"},{"order_id":"near","client_order_id":"client-10"}],"cursor":""}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer, Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil }})
	args := []string{"orders", "reconcile", "--client-order-id", "client-1", "--max-pages", "2", "--max-items", "10", "--fields", "order_id", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if !strings.Contains(stdout.String(), `"order_id":"found"`) || strings.Contains(stdout.String(), `"client_order_id"`) || strings.Contains(stdout.String(), `"order_id":"near"`) || strings.Contains(stdout.String(), `"order_id":"other"`) {
		t.Fatalf("output=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"items_scanned":3`) || !strings.Contains(stdout.String(), `"items_returned":1`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestResponseSanitization(t *testing.T) {
	unsafe := "bad\x1b[31m" + string(rune(0x202e))
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		items := []map[string]string{{"ticker": unsafe}}
		raw, _ := json.Marshal(map[string]any{"markets": items, "cursor": "next"})
		return response(200, string(raw)), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"markets", "list", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if strings.ContainsRune(stdout.String(), '\x1b') || strings.Contains(stdout.String(), string(rune(0x202e))) {
		t.Fatalf("unsafe output=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `\\u001B`) {
		t.Fatalf("missing sanitization metadata: %s", stdout.String())
	}
}

func TestProjectionRunsBeforeOutputByteCap(t *testing.T) {
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Query().Has("fields") {
			t.Fatal("local --fields leaked upstream")
		}
		items := []map[string]string{
			{"ticker": "A", "rules": strings.Repeat("x", 700)},
			{"ticker": "B", "rules": strings.Repeat("x", 700)},
		}
		raw, _ := json.Marshal(map[string]any{"markets": items, "cursor": "next"})
		return response(200, string(raw)), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	args := []string{"markets", "list", "--fields", "ticker", "--max-items", "2", "--max-bytes", "1024", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "rules") || !strings.Contains(stdout.String(), `"ticker":"A"`) || !strings.Contains(stdout.String(), `"ticker":"B"`) {
		t.Fatalf("projection=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"next_cursor":"next"`) || !strings.Contains(stdout.String(), `"cursor":"next"`) {
		t.Fatalf("cursor missing: %s", stdout.String())
	}
}

func TestOutputByteCapFailsAtomically(t *testing.T) {
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		items := []map[string]string{{"ticker": "A", "rules": strings.Repeat("x", 2000)}}
		raw, _ := json.Marshal(map[string]any{"markets": items, "cursor": "after-a"})
		return response(200, string(raw)), nil
	})
	for _, format := range [][]string{{"--compact"}, {"--ndjson"}} {
		var stdout, stderr bytes.Buffer
		app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
		args := append([]string{"markets", "list", "--max-items", "1", "--max-bytes", "1024"}, format...)
		if code := app.Run(context.Background(), args); code != contract.ExitOutput {
			t.Fatalf("format=%v exit=%d stderr=%s", format, code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("partial success leaked for %v: %s", format, stdout.String())
		}
		if !strings.Contains(stderr.String(), `"code":"OUTPUT_LIMIT"`) || strings.Contains(stderr.String(), "after-a") {
			t.Fatalf("unsafe error for %v: %s", format, stderr.String())
		}
	}
}

func TestRenderFailuresPreserveActualEffectState(t *testing.T) {
	var network, credentials atomic.Int64
	for name, args := range map[string][]string{
		"help":    {"orders", "create", "--help", "--max-bytes", "1024", "--compact"},
		"dry-run": {"orders", "create", "--params", validParams("orders.create"), "--write-policy", "demo-only", "--dry-run", "--max-bytes", "1024", "--pretty"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := New(Config{
				Stdout: &stdout, Stderr: &stderr,
				HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
					network.Add(1)
					return nil, errors.New("unexpected network")
				}),
				Credentials: func() (auth.Credentials, error) {
					credentials.Add(1)
					return auth.Credentials{}, errors.New("unexpected credentials")
				},
			})
			if code := app.Run(context.Background(), args); code != contract.ExitOutput {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			var env contract.Envelope
			if err := json.Unmarshal(stderr.Bytes(), &env); err != nil {
				t.Fatalf("decode error envelope: %v; stderr=%s", err, stderr.String())
			}
			if stdout.Len() != 0 || env.Error == nil || env.Error.Code != "OUTPUT_LIMIT" || env.Effect.Network || env.Effect.Mutation {
				t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
			if name == "dry-run" && (!env.Effect.DryRun || env.Effect.MutationStatus != "not_attempted") {
				t.Fatalf("dry-run state lost: %s", stderr.String())
			}
		})
	}
	if network.Load() != 0 || credentials.Load() != 0 {
		t.Fatalf("network=%d credentials=%d", network.Load(), credentials.Load())
	}
}

func TestUpstreamCannotOverrunRequestedPageLimit(t *testing.T) {
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"markets":[{"ticker":"A"},{"ticker":"B"}],"cursor":"after-b"}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"markets", "list", "--max-items", "1", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"UPSTREAM_SCHEMA_MISMATCH"`) || strings.Contains(stderr.String(), "after-b") {
		t.Fatalf("output=%s error=%s", stdout.String(), stderr.String())
	}
}

func TestUpstreamCursorMustBeBoundedSafeString(t *testing.T) {
	unsafeCursor, _ := json.Marshal(map[string]any{"markets": []any{map[string]any{"ticker": "A"}}, "cursor": string(rune(0x202e)) + "after"})
	oversizedCursor, _ := json.Marshal(map[string]any{"markets": []any{map[string]any{"ticker": "A"}}, "cursor": strings.Repeat("x", maxCursorBytes+1)})
	for name, body := range map[string]string{
		"wrong type":  `{"markets":[{"ticker":"A"}],"cursor":123}`,
		"unsafe text": string(unsafeCursor),
		"oversized":   string(oversizedCursor),
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			var stdout, stderr bytes.Buffer
			app := New(Config{
				Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
				HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return response(200, body), nil
				}),
			})
			if code := app.Run(context.Background(), []string{"markets", "list", "--max-pages", "2", "--compact"}); code != contract.ExitUpstream {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if calls.Load() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"UPSTREAM_SCHEMA_MISMATCH"`) || !strings.Contains(stderr.String(), `"network":true`) {
				t.Fatalf("calls=%d output=%s error=%s", calls.Load(), stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), string(rune(0x202e))) {
				t.Fatalf("unsafe cursor leaked: %q", stderr.String())
			}
		})
	}
}

func TestFieldProjectionIsStrictAndWorksForNDJSON(t *testing.T) {
	var calls atomic.Int64
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(200, `{"trades":[{"trade_id":"1","ticker":"A","count_fp":"9.00"}],"cursor":"next"}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"trades", "list", "--fields", "trade_id,ticker", "--ndjson"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "count_fp") || !strings.Contains(stdout.String(), `"trade_id":"1"`) || !strings.Contains(stdout.String(), `"record_type":"summary"`) {
		t.Fatalf("output=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"trades", "list", "--fields", "unknown", "--compact"}); code != contract.ExitUsage {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || !strings.Contains(stderr.String(), `"code":"PROJECTION_FAILED"`) || !strings.Contains(stderr.String(), `"network":false`) {
		t.Fatalf("calls=%d error=%s", calls.Load(), stderr.String())
	}
}

func TestDiscoveryCanBeProjected(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	args := []string{"commands", "list", "--fields", "registry_version,commands.name,commands.summary", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "params_schema") || strings.Contains(stdout.String(), "global_options") || !strings.Contains(stdout.String(), `"name":"markets.list"`) {
		t.Fatalf("output=%s", stdout.String())
	}
	if stdout.Len() > 2000 {
		t.Fatalf("projected discovery grew to %d bytes", stdout.Len())
	}
}

func TestFieldsEqualsSyntaxAndRepeatedFlagHandling(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	if code := app.Run(context.Background(), []string{"commands", "list", "--fields=registry_version", "--compact"}); code != 0 {
		t.Fatalf("equals syntax exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"registry_version":"kalshi.registry/v1"`) || strings.Contains(stdout.String(), `"commands"`) {
		t.Fatalf("output=%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"commands", "list", "--fields", "registry_version", "--fields", "commands.name", "--compact"}); code != contract.ExitUsage {
		t.Fatalf("repeated fields exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `"code":"INVALID_ARGUMENT"`) {
		t.Fatalf("error=%s", stderr.String())
	}
}

func TestProjectableFieldsAreDiscoverableOffline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	args := []string{"commands", "describe", "markets.list", "--fields", "command.response_schema.x-projectable-fields", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"x-projectable-fields":[`) || !strings.Contains(stdout.String(), `"ticker"`) || !strings.Contains(stdout.String(), `"price_ranges.start"`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestKnownOptionalProjectionDoesNotDependOnReturnedValues(t *testing.T) {
	var calls atomic.Int64
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return response(200, `{"markets":[{"ticker":"A"}],"cursor":""}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"markets", "list", "--fields", "settlement_ts", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || !strings.Contains(stdout.String(), `"markets":[{}]`) {
		t.Fatalf("calls=%d output=%s", calls.Load(), stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"markets", "list", "--fields", "titlle", "--compact"}); code != contract.ExitUsage {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || !strings.Contains(stderr.String(), `"network":false`) {
		t.Fatalf("calls=%d error=%s", calls.Load(), stderr.String())
	}
}

func TestSingletonFieldProjectionPreservesWrapper(t *testing.T) {
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"market":{"ticker":"A","title":"Alpha","rules_primary":"omit"}}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	args := []string{"markets", "get", "--ticker", "A", "--fields", "market.ticker,market.title", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"market":{"ticker":"A","title":"Alpha"}`) || strings.Contains(stdout.String(), "rules_primary") {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestFieldsRejectedWhenTheyCouldHidePlansOrWrites(t *testing.T) {
	var calls atomic.Int64
	for _, args := range [][]string{
		{"markets", "list", "--fields", "ticker", "--dry-run", "--compact"},
		{"markets", "list", "--fields", "ticker", "--help", "--compact"},
		{"orders", "cancel", "--order-id", "order-1", "--fields", "order_id", "--compact"},
	} {
		var stdout, stderr bytes.Buffer
		app := New(Config{Stdout: &stdout, Stderr: &stderr, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("unexpected")
		})})
		if code := app.Run(context.Background(), args); code != contract.ExitUsage {
			t.Fatalf("args=%v exit=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), `"code":"INVALID_ARGUMENT"`) {
			t.Fatalf("args=%v error=%s", args, stderr.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls=%d", calls.Load())
	}
}

func TestEventsGetRequiresTickerBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected")
	})})
	if code := app.Run(context.Background(), []string{"events", "get", "--compact"}); code != contract.ExitUsage {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 0 || !strings.Contains(stderr.String(), `"code":"SCHEMA_VALIDATION_FAILED"`) {
		t.Fatalf("calls=%d error=%s", calls.Load(), stderr.String())
	}
}

func TestOptionalAuthCredentialFailureIsLocal(t *testing.T) {
	var calls atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout,
		Stderr: &stderr,
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("unexpected")
		}),
		Credentials: func() (auth.Credentials, error) {
			return auth.Credentials{}, errors.New("missing test credentials")
		},
	})
	args := []string{"orderbook", "get", "--ticker", "TEST-1", "--authenticated", "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitAuth {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 0 || !strings.Contains(stderr.String(), `"code":"CREDENTIALS_INVALID"`) || !strings.Contains(stderr.String(), `"network":false`) || strings.Contains(stderr.String(), `"retryable":true`) {
		t.Fatalf("calls=%d error=%s", calls.Load(), stderr.String())
	}
}

func TestSecretLookingUnknownArgIsNotEchoed(t *testing.T) {
	secret := "SUPER_SECRET_PEM_MATERIAL"
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	if code := app.Run(context.Background(), []string{"portfolio", "balance", "--api-key", secret, "--compact"}); code != contract.ExitUsage {
		t.Fatalf("exit=%d", code)
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatalf("secret was echoed: %s", stderr.String())
	}
}

func TestCursorCycleIsStableError(t *testing.T) {
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"markets":[],"cursor":"same"}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"markets", "list", "--cursor", "same", "--max-pages", "2", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `"code":"CURSOR_CYCLE"`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func extractDigest(t *testing.T, raw []byte) string {
	t.Helper()
	var envelope struct {
		Data struct {
			ConfirmationDigest string `json:"confirmation_digest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(envelope.Data.ConfirmationDigest, "sha256:") {
		t.Fatalf("digest=%q", envelope.Data.ConfirmationDigest)
	}
	return envelope.Data.ConfirmationDigest
}

func dryRunDigest(t *testing.T, params string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	if code := app.Run(context.Background(), []string{"orders", "create", "--params", params, "--write-policy", "demo-only", "--dry-run", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	return extractDigest(t, stdout.Bytes())
}

var _ api.Doer = roundTripFunc(nil)
