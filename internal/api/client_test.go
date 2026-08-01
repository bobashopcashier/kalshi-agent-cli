package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
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

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
