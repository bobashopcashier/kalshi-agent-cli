package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"kalshi-cli/internal/auth"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

type readErrorBody struct{ closed bool }

func (b *readErrorBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (b *readErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestClientSignsPathWithoutQuery(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	creds := auth.Credentials{KeyID: "kid", PrivateKey: key}
	client := Client{
		BaseURL: "https://example.test/trade-api/v2", Now: func() time.Time { return time.UnixMilli(1700000000000) },
		Credentials: func() (auth.Credentials, error) { return creds, nil },
		HTTP: doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/trade-api/v2/portfolio/balance" || req.URL.RawQuery != "subaccount=2" {
				t.Fatalf("url=%s", req.URL)
			}
			if got := req.Header.Get("User-Agent"); got != "kalshi-cli/1" {
				t.Fatalf("user agent=%q", got)
			}
			if req.Header.Get("KALSHI-ACCESS-KEY") != "kid" || req.Header.Get("KALSHI-ACCESS-TIMESTAMP") != "1700000000000" || req.Header.Get("KALSHI-ACCESS-SIGNATURE") == "" {
				t.Fatalf("missing auth headers: %#v", req.Header)
			}
			return jsonResponse(200, `{"balance":1}`), nil
		}),
	}
	data, err := client.Do(context.Background(), Request{Method: "GET", Path: "/portfolio/balance", Query: url.Values{"subaccount": {"2"}}, Auth: true})
	if err != nil || data["balance"] == nil {
		t.Fatalf("data=%#v err=%v", data, err)
	}
}

func TestClientBoundsAndClassifiesUpstream(t *testing.T) {
	client := Client{BaseURL: "https://example.test/trade-api/v2", MaxResponseBytes: 8, HTTP: doerFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{"long":"payload"}`), nil })}
	if _, err := client.Do(context.Background(), Request{Method: "GET", Path: "/x"}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("err=%v", err)
	}
	client.MaxResponseBytes = 1024
	client.HTTP = doerFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(429, `{"code":"limited","message":"slow down","details":"later"}`), nil
	})
	_, err := client.Do(context.Background(), Request{Method: "GET", Path: "/x"})
	up, ok := err.(*UpstreamError)
	if !ok || !up.Retryable || up.Code != "limited" {
		t.Fatalf("error=%#v", err)
	}
	client.HTTP = doerFunc(func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{} {}`), nil })
	if _, err := client.Do(context.Background(), Request{Method: "GET", Path: "/x"}); err == nil || !strings.Contains(err.Error(), "more than one JSON") {
		t.Fatalf("trailing JSON err=%v", err)
	}
}

func TestClientClassifiesPlainTextRateLimitAndRetryAfter(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("slow down")}
	client := Client{
		BaseURL: "https://example.test/trade-api/v2",
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"2"}}, Body: body}, nil
		}),
	}
	_, err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/x"})
	var up *UpstreamError
	if !errors.As(err, &up) || up.Status != http.StatusTooManyRequests || !up.Retryable {
		t.Fatalf("error=%#v", err)
	}
	if !up.HasRetryAfter || up.RetryAfter != 2*time.Second {
		t.Fatalf("retry_after=%s present=%t", up.RetryAfter, up.HasRetryAfter)
	}
	if !body.closed {
		t.Fatal("rate-limit response body was not closed")
	}
}

func TestClientParsesRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	client := Client{
		BaseURL: "https://example.test/trade-api/v2",
		Now:     func() time.Time { return now },
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			resp := jsonResponse(http.StatusTooManyRequests, `{}`)
			resp.Header.Set("Retry-After", now.Add(3*time.Second).Format(http.TimeFormat))
			return resp, nil
		}),
	}
	_, err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/x"})
	var up *UpstreamError
	if !errors.As(err, &up) || !up.HasRetryAfter || up.RetryAfter != 3*time.Second {
		t.Fatalf("error=%#v", err)
	}
}

func TestClientClassifiesRateLimitWhenBodyReadFails(t *testing.T) {
	body := &readErrorBody{}
	client := Client{
		BaseURL: "https://example.test/trade-api/v2",
		HTTP: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: body}, nil
		}),
	}
	_, err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "/x"})
	var up *UpstreamError
	if !errors.As(err, &up) || up.Status != http.StatusTooManyRequests || !up.Retryable {
		t.Fatalf("error=%#v", err)
	}
	if !body.closed {
		t.Fatal("rate-limit response body was not closed")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(1994, time.November, 6, 8, 49, 36, 0, time.UTC)
	tests := []struct {
		name string
		raw  string
		want time.Duration
		ok   bool
	}{
		{name: "empty", raw: "", ok: false},
		{name: "whitespace", raw: "   ", ok: false},
		{name: "zero", raw: "0", want: 0, ok: true},
		{name: "leading zero", raw: "02", want: 2 * time.Second, ok: true},
		{name: "trimmed", raw: " 2 ", want: 2 * time.Second, ok: true},
		{name: "plus", raw: "+2", ok: false},
		{name: "negative zero", raw: "-0", ok: false},
		{name: "negative", raw: "-2", ok: false},
		{name: "fractional", raw: "1.5", ok: false},
		{name: "malformed", raw: "later", ok: false},
		{name: "overflow", raw: "999999999999999999999999999", ok: false},
		{name: "http date", raw: "Sun, 06 Nov 1994 08:49:37 GMT", want: time.Second, ok: true},
		{name: "rfc850 date", raw: "Sunday, 06-Nov-94 08:49:37 GMT", want: time.Second, ok: true},
		{name: "asctime date", raw: "Sun Nov  6 08:49:37 1994", want: time.Second, ok: true},
		{name: "past date", raw: "Sun, 06 Nov 1994 08:49:35 GMT", want: 0, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tt.raw, now)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("parseRetryAfter(%q)=(%s,%t), want (%s,%t)", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
