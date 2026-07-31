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

	"kalshi-agent-cli/internal/api"
	"kalshi-agent-cli/internal/auth"
	"kalshi-agent-cli/internal/contract"
	"kalshi-agent-cli/internal/registry"
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
	args := []string{"orders", "reconcile", "--client-order-id", "client-1", "--max-pages", "2", "--max-items", "10", "--compact"}
	if code := app.Run(context.Background(), args); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if !strings.Contains(stdout.String(), `"order_id":"found"`) || strings.Contains(stdout.String(), `"order_id":"near"`) || strings.Contains(stdout.String(), `"order_id":"other"`) {
		t.Fatalf("output=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"items_scanned":3`) || !strings.Contains(stdout.String(), `"items_returned":1`) {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestResponseSanitizationAndOutputByteCap(t *testing.T) {
	unsafe := "bad\x1b[31m" + string(rune(0x202e))
	doer := roundTripFunc(func(*http.Request) (*http.Response, error) {
		items := make([]map[string]string, 100)
		for i := range items {
			items[i] = map[string]string{"ticker": unsafe + strings.Repeat("x", 200)}
		}
		raw, _ := json.Marshal(map[string]any{"markets": items, "cursor": "next"})
		return response(200, string(raw)), nil
	})
	var stdout, stderr bytes.Buffer
	app := New(Config{Stdout: &stdout, Stderr: &stderr, BaseURL: "https://example.test/trade-api/v2", HTTP: doer})
	if code := app.Run(context.Background(), []string{"markets", "list", "--max-bytes", "1024", "--compact"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() > 1024 {
		t.Fatalf("output=%d bytes", stdout.Len())
	}
	if strings.ContainsRune(stdout.String(), '\x1b') || strings.Contains(stdout.String(), string(rune(0x202e))) {
		t.Fatalf("unsafe output=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `max_bytes`) || !strings.Contains(stdout.String(), `\\u001B`) {
		t.Fatalf("missing sanitization/truncation metadata: %s", stdout.String())
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
