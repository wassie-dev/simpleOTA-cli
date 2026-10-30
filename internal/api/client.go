// Package api is a client for the SimpleOTA developer REST API (/api/v1/).
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
	"net/url"
	"strconv"
	"strings"
	"time"
)

// API is the subset of the developer API the CLI uses.
type API interface {
	Me(ctx context.Context) (Me, error)
	ListProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, projectID string) (Project, error)
	ListSigningKeys(ctx context.Context, projectID string) ([]SigningKey, error)
	RegisterSigningKey(ctx context.Context, projectID, keyID, publicKeyPEM string) (SigningKey, error)
	Release(ctx context.Context, projectID string, req ReleaseRequest) (ReleaseResult, error)
	ListArtifacts(ctx context.Context, projectID string) ([]Artifact, error)
	ListDevices(ctx context.Context, projectID string, f DeviceFilter) ([]Device, error)
	GetDevice(ctx context.Context, projectID, deviceID string) (Device, error)
	ListChannels(ctx context.Context, projectID string) ([]Channel, error)
	ListGroups(ctx context.Context, projectID string) ([]Group, error)
	ListDeployments(ctx context.Context, projectID string) ([]Deployment, error)
	GetDeployment(ctx context.Context, projectID, deploymentID string) (Deployment, error)
	TransitionDeployment(ctx context.Context, projectID, deploymentID string, t Transition) (Deployment, error)
	ListDeploymentStates(ctx context.Context, projectID, deploymentID string) ([]DeviceState, error)
	UploadArtifact(ctx context.Context, projectID string, req UploadRequest) (Artifact, error)
	UpdateArtifact(ctx context.Context, projectID, artifactID string, archived bool) (Artifact, error)
	CreateDeployment(ctx context.Context, projectID string, in DeploymentRequest) (Deployment, error)
	UpdateDevice(ctx context.Context, projectID, deviceID string, in DeviceUpdate) (Device, error)
	ListCheckEvents(ctx context.Context, projectID, deviceID string, limit int) ([]CheckEvent, error)
	CreateChannel(ctx context.Context, projectID string, in ChannelRequest) (Channel, error)
	CreateGroup(ctx context.Context, projectID string, in GroupRequest) (Group, error)
	ListGroupMembers(ctx context.Context, projectID, groupID string) ([]Device, error)
	AddGroupMembers(ctx context.Context, projectID, groupID string, deviceIDs []string) error
	RemoveGroupMember(ctx context.Context, projectID, groupID, deviceID string) error
	RevokeSigningKey(ctx context.Context, projectID, keyID string) error
	ListAudit(ctx context.Context, projectID, eventType string, limit int) ([]AuditEvent, error)
}

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

func projectPath(projectID string, rest ...string) string {
	p := "/api/v1/projects/" + url.PathEscape(projectID) + "/"
	for _, r := range rest {
		p += url.PathEscape(r) + "/"
	}
	return p
}

// Me reports who the token belongs to: the account for a personal token, the
// project for a project token.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var out Me
	return out, c.get(ctx, "/api/v1/me/", &out)
}

// ListProjects lists the projects the token can see. The API lists projects
// for a staff account only when given an owner, so for a staff token it asks
// /me/ for the account and lists that account's own projects. A server
// without /me/ leaves the 400 in place.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := c.get(ctx, "/api/v1/projects/", &out)
	var ae *Error
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusBadRequest {
		return out, err
	}
	me, meErr := c.Me(ctx)
	if meErr != nil || me.Account == nil || !me.Account.IsStaff {
		return nil, err
	}
	out = nil
	return out, c.get(ctx, "/api/v1/projects/?owner_id="+strconv.Itoa(me.Account.ID), &out)
}

func (c *Client) GetProject(ctx context.Context, projectID string) (Project, error) {
	var out Project
	return out, c.get(ctx, projectPath(projectID), &out)
}

func (c *Client) ListSigningKeys(ctx context.Context, projectID string) ([]SigningKey, error) {
	var out []SigningKey
	return out, c.get(ctx, projectPath(projectID, "signing-keys"), &out)
}

// RegisterSigningKey registers a locally generated public key. It refuses an
// empty key, because the server would then generate a keypair itself.
func (c *Client) RegisterSigningKey(ctx context.Context, projectID, keyID, publicKeyPEM string) (SigningKey, error) {
	var out SigningKey
	if strings.TrimSpace(publicKeyPEM) == "" {
		return out, errors.New("refusing to register a signing key without a public key")
	}
	body := map[string]string{"key_id": keyID, "public_key_pem": publicKeyPEM}
	return out, c.postJSON(ctx, projectPath(projectID, "signing-keys"), body, &out)
}

func (c *Client) ListArtifacts(ctx context.Context, projectID string) ([]Artifact, error) {
	var out []Artifact
	return out, c.get(ctx, projectPath(projectID, "artifacts"), &out)
}

func (c *Client) ListDevices(ctx context.Context, projectID string, f DeviceFilter) ([]Device, error) {
	q := url.Values{}
	if f.ChannelID != "" {
		q.Set("channel_id", f.ChannelID)
	}
	if f.GroupID != "" {
		q.Set("group_id", f.GroupID)
	}
	if f.Active != nil {
		q.Set("active", strconv.FormatBool(*f.Active))
	}
	path := projectPath(projectID, "devices")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []Device
	return out, c.get(ctx, path, &out)
}

func (c *Client) GetDevice(ctx context.Context, projectID, deviceID string) (Device, error) {
	var out Device
	return out, c.get(ctx, projectPath(projectID, "devices", deviceID), &out)
}

func (c *Client) ListChannels(ctx context.Context, projectID string) ([]Channel, error) {
	var out []Channel
	return out, c.get(ctx, projectPath(projectID, "channels"), &out)
}

func (c *Client) ListGroups(ctx context.Context, projectID string) ([]Group, error) {
	var out []Group
	return out, c.get(ctx, projectPath(projectID, "groups"), &out)
}

func (c *Client) ListDeployments(ctx context.Context, projectID string) ([]Deployment, error) {
	var out []Deployment
	return out, c.get(ctx, projectPath(projectID, "deployments"), &out)
}

func (c *Client) GetDeployment(ctx context.Context, projectID, deploymentID string) (Deployment, error) {
	var out Deployment
	return out, c.get(ctx, projectPath(projectID, "deployments", deploymentID), &out)
}

func (c *Client) TransitionDeployment(ctx context.Context, projectID, deploymentID string, t Transition) (Deployment, error) {
	var out Deployment
	return out, c.postJSON(ctx, projectPath(projectID, "deployments", deploymentID, "transition"), t, &out)
}

func (c *Client) ListDeploymentStates(ctx context.Context, projectID, deploymentID string) ([]DeviceState, error) {
	var out []DeviceState
	return out, c.get(ctx, projectPath(projectID, "deployments", deploymentID, "states"), &out)
}

// Release uploads the binary and starts a deployment in one request. It is
// never retried: every accepted upload consumes a new build number.
func (c *Client) Release(ctx context.Context, projectID string, req ReleaseRequest) (ReleaseResult, error) {
	var out ReleaseResult
	fields := [][2]string{
		{"manifest", string(req.ManifestJSON)},
		{"rollout_percentage", strconv.Itoa(req.RolloutPercentage)},
		{"channel_name", req.ChannelName},
	}
	return out, c.upload(ctx, projectPath(projectID, "release"), fields, req.FileName, req.Binary, &out)
}

// UploadArtifact uploads a build without deploying it. Like Release, it is
// never retried.
func (c *Client) UploadArtifact(ctx context.Context, projectID string, req UploadRequest) (Artifact, error) {
	var out Artifact
	fields := [][2]string{{"manifest", string(req.ManifestJSON)}}
	return out, c.upload(ctx, projectPath(projectID, "artifacts"), fields, req.FileName, req.Binary, &out)
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

func (c *Client) UpdateArtifact(ctx context.Context, projectID, artifactID string, archived bool) (Artifact, error) {
	var out Artifact
	return out, c.sendJSON(ctx, http.MethodPatch, projectPath(projectID, "artifacts", artifactID), map[string]bool{"archived": archived}, &out)
}

func (c *Client) CreateDeployment(ctx context.Context, projectID string, in DeploymentRequest) (Deployment, error) {
	var out Deployment
	return out, c.sendJSON(ctx, http.MethodPost, projectPath(projectID, "deployments"), in, &out)
}

func (c *Client) UpdateDevice(ctx context.Context, projectID, deviceID string, in DeviceUpdate) (Device, error) {
	var out Device
	return out, c.sendJSON(ctx, http.MethodPatch, projectPath(projectID, "devices", deviceID), in, &out)
}

func (c *Client) ListCheckEvents(ctx context.Context, projectID, deviceID string, limit int) ([]CheckEvent, error) {
	var out []CheckEvent
	path := projectPath(projectID, "devices", deviceID, "check-events") + "?limit=" + strconv.Itoa(limit)
	return out, c.get(ctx, path, &out)
}

func (c *Client) CreateChannel(ctx context.Context, projectID string, in ChannelRequest) (Channel, error) {
	var out Channel
	return out, c.sendJSON(ctx, http.MethodPost, projectPath(projectID, "channels"), in, &out)
}

func (c *Client) CreateGroup(ctx context.Context, projectID string, in GroupRequest) (Group, error) {
	var out Group
	return out, c.sendJSON(ctx, http.MethodPost, projectPath(projectID, "groups"), in, &out)
}

func (c *Client) ListGroupMembers(ctx context.Context, projectID, groupID string) ([]Device, error) {
	var out []Device
	return out, c.get(ctx, projectPath(projectID, "groups", groupID, "members"), &out)
}

func (c *Client) AddGroupMembers(ctx context.Context, projectID, groupID string, deviceIDs []string) error {
	return c.sendJSON(ctx, http.MethodPost, projectPath(projectID, "groups", groupID, "members"), map[string][]string{"device_ids": deviceIDs}, nil)
}

func (c *Client) RemoveGroupMember(ctx context.Context, projectID, groupID, deviceID string) error {
	return c.sendJSON(ctx, http.MethodDelete, projectPath(projectID, "groups", groupID, "members", deviceID), nil, nil)
}

func (c *Client) RevokeSigningKey(ctx context.Context, projectID, keyID string) error {
	return c.sendJSON(ctx, http.MethodDelete, projectPath(projectID, "signing-keys", keyID), nil, nil)
}

func (c *Client) ListAudit(ctx context.Context, projectID, eventType string, limit int) ([]AuditEvent, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if eventType != "" {
		q.Set("event_type", eventType)
	}
	var out []AuditEvent
	return out, c.get(ctx, projectPath(projectID, "audit")+"?"+q.Encode(), &out)
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

func (c *Client) postJSON(ctx context.Context, path string, in, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, in, out)
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
