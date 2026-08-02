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
	"time"

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

type readFailBody struct{ closed bool }

func (b *readFailBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (b *readFailBody) Close() error {
	b.closed = true
	return nil
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

func TestReadRetriesRateLimitAndHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int64
	var waits []time.Duration
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			resp := response(http.StatusTooManyRequests, "slow down")
			resp.Header.Set("Retry-After", "2")
			return resp, nil
		}
		return response(http.StatusOK, `{"series":[]}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	})
	if code := app.Run(context.Background(), []string{"series", "list", "--compact"}); code != contract.ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || len(waits) != 1 || waits[0] != 2*time.Second {
		t.Fatalf("calls=%d waits=%v", calls.Load(), waits)
	}
	if !strings.Contains(stdout.String(), `"retry":{"attempts":2,"retries":1,"exhausted":false,"last_http_status":429,"last_retry_after_ms":2000}`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestReadRetriesRateLimitWhenErrorBodyReadFails(t *testing.T) {
	failedBody := &readFailBody{}
	var calls atomic.Int64
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: failedBody}, nil
		}
		return response(http.StatusOK, `{"series":[]}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if code := app.Run(context.Background(), []string{"series", "list", "--compact"}); code != contract.ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || !failedBody.closed {
		t.Fatalf("calls=%d body_closed=%t", calls.Load(), failedBody.closed)
	}
}

func TestAuthenticatedReadRetryIsResigned(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var calls, clock atomic.Int64
	var timestamps []string
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		timestamps = append(timestamps, req.Header.Get("KALSHI-ACCESS-TIMESTAMP"))
		if calls.Add(1) == 1 {
			return response(http.StatusTooManyRequests, `{}`), nil
		}
		return response(http.StatusOK, `{"balance":1,"balance_dollars":"0.01","portfolio_value":1,"updated_ts":1}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil },
		Now: func() time.Time {
			return time.UnixMilli(1700000000000 + clock.Add(1)*1000)
		},
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if code := app.Run(context.Background(), []string{"portfolio", "balance", "--compact"}); code != contract.ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || len(timestamps) != 2 || timestamps[0] == "" || timestamps[0] == timestamps[1] {
		t.Fatalf("calls=%d timestamps=%v", calls.Load(), timestamps)
	}
}

func TestRateLimitDelayIsBoundedAndRetryAfterIsFloor(t *testing.T) {
	for retry := 0; retry < maxRead429Retries; retry++ {
		base := retryBaseDelay << retry
		if base > retryMaxDelay {
			base = retryMaxDelay
		}
		delay := rateLimitDelay(retry, &api.UpstreamError{})
		if delay < base/2 || delay > base {
			t.Fatalf("retry=%d delay=%s expected_range=[%s,%s]", retry, delay, base/2, base)
		}
	}
	floor := 3 * time.Second
	if delay := rateLimitDelay(0, &api.UpstreamError{HasRetryAfter: true, RetryAfter: floor}); delay < floor {
		t.Fatalf("delay=%s floor=%s", delay, floor)
	}
}

func TestReadRateLimitRetriesAreBounded(t *testing.T) {
	var calls, waits atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return response(http.StatusTooManyRequests, `{}`), nil
		}),
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	if code := app.Run(context.Background(), []string{"series", "list", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != maxRead429Retries+1 || waits.Load() != maxRead429Retries {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if !strings.Contains(stderr.String(), `"http_status":429`) || !strings.Contains(stderr.String(), `"retryable":true`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), `"retry":{"attempts":6,"retries":5,"exhausted":true,"last_http_status":429}`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestReadDoesNotRetryServerErrors(t *testing.T) {
	var calls, waits atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return response(http.StatusServiceUnavailable, `{}`), nil
		}),
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	if code := app.Run(context.Background(), []string{"series", "list", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || waits.Load() != 0 {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if strings.Contains(stderr.String(), `"retry"`) {
		t.Fatalf("unexpected retry metadata: %s", stderr.String())
	}
}

func TestRateLimitRetryStopsBeforeExceedingTimeoutBudget(t *testing.T) {
	var calls, waits atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			resp := response(http.StatusTooManyRequests, `{}`)
			resp.Header.Set("Retry-After", "2")
			return resp, nil
		}),
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	if code := app.Run(context.Background(), []string{"series", "list", "--timeout", "1s", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || waits.Load() != 0 {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if !strings.Contains(stderr.String(), `"http_status":429`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), `"retry":{"attempts":1,"retries":0,"exhausted":true,"last_http_status":429,"last_retry_after_ms":2000}`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestRateLimitRetryDoesNotConsumePaginationPage(t *testing.T) {
	var calls atomic.Int64
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			return response(http.StatusTooManyRequests, `{}`), nil
		case 2:
			if req.URL.Query().Get("cursor") != "" {
				t.Fatalf("first page cursor=%q", req.URL.Query().Get("cursor"))
			}
			return response(http.StatusOK, `{"markets":[{"ticker":"A"}],"cursor":"next"}`), nil
		case 3:
			if req.URL.Query().Get("cursor") != "next" {
				t.Fatalf("second page cursor=%q", req.URL.Query().Get("cursor"))
			}
			return response(http.StatusOK, `{"markets":[{"ticker":"B"}],"cursor":""}`), nil
		default:
			t.Fatal("unexpected extra request")
			return nil, nil
		}
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if code := app.Run(context.Background(), []string{"markets", "list", "--max-pages", "2", "--max-items", "10", "--compact"}); code != contract.ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 3 || !strings.Contains(stdout.String(), `"pages_fetched":2`) || !strings.Contains(stdout.String(), `"items_scanned":2`) {
		t.Fatalf("calls=%d output=%s", calls.Load(), stdout.String())
	}
	if !strings.Contains(stdout.String(), `"retry":{"attempts":3,"retries":1,"exhausted":false,"last_http_status":429}`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestRateLimitRetryBudgetIsCommandWideAcrossPages(t *testing.T) {
	var calls, waits atomic.Int64
	doer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			if req.URL.Query().Get("cursor") != "" {
				t.Fatalf("first page cursor=%q", req.URL.Query().Get("cursor"))
			}
			return response(http.StatusTooManyRequests, `{}`), nil
		case 2:
			return response(http.StatusOK, `{"markets":[{"ticker":"A"}],"cursor":"next"}`), nil
		default:
			if req.URL.Query().Get("cursor") != "next" {
				t.Fatalf("second page cursor=%q", req.URL.Query().Get("cursor"))
			}
			return response(http.StatusTooManyRequests, `{}`), nil
		}
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	if code := app.Run(context.Background(), []string{"markets", "list", "--max-pages", "2", "--max-items", "10", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 7 || waits.Load() != maxRead429Retries {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if !strings.Contains(stderr.String(), `"retry":{"attempts":7,"retries":5,"exhausted":true,"last_http_status":429}`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestRateLimitRetryDeadlineDuringNextAttemptIsExhausted(t *testing.T) {
	var calls, waits atomic.Int64
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return response(http.StatusTooManyRequests, `{}`), nil
		}
		return nil, context.DeadlineExceeded
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	if code := app.Run(context.Background(), []string{"series", "list", "--timeout", "1s", "--compact"}); code != contract.ExitNetwork {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || waits.Load() != 1 {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if !strings.Contains(stderr.String(), `"code":"TIMEOUT"`) || !strings.Contains(stderr.String(), `"retry":{"attempts":2,"retries":1,"exhausted":true,"last_http_status":429}`) {
		t.Fatalf("stderr=%s", stderr.String())
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

func TestMalformedSuccessfulMutationResponseIsUnknown(t *testing.T) {
	params := validParams("orders.create")
	digest := dryRunDigest(t, params)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil },
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(201, `{"order_id":"order-1","fill_count":"0.00","ts_ms":1}`), nil
		}),
	})
	args := []string{"orders", "create", "--params", params, "--write-policy", "demo-only", "--confirm", digest, "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"code":"UPSTREAM_SCHEMA_MISMATCH"`, `"mutation_status":"unknown"`, `"missing_fields":["remaining_count"]`, `"outcome":"unknown"`, `orders reconcile`} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %s in %s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("partial mutation result leaked: %s", stdout.String())
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

func TestWriteRateLimitIsNeverRetried(t *testing.T) {
	params := validParams("orders.create")
	digest := dryRunDigest(t, params)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var calls, waits atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil },
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			resp := response(http.StatusTooManyRequests, `{}`)
			resp.Header.Set("Retry-After", "0")
			return resp, nil
		}),
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	args := []string{"orders", "create", "--params", params, "--write-policy", "demo-only", "--confirm", digest, "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || waits.Load() != 0 {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if !strings.Contains(stderr.String(), `"retryable":false`) || !strings.Contains(stderr.String(), `"mutation_status":"not_attempted"`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
	if strings.Contains(stderr.String(), `"retry"`) {
		t.Fatalf("write exposed retry metadata: %s", stderr.String())
	}
}

func TestCancelServerErrorIsNeverRetried(t *testing.T) {
	params := validParams("orders.cancel")
	digest := dryRunCancelDigest(t, params)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var calls, waits atomic.Int64
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil },
		HTTP: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls.Add(1)
			if req.Method != http.MethodDelete {
				t.Fatalf("method=%s", req.Method)
			}
			return response(http.StatusServiceUnavailable, `{}`), nil
		}),
		Wait: func(context.Context, time.Duration) error { waits.Add(1); return nil },
	})
	args := []string{"orders", "cancel", "--params", params, "--write-policy", "demo-only", "--confirm", digest, "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 1 || waits.Load() != 0 {
		t.Fatalf("calls=%d waits=%d", calls.Load(), waits.Load())
	}
	if !strings.Contains(stderr.String(), `"retryable":false`) || !strings.Contains(stderr.String(), `"mutation_status":"unknown"`) {
		t.Fatalf("stderr=%s", stderr.String())
	}
	if strings.Contains(stderr.String(), `"retry"`) {
		t.Fatalf("write exposed retry metadata: %s", stderr.String())
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
		return response(200, `{"orders":[{"order_id":"found","client_order_id":"client-1","ticker":"A"},{"order_id":"near","client_order_id":"client-10"}],"cursor":""}`), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer, Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil }})
	args := []string{"orders", "reconcile", "--client-order-id", "client-1", "--max-pages", "2", "--max-items", "10", "--fields", "order_id,ticker", "--require-fields", "ticker", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if !strings.Contains(stdout.String(), `"order_id":"found"`) || !strings.Contains(stdout.String(), `"ticker":"A"`) || strings.Contains(stdout.String(), `"client_order_id"`) || strings.Contains(stdout.String(), `"order_id":"near"`) || strings.Contains(stdout.String(), `"order_id":"other"`) {
		t.Fatalf("output=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"items_scanned":3`) || !strings.Contains(stdout.String(), `"items_returned":1`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestReconcileFailsIfLocalFilterFieldDisappears(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		Credentials: func() (auth.Credentials, error) { return auth.Credentials{KeyID: "kid", PrivateKey: key}, nil },
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(200, `{"orders":[{"order_id":"order-1"}],"cursor":""}`), nil
		}),
	})
	args := []string{"orders", "reconcile", "--client-order-id", "client-1", "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `"missing_fields":["orders[].client_order_id"]`) {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestSchemaDriftPreservesSafePaginationAndRetryMetadata(t *testing.T) {
	var calls atomic.Int64
	appDoer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			return response(200, `{"markets":[{"ticker":"A"}],"cursor":"next"}`), nil
		case 2:
			return response(http.StatusTooManyRequests, `{}`), nil
		default:
			return response(200, `{"markets":[{"title":"missing identity"}],"cursor":"unsafe-next"}`), nil
		}
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: appDoer,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if code := app.Run(context.Background(), []string{"markets", "list", "--max-pages", "2", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"retry":{"attempts":3,"retries":1`, `"pagination":{"pages_fetched":2,"items_scanned":1,"items_returned":1}`, `"missing_fields":["markets[].ticker"]`} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %s in %s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "unsafe-next") {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestAbsentAndNullCursorsNormalizeToTerminal(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"markets":[]}`,
		"null":   `{"markets":[],"cursor":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := New(Config{
				Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
				HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil }),
			})
			if code := app.Run(context.Background(), []string{"markets", "list", "--compact"}); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), `"cursor":""`) {
				t.Fatalf("output=%s", stdout.String())
			}
		})
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

func TestOutputLimitPreservesRateLimitRetryMetadata(t *testing.T) {
	var calls atomic.Int64
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return response(http.StatusTooManyRequests, `{}`), nil
		}
		items := []map[string]string{{"ticker": "A", "rules": strings.Repeat("x", 2000)}}
		raw, _ := json.Marshal(map[string]any{"markets": items, "cursor": ""})
		return response(http.StatusOK, string(raw)), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	args := []string{"markets", "list", "--max-items", "1", "--max-bytes", "1024", "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitOutput {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || stdout.Len() != 0 {
		t.Fatalf("calls=%d stdout=%s", calls.Load(), stdout.String())
	}
	if !strings.Contains(stderr.String(), `"code":"OUTPUT_LIMIT"`) || !strings.Contains(stderr.String(), `"retry":{"attempts":2,"retries":1,"exhausted":false,"last_http_status":429}`) {
		t.Fatalf("stderr=%s", stderr.String())
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
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if !strings.Contains(line, `"output_contract_version":"kalshi.output/trades.list/v1"`) {
			t.Fatalf("NDJSON record lacks output contract version: %s", line)
		}
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
	if !strings.Contains(stdout.String(), `"registry_version":"kalshi.registry/v3"`) || strings.Contains(stdout.String(), `"commands"`) {
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

func TestResponseContractExtensionKeysAreStableOffline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	args := []string{
		"commands", "describe", "events.list",
		"--fields", "command.response_schema.x-projectable-fields,command.response_schema.x-projected-field-contracts,command.response_schema.x-cursor-aliases",
		"--compact",
	}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"x-projectable-fields":[`, `"x-projected-field-contracts":{"title":{`, `"x-cursor-aliases":["next_cursor"]`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("missing %s in %s", want, stdout.String())
		}
	}
}

func TestKnownOptionalProjectionMaterializesNull(t *testing.T) {
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
	if calls.Load() != 1 || !strings.Contains(stdout.String(), `"markets":[{"settlement_ts":null}]`) {
		t.Fatalf("calls=%d output=%s", calls.Load(), stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"markets", "list", "--fields", "ticker,title", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || !strings.Contains(stdout.String(), `"markets":[{"ticker":"A","title":null}]`) {
		t.Fatalf("calls=%d output=%s", calls.Load(), stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"markets", "list", "--fields", "titlle", "--compact"}); code != contract.ExitUsage {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 || !strings.Contains(stderr.String(), `"network":false`) {
		t.Fatalf("calls=%d error=%s", calls.Load(), stderr.String())
	}
}

func TestRequiredProjectionFailsClosedWithNamedField(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(200, `{"markets":[{"ticker":"A"}],"cursor":""}`), nil
		}),
	})
	args := []string{"markets", "list", "--fields", "ticker,title", "--require-fields", "title", "--compact"}
	if code := app.Run(context.Background(), args); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"code":"UPSTREAM_SCHEMA_MISMATCH"`, `"missing_fields":["markets[].title"]`, `"output_contract_version":"kalshi.output/markets.list/v1"`} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %s in %s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("partial stdout=%s", stdout.String())
	}
}

func TestRequiredFieldsMustBeProjectableAndCoveredByProjection(t *testing.T) {
	for _, args := range [][]string{
		{"markets", "list", "--require-fields", "titlle", "--compact"},
		{"markets", "list", "--fields", "ticker", "--require-fields", "title", "--compact"},
	} {
		var stdout, stderr bytes.Buffer
		app := New(Config{Stdout: &stdout, Stderr: &stderr, HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid required fields performed network I/O")
			return nil, nil
		})})
		if code := app.Run(context.Background(), args); code != contract.ExitUsage {
			t.Fatalf("args=%v exit=%d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), `"code":"PROJECTION_FAILED"`) {
			t.Fatalf("args=%v stderr=%s", args, stderr.String())
		}
	}
}

func TestProjectedFieldTypeAndFormatDrift(t *testing.T) {
	for name, test := range map[string]struct {
		body string
		args []string
		want string
	}{
		"type": {
			body: `{"markets":[{"ticker":"A","title":42}],"cursor":""}`,
			args: []string{"markets", "list", "--fields", "ticker,title", "--compact"},
			want: `"type_mismatches":[{"field":"markets[].title","expected":"string","actual":"number"}]`,
		},
		"format": {
			body: `{"markets":[{"ticker":"A","close_time":"tomorrow"}],"cursor":""}`,
			args: []string{"markets", "list", "--fields", "ticker,close_time", "--compact"},
			want: `"format_mismatches":[{"field":"markets[].close_time","expected":"date-time","actual":"invalid"}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(200, test.body), nil
			})})
			if code := app.Run(context.Background(), test.args); code != contract.ExitUpstream {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRenamedNonemptyCursorFailsClosedWithoutLeakingToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(200, `{"markets":[{"ticker":"A"}],"next_cursor":"secret-page-token"}`), nil
		}),
	})
	if code := app.Run(context.Background(), []string{"markets", "list", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"missing_fields":["cursor"]`, `"unexpected_fields":["next_cursor"]`} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %s in %s", want, stderr.String())
		}
	}
	if stdout.Len() != 0 || strings.Contains(stderr.String(), "secret-page-token") {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
}

func TestOutputContractVersionAppearsOnSuccessAndKnownErrors(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		app := New(Config{
			Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
			HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(200, `{"exchange_active":true,"trading_active":true}`), nil
			}),
		})
		if code := app.Run(context.Background(), []string{"exchange", "status", "--compact"}); code != 0 {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"output_contract_version":"kalshi.output/exchange.status/v1"`) {
			t.Fatalf("output=%s", stdout.String())
		}
	})

	t.Run("preflight error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		app := New(Config{Stdout: &stdout, Stderr: &stderr})
		if code := app.Run(context.Background(), []string{"markets", "get", "--compact"}); code != contract.ExitUsage {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		if !strings.Contains(stderr.String(), `"output_contract_version":"kalshi.output/markets.get/v1"`) {
			t.Fatalf("error=%s", stderr.String())
		}
	})
}

func TestOutputContractDriftNamesMissingRequiredFields(t *testing.T) {
	for name, test := range map[string]struct {
		args        []string
		body        string
		wantMissing string
	}{
		"top-level field": {
			args:        []string{"exchange", "status", "--compact"},
			body:        `{"exchange_active":true}`,
			wantMissing: "trading_active",
		},
		"collection item field": {
			args:        []string{"markets", "list", "--compact"},
			body:        `{"markets":[{"ticker":"A"},{"title":"missing identity"}],"cursor":""}`,
			wantMissing: "markets[].ticker",
		},
		"singleton field": {
			args:        []string{"markets", "get", "--ticker", "A", "--compact"},
			body:        `{"market":{"title":"missing identity"}}`,
			wantMissing: "market.ticker",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := New(Config{
				Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
				HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, test.body), nil }),
			})
			if code := app.Run(context.Background(), test.args); code != contract.ExitUpstream {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"UPSTREAM_SCHEMA_MISMATCH"`) || !strings.Contains(stderr.String(), test.wantMissing) || !strings.Contains(stderr.String(), `"missing_fields"`) {
				t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
			}
		})
	}
}

func TestOutputContractDriftReportsTypeMismatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(200, `{"exchange_active":"yes","trading_active":true}`), nil
		}),
	})
	if code := app.Run(context.Background(), []string{"exchange", "status", "--compact"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"code":"UPSTREAM_SCHEMA_MISMATCH"`, `"type_mismatches"`, `"field":"exchange_active"`, `"expected":"boolean"`, `"actual":"string"`} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("missing %s in %s", want, stderr.String())
		}
	}
}

func TestNDJSONSchemaDriftIsAtomic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(Config{
		Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2",
		HTTP: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response(200, `{"markets":[{"title":"missing identity"}],"cursor":""}`), nil
		}),
	})
	if code := app.Run(context.Background(), []string{"markets", "list", "--ndjson"}); code != contract.ExitUpstream {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `"record_type":"result"`) || !strings.Contains(stderr.String(), `"output_contract_version":"kalshi.output/markets.list/v1"`) {
		t.Fatalf("stdout=%s stderr=%s", stdout.String(), stderr.String())
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

func dryRunCancelDigest(t *testing.T, params string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr})
	if code := app.Run(context.Background(), []string{"orders", "cancel", "--params", params, "--write-policy", "demo-only", "--dry-run", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	return extractDigest(t, stdout.Bytes())
}

var _ api.Doer = roundTripFunc(nil)
