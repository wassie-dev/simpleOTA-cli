package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Error is a non-2xx response from the API.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Detail     string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s %s: server returned %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, e.Detail)
}

// IsAuth reports whether the server rejected the token or its permissions.
func (e *Error) IsAuth() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

const (
	requestTimeout = 30 * time.Second
	uploadTimeout  = 10 * time.Minute
	maxResponse    = 8 << 20
	maxAttempts    = 3
)

// Client talks to one SimpleOTA server with one bearer token.
type Client struct {
	baseURL   string
	token     string
	userAgent string
	http      *http.Client
	sleep     func(context.Context, time.Duration) error
}

// Option customises a Client.
type Option func(*Client)

// WithSleep replaces the retry backoff sleep, for tests.
func WithSleep(f func(context.Context, time.Duration) error) Option {
	return func(c *Client) { c.sleep = f }
}

// New returns a client for baseURL, which must already be validated by
// config.NormalizeAPIURL.
func New(baseURL, token, userAgent string, opts ...Option) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		userAgent: userAgent,
		http:      defaultHTTPClient(),
		sleep:     sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func defaultHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: transport,
		// A redirect would mean a wrong --api-url; following it could
		// downgrade the scheme or drop the request body.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	// A non-replayable body: the transport must never resend it.
	req.GetBody = nil
	return req, nil
}

// sendJSON sends a write request, with in as the JSON body unless it is
// nil, and decodes the response into out unless it is nil. Writes are sent
// once and never retried.
func (c *Client) sendJSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.doOnce(req, path, out)
}

// upload posts form fields and a binary as multipart/form-data. The body is
// streamed with a known Content-Length and sent once.
func (c *Client) upload(ctx context.Context, path string, fields [][2]string, fileName string, binary []byte, out any) error {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return err
		}
	}
	if _, err := mw.CreateFormFile("binary", fileName); err != nil {
		return err
	}
	prefixLen := head.Len()
	if err := mw.Close(); err != nil {
		return err
	}
	all := head.Bytes()
	prefix, trailer := all[:prefixLen], all[prefixLen:]
	body := io.MultiReader(bytes.NewReader(prefix), bytes.NewReader(binary), bytes.NewReader(trailer))

	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(prefix) + len(binary) + len(trailer))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.doOnce(req, path, out)
}

// get retries network errors and 429/502/503/504 with bounded, jittered
// exponential backoff.
func (c *Client) get(ctx context.Context, path string, out any) error {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, backoff(attempt, lastErr)); err != nil {
				return err
			}
		}
		lastErr = c.getOnce(ctx, path, out)
		if lastErr == nil || !retryable(ctx, lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) getOnce(ctx context.Context, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.doOnce(req, path, out)
}

type retryAfterError struct {
	apiErr *Error
	after  time.Duration
}

func (e *retryAfterError) Error() string { return e.apiErr.Error() }

func (e *retryAfterError) Unwrap() error { return e.apiErr }

func (c *Client) doOnce(req *http.Request, path string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return &transportError{err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return &transportError{err: err}
	}
	if len(body) > maxResponse {
		return fmt.Errorf("%s %s: response is larger than %d bytes", req.Method, path, maxResponse)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("the server redirected %s to %q; check --api-url", path, resp.Header.Get("Location"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &Error{StatusCode: resp.StatusCode, Method: req.Method, Path: path, Detail: errorDetail(body)}
		if d := retryAfter(resp.Header.Get("Retry-After")); d > 0 {
			return &retryAfterError{apiErr: apiErr, after: d}
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s %s: unexpected response from the server: %w", req.Method, path, err)
	}
	return nil
}

type transportError struct{ err error }

func (e *transportError) Error() string { return "could not reach SimpleOTA: " + e.err.Error() }

func (e *transportError) Unwrap() error { return e.err }

func retryable(ctx context.Context, err error) bool {
	return ctx.Err() == nil && IsTransient(err)
}

// IsTransient reports whether err is a network failure or a 429/502/503/504
// that may succeed when repeated. Certificate failures are not transient.
func IsTransient(err error) bool {
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}
	var te *transportError
	if errors.As(err, &te) {
		return !errors.Is(err, context.Canceled)
	}
	var ae *Error
	if errors.As(err, &ae) {
		switch ae.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

func backoff(attempt int, lastErr error) time.Duration {
	var ra *retryAfterError
	if errors.As(lastErr, &ra) {
		return ra.after
	}
	base := 500 * time.Millisecond << (attempt - 1)
	return base + rand.N(base/2) // #nosec G404 -- backoff jitter, not a secret
}

func retryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, 10*time.Second)
}

// errorDetail extracts Django Ninja's {"detail": ...} message.
func errorDetail(body []byte) string {
	var env struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Detail) == 0 {
		text := strings.TrimSpace(string(body))
		if strings.HasPrefix(text, "<") {
			return ""
		}
		return truncate(text, 300)
	}
	var s string
	if err := json.Unmarshal(env.Detail, &s); err == nil {
		return s
	}
	return truncate(string(env.Detail), 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
