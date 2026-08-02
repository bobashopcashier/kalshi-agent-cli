package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bobashopcashier/kalshi-cli/internal/auth"
)

const userAgent = "kalshi-cli/1"

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	BaseURL          string
	HTTP             Doer
	Credentials      func() (auth.Credentials, error)
	Now              func() time.Time
	MaxResponseBytes int64
}

type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   any
	Auth   bool
}

type UpstreamError struct {
	Status        int
	Code          string
	Message       string
	Details       any
	Retryable     bool
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream HTTP %d: %s", e.Status, e.Message)
}

type CredentialError struct{ Err error }

func (e *CredentialError) Error() string { return e.Err.Error() }
func (e *CredentialError) Unwrap() error { return e.Err }

func (c *Client) Do(ctx context.Context, in Request) (map[string]any, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	if base.Scheme != "https" && base.Scheme != "http" {
		return nil, errors.New("base URL must use http or https")
	}
	base.Path = strings.TrimRight(base.Path, "/") + in.Path
	base.RawQuery = in.Query.Encode()
	var body io.Reader
	if in.Body != nil {
		payload, err := json.Marshal(in.Body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, in.Method, base.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if in.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if in.Auth {
		if c.Credentials == nil {
			return nil, &CredentialError{Err: errors.New("authenticated command has no credential source")}
		}
		creds, err := c.Credentials()
		if err != nil {
			return nil, &CredentialError{Err: err}
		}
		now := time.Now
		if c.Now != nil {
			now = c.Now
		}
		ts := strconv.FormatInt(now().UnixMilli(), 10)
		signature, err := creds.Sign(ts, in.Method, req.URL.EscapedPath())
		if err != nil {
			return nil, &CredentialError{Err: fmt.Errorf("sign request: %w", err)}
		}
		req.Header.Set("KALSHI-ACCESS-KEY", creds.KeyID)
		req.Header.Set("KALSHI-ACCESS-TIMESTAMP", ts)
		req.Header.Set("KALSHI-ACCESS-SIGNATURE", signature)
	}
	doer := c.HTTP
	if doer == nil {
		doer = defaultHTTPClient()
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	var upstream *UpstreamError
	if !success {
		retryAfter, hasRetryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
		upstream = &UpstreamError{
			Status: resp.StatusCode, Code: "upstream_error", Message: http.StatusText(resp.StatusCode),
			Retryable:  resp.StatusCode == 429 || resp.StatusCode >= 500,
			RetryAfter: retryAfter, HasRetryAfter: hasRetryAfter,
		}
	}
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = 8 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if upstream != nil {
			return nil, upstream
		}
		return nil, err
	}
	if int64(len(raw)) > limit {
		if upstream != nil {
			return nil, upstream
		}
		return nil, errors.New("upstream response exceeded byte limit")
	}
	decoded, decodeErr := decodeObject(raw)
	if !success {
		// Status and headers remain authoritative for non-success responses. Error
		// bodies are optional upstream diagnostics and may be empty or non-JSON.
		if decodeErr != nil {
			return nil, upstream
		}
		if v, ok := decoded["code"].(string); ok && v != "" {
			upstream.Code = v
		}
		if v, ok := decoded["message"].(string); ok && v != "" {
			upstream.Message = v
		}
		if v, ok := decoded["details"]; ok {
			upstream.Details = v
		}
		return nil, upstream
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func decodeObject(raw []byte) (map[string]any, error) {
	var decoded map[string]any
	if len(raw) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("upstream returned invalid JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("upstream returned more than one JSON value")
	}
	return decoded, nil
}

func parseRetryAfter(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			when, err := http.ParseTime(raw)
			if err != nil {
				return 0, false
			}
			delay := when.Sub(now)
			if delay < 0 {
				delay = 0
			}
			return delay, true
		}
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		const maxDurationSeconds = int64(^uint64(0)>>1) / int64(time.Second)
		if seconds < 0 || seconds > maxDurationSeconds {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	return 0, false
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are disabled to protect credentials")
		},
	}
}
