// Package api is a client for the SimpleOTA developer REST API (/api/v1/).
package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	return out, c.sendJSON(ctx, http.MethodPost, projectPath(projectID, "signing-keys"), body, &out)
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
	return out, c.sendJSON(ctx, http.MethodPost, projectPath(projectID, "deployments", deploymentID, "transition"), t, &out)
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
