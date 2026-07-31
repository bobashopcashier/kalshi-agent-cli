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

	"kalshi-agent-cli/internal/auth"
)

const userAgent = "kalshi-agent-cli/1"

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
	Status    int
	Code      string
	Message   string
	Details   any
	Retryable bool
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream HTTP %d: %s", e.Status, e.Message)
}

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
			return nil, errors.New("authenticated command has no credential source")
		}
		creds, err := c.Credentials()
		if err != nil {
			return nil, err
		}
		now := time.Now
		if c.Now != nil {
			now = c.Now
		}
		ts := strconv.FormatInt(now().UnixMilli(), 10)
		signature, err := creds.Sign(ts, in.Method, req.URL.EscapedPath())
		if err != nil {
			return nil, fmt.Errorf("sign request: %w", err)
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
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = 8 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("upstream response exceeded byte limit")
	}
	var decoded map[string]any
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("upstream returned invalid JSON: %w", err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, errors.New("upstream returned more than one JSON value")
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		up := &UpstreamError{Status: resp.StatusCode, Code: "upstream_error", Message: http.StatusText(resp.StatusCode), Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500}
		if v, ok := decoded["code"].(string); ok && v != "" {
			up.Code = v
		}
		if v, ok := decoded["message"].(string); ok && v != "" {
			up.Message = v
		}
		if v, ok := decoded["details"]; ok {
			up.Details = v
		}
		return nil, up
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("redirects are disabled to protect credentials")
		},
	}
}
