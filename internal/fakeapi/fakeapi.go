// Package fakeapi is an in-memory httptest stand-in for the SimpleOTA
// developer API, for unit tests of the client and commands.
package fakeapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
)

// Fixed tokens and IDs.
const (
	PersonalToken = "soto_pat_personalpersonalpersonal"
	ProjectToken  = "soto_proj_projectprojectproject1"
	// StaffToken is a staff account's personal token: listing projects
	// needs an owner filter, as on the real server.
	StaffToken = "soto_pat_staffstaffstaffstaffstaff"
	// StaffAccountID is the staff token's account.
	StaffAccountID = 7
	ProjectID      = "11111111-1111-4111-8111-111111111111"
	OtherID        = "22222222-2222-4222-8222-222222222222"
	// StableChannelID is the ID of the project's only channel, stable.
	StableChannelID = "33333333-3333-4333-8333-333333333333"
)

// Manifest is the upload manifest as the server parses it.
type Manifest struct {
	Framework       string           `json:"framework"`
	VersionLabel    string           `json:"version_label"`
	ChipFamily      string           `json:"chip_family"`
	SecurityMode    string           `json:"security_mode"`
	SigningMetadata *SigningMetadata `json:"signing_metadata"`
	SHA256          *string          `json:"sha256"`
}

// SigningMetadata is the manifest's signature block.
type SigningMetadata struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

// Epoch is the creation time of the first object; later ones add a minute each.
var Epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// Server holds the fake's state. Lock Mu before touching fields from a test
// while requests may be in flight.
type Server struct {
	*httptest.Server
	Mu sync.Mutex

	Projects    []api.Project
	SigningKeys []api.SigningKey
	Artifacts   []api.Artifact
	Devices     []api.Device
	Channels    []api.Channel
	Groups      []api.Group
	Deployments []api.Deployment
	States      map[string][]api.DeviceState
	// Requests counts calls by "METHOD path".
	Requests map[string]int
	// LastManifest is the manifest of the last release.
	LastManifest Manifest
	// CorruptUploads flips a byte of each uploaded binary, as if it changed
	// in transit.
	CorruptUploads bool
	// CheckEvents holds each device's OTA checks by device UUID.
	CheckEvents map[string][]api.CheckEvent
	// Audit is the project's audit log, newest first.
	Audit []api.AuditEvent
	// LastDeploymentRequest is the body of the last deployment created.
	LastDeploymentRequest api.DeploymentRequest
	// StaffProjects are the staff account's own projects, listed for the
	// staff token with owner_id set to StaffAccountID. Staff can still read
	// every project by ID.
	StaffProjects []api.Project
	// NoMe makes /me/ answer 404, as on a server without it.
	NoMe bool
	// FailNext makes the next requests to a path fail with these statuses,
	// one per request.
	FailNext map[string][]int

	clock int
}

// New starts a fake with one project owned by the personal token.
func New(t *testing.T) *Server {
	s := &Server{
		Projects: []api.Project{
			{ID: ProjectID, Name: "Fleet", Slug: "fleet", DefaultFramework: "esp_idf", CreatedAt: Epoch},
		},
		Channels:      []api.Channel{{ID: StableChannelID, Name: "stable", IsDefault: true}},
		Groups:        []api.Group{},
		CheckEvents:   map[string][]api.CheckEvent{},
		Audit:         []api.AuditEvent{},
		StaffProjects: []api.Project{},
		States:        map[string][]api.DeviceState{},
		Requests:      map[string]int{},
		FailNext:      map[string][]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) next() time.Time {
	s.clock++
	return Epoch.Add(time.Duration(s.clock) * time.Minute)
}

// AddDeployment inserts a deployment and returns it.
func (s *Server) AddDeployment(id, state string, build, percent int) api.Deployment {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	d := api.Deployment{
		ID: id, ProjectID: ProjectID, ArtifactID: "a-" + id, ArtifactBuildNumber: build,
		RolloutStrategy: "immediate", RolloutPercentage: percent, State: state, CreatedAt: s.next(),
	}
	s.Deployments = append(s.Deployments, d)
	return d
}

// SetStates replaces a deployment's device states.
func (s *Server) SetStates(deploymentID string, states ...api.DeviceState) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.States[deploymentID] = states
}

// SetDeploymentState changes a deployment's state.
func (s *Server) SetDeploymentState(deploymentID, state string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for i := range s.Deployments {
		if s.Deployments[i].ID == deploymentID {
			s.Deployments[i].State = state
		}
	}
}

// Count returns how many times "METHOD path" was requested.
func (s *Server) Count(key string) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.Requests[key]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func detail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Requests[r.Method+" "+r.URL.Path]++
	if codes := s.FailNext[r.URL.Path]; len(codes) > 0 {
		s.FailNext[r.URL.Path] = codes[1:]
		detail(w, codes[0], "injected failure")
		return
	}

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token != PersonalToken && token != ProjectToken && token != StaffToken {
		detail(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if path == "me/" && !s.NoMe {
		s.me(w, token)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/") || len(parts) == 0 || parts[0] != "projects" {
		detail(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(parts) == 1 {
		q := r.URL.Query()
		if token == StaffToken {
			switch q.Get("owner_id") {
			case "":
				detail(w, http.StatusBadRequest, "staff project listing requires email or owner_id")
			case strconv.Itoa(StaffAccountID):
				writeJSON(w, http.StatusOK, s.StaffProjects)
			default:
				writeJSON(w, http.StatusOK, s.Projects)
			}
			return
		}
		writeJSON(w, http.StatusOK, s.Projects)
		return
	}
	if parts[1] != ProjectID {
		detail(w, http.StatusNotFound, "Not Found")
		return
	}
	route := strings.Join(parts[2:], "/")
	switch {
	case route == "" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.Projects[0])
	case route == "signing-keys" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.SigningKeys)
	case route == "signing-keys" && r.Method == http.MethodPost:
		s.registerKey(w, r)
	case route == "artifacts" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.Artifacts)
	case route == "artifacts" && r.Method == http.MethodPost:
		s.uploadArtifact(w, r)
	case len(parts) == 4 && parts[2] == "artifacts" && r.Method == http.MethodPatch:
		s.updateArtifact(w, r, parts[3])
	case route == "deployments" && r.Method == http.MethodPost:
		s.createDeployment(w, r)
	case len(parts) == 4 && parts[2] == "devices" && r.Method == http.MethodPatch:
		s.updateDevice(w, r, parts[3])
	case len(parts) == 5 && parts[2] == "devices" && parts[4] == "check-events" && r.Method == http.MethodGet:
		events := s.CheckEvents[parts[3]]
		if events == nil {
			events = []api.CheckEvent{}
		}
		writeJSON(w, http.StatusOK, events)
	case route == "channels" && r.Method == http.MethodPost:
		s.createChannel(w, r)
	case route == "groups" && r.Method == http.MethodPost:
		s.createGroup(w, r)
	case len(parts) == 5 && parts[2] == "groups" && parts[4] == "members":
		s.groupMembers(w, r, parts[3], "")
	case len(parts) == 6 && parts[2] == "groups" && parts[4] == "members":
		s.groupMembers(w, r, parts[3], parts[5])
	case len(parts) == 4 && parts[2] == "signing-keys" && r.Method == http.MethodDelete:
		s.revokeKey(w, parts[3])
	case route == "audit" && r.Method == http.MethodGet:
		events := s.Audit
		if t := r.URL.Query().Get("event_type"); t != "" {
			events = slices.DeleteFunc(slices.Clone(events), func(e api.AuditEvent) bool { return e.EventType != t })
		}
		writeJSON(w, http.StatusOK, events)
	case route == "devices" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.devices(r.URL.Query()))
	case len(parts) == 4 && parts[2] == "devices" && r.Method == http.MethodGet:
		for _, d := range s.Devices {
			if d.ID == parts[3] {
				writeJSON(w, http.StatusOK, d)
				return
			}
		}
		detail(w, http.StatusNotFound, "Not Found")
	case route == "channels" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.Channels)
	case route == "groups" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.Groups)
	case route == "release" && r.Method == http.MethodPost:
		s.release(w, r)
	case route == "deployments" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, s.Deployments)
	case len(parts) == 4 && parts[2] == "deployments" && r.Method == http.MethodGet:
		if d := s.deployment(parts[3]); d != nil {
			writeJSON(w, http.StatusOK, d)
			return
		}
		detail(w, http.StatusNotFound, "Not Found")
	case len(parts) == 5 && parts[2] == "deployments" && parts[4] == "states" && r.Method == http.MethodGet:
		states := s.States[parts[3]]
		if states == nil {
			states = []api.DeviceState{}
		}
		writeJSON(w, http.StatusOK, states)
	case len(parts) == 5 && parts[2] == "deployments" && parts[4] == "transition" && r.Method == http.MethodPost:
		s.transition(w, r, parts[3])
	default:
		detail(w, http.StatusNotFound, "Not Found")
	}
}

// devices applies the channel_id, group_id and active filters.
func (s *Server) devices(q url.Values) []api.Device {
	out := []api.Device{}
	for _, d := range s.Devices {
		if c := q.Get("channel_id"); c != "" && (d.ChannelID == nil || *d.ChannelID != c) {
			continue
		}
		if g := q.Get("group_id"); g != "" && !slices.Contains(d.GroupIDs, g) {
			continue
		}
		if a := q.Get("active"); a != "" && strconv.FormatBool(d.Active) != a {
			continue
		}
		out = append(out, d)
	}
	return out
}

func (s *Server) me(w http.ResponseWriter, token string) {
	switch token {
	case ProjectToken:
		writeJSON(w, http.StatusOK, map[string]any{"token_type": "project", "project": s.Projects[0], "scopes": []string{"api"}})
	case StaffToken:
		writeJSON(w, http.StatusOK, map[string]any{"token_type": "personal", "account": api.Account{ID: StaffAccountID, Email: "staff@example.invalid", Username: "staff", IsStaff: true}})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"token_type": "personal", "account": api.Account{ID: 1, Email: "me@example.invalid", Username: "me"}})
	}
}

func (s *Server) deployment(id string) *api.Deployment {
	for i := range s.Deployments {
		if s.Deployments[i].ID == id {
			return &s.Deployments[i]
		}
	}
	return nil
}

func (s *Server) registerKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		KeyID        string  `json:"key_id"`
		PublicKeyPEM *string `json:"public_key_pem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		detail(w, http.StatusBadRequest, "bad json")
		return
	}
	if in.PublicKeyPEM == nil || *in.PublicKeyPEM == "" {
		detail(w, http.StatusBadRequest, "fake refuses server-side key generation")
		return
	}
	for _, k := range s.SigningKeys {
		if k.KeyID == in.KeyID {
			detail(w, http.StatusBadRequest, fmt.Sprintf("a signing key named '%s' already exists", in.KeyID))
			return
		}
	}
	k := api.SigningKey{ID: fmt.Sprintf("key-%d", len(s.SigningKeys)+1), ProjectID: ProjectID, KeyID: in.KeyID, Algorithm: "ed25519", PublicKeyPEM: *in.PublicKeyPEM, CreatedAt: s.next()}
	s.SigningKeys = append(s.SigningKeys, k)
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	a, ok := s.receiveUpload(w, r, func() bool {
		if channel := r.FormValue("channel_name"); channel != "stable" {
			detail(w, http.StatusBadRequest, fmt.Sprintf("channel '%s' not found on this project", channel))
			return false
		}
		return true
	})
	if !ok {
		return
	}
	pct, _ := strconv.Atoi(r.FormValue("rollout_percentage"))
	d := api.Deployment{
		ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", a.BuildNumber), ProjectID: ProjectID, ArtifactID: a.ID,
		ArtifactBuildNumber: a.BuildNumber, RolloutStrategy: "immediate", RolloutPercentage: pct, State: api.StateActive, CreatedAt: s.next(),
		TargetChannelIDs: []string{StableChannelID},
	}
	s.Deployments = append([]api.Deployment{d}, s.Deployments...)
	writeJSON(w, http.StatusCreated, api.ReleaseResult{ArtifactID: a.ID, BuildNumber: a.BuildNumber, DeploymentID: d.ID, State: d.State, Channel: "stable"})
}

func (s *Server) uploadArtifact(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.receiveUpload(w, r, func() bool { return true }); ok {
		writeJSON(w, http.StatusCreated, a)
	}
}

// receiveUpload parses and checks a multipart upload like the server does,
// runs check before allocating a build number, and stores the build. On
// failure it has written the error response.
func (s *Server) receiveUpload(w http.ResponseWriter, r *http.Request, check func() bool) (api.Artifact, bool) {
	if r.ContentLength <= 0 {
		detail(w, http.StatusBadRequest, "missing Content-Length")
		return api.Artifact{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		detail(w, http.StatusBadRequest, err.Error())
		return api.Artifact{}, false
	}
	// Like the server, reject unknown manifest keys.
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("manifest")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		detail(w, http.StatusBadRequest, "manifest invalid: "+err.Error())
		return api.Artifact{}, false
	}
	f, _, err := r.FormFile("binary")
	if err != nil {
		detail(w, http.StatusBadRequest, "binary missing")
		return api.Artifact{}, false
	}
	binary, _ := io.ReadAll(f)
	if s.CorruptUploads {
		binary[len(binary)-1] ^= 0xff
	}
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	if m.SHA256 != nil && strings.ToLower(*m.SHA256) != checksum {
		detail(w, http.StatusBadRequest, fmt.Sprintf("sha256 mismatch: expected %s, received %s", strings.ToLower(*m.SHA256), checksum))
		return api.Artifact{}, false
	}
	if m.SecurityMode == "signed" {
		if err := s.verify(m, binary); err != nil {
			detail(w, http.StatusBadRequest, err.Error())
			return api.Artifact{}, false
		}
	}
	if !check() {
		return api.Artifact{}, false
	}
	s.LastManifest = m
	build := len(s.Artifacts) + 1
	a := api.Artifact{
		ID: fmt.Sprintf("aaaaaaaa-0000-4000-8000-%012d", build), BuildNumber: build, VersionLabel: m.VersionLabel, ChecksumSHA256: checksum,
		FileSize: int64(len(binary)), SecurityMode: m.SecurityMode, ChipFamily: m.ChipFamily, Framework: m.Framework, CreatedAt: s.next(),
	}
	s.Artifacts = append(s.Artifacts, a)
	return a, true
}

func (s *Server) updateArtifact(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Archived bool `json:"archived"`
	}
	if !decode(w, r, &in) {
		return
	}
	for i := range s.Artifacts {
		if s.Artifacts[i].ID != id {
			continue
		}
		if in.Archived {
			for _, d := range s.Deployments {
				if d.ArtifactID == id && (d.State == api.StateActive || d.State == api.StatePaused || d.State == api.StateDraft) {
					detail(w, http.StatusBadRequest, "1 deployment still reference this artifact; cancel or complete them first.")
					return
				}
			}
		}
		s.Artifacts[i].Archived = in.Archived
		writeJSON(w, http.StatusOK, s.Artifacts[i])
		return
	}
	detail(w, http.StatusNotFound, "Not Found")
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var in api.DeploymentRequest
	if !decode(w, r, &in) {
		return
	}
	var art *api.Artifact
	for i := range s.Artifacts {
		if s.Artifacts[i].ID == in.ArtifactID {
			art = &s.Artifacts[i]
		}
	}
	if art == nil {
		detail(w, http.StatusNotFound, "artifact not found in this project")
		return
	}
	s.LastDeploymentRequest = in
	d := api.Deployment{
		ID: fmt.Sprintf("cccccccc-0000-4000-8000-%012d", len(s.Deployments)+1), ProjectID: ProjectID, ArtifactID: art.ID,
		ArtifactBuildNumber: art.BuildNumber, RolloutStrategy: in.RolloutStrategy, RolloutPercentage: in.RolloutPercentage,
		TargetChannelIDs: in.TargetChannelIDs, State: api.StateDraft, CreatedAt: s.next(),
	}
	s.Deployments = append([]api.Deployment{d}, s.Deployments...)
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request, id string) {
	var in api.DeviceUpdate
	if !decode(w, r, &in) {
		return
	}
	for i := range s.Devices {
		d := &s.Devices[i]
		if d.ID != id {
			continue
		}
		if in.Name != nil {
			d.Name = *in.Name
		}
		if in.Active != nil {
			d.Active = *in.Active
		}
		if in.ChannelID != nil {
			d.ChannelID = in.ChannelID
		}
		writeJSON(w, http.StatusOK, d)
		return
	}
	detail(w, http.StatusNotFound, "Not Found")
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var in api.ChannelRequest
	if !decode(w, r, &in) {
		return
	}
	for _, c := range s.Channels {
		if c.Name == in.Name {
			detail(w, http.StatusBadRequest, "channel name already exists in this project")
			return
		}
	}
	c := api.Channel{ID: fmt.Sprintf("eeeeeeee-0000-4000-8000-%012d", len(s.Channels)+1), Name: in.Name, Description: in.Description, IsDefault: in.IsDefault}
	if c.IsDefault {
		for i := range s.Channels {
			s.Channels[i].IsDefault = false
		}
	}
	s.Channels = append(s.Channels, c)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var in api.GroupRequest
	if !decode(w, r, &in) {
		return
	}
	for _, g := range s.Groups {
		if g.Name == in.Name {
			detail(w, http.StatusBadRequest, "group name already exists in this project")
			return
		}
	}
	g := api.Group{ID: fmt.Sprintf("ffffffff-0000-4000-8000-%012d", len(s.Groups)+1), Name: in.Name, Description: in.Description}
	s.Groups = append(s.Groups, g)
	writeJSON(w, http.StatusCreated, g)
}

// groupMembers serves groups/{id}/members/ and groups/{id}/members/{device}/.
func (s *Server) groupMembers(w http.ResponseWriter, r *http.Request, groupID, deviceID string) {
	if !slices.ContainsFunc(s.Groups, func(g api.Group) bool { return g.ID == groupID }) {
		detail(w, http.StatusNotFound, "Not Found")
		return
	}
	switch {
	case r.Method == http.MethodGet && deviceID == "":
		writeJSON(w, http.StatusOK, s.devices(url.Values{"group_id": {groupID}}))
	case r.Method == http.MethodPost && deviceID == "":
		var in struct {
			DeviceIDs []string `json:"device_ids"`
		}
		if !decode(w, r, &in) {
			return
		}
		for i := range s.Devices {
			if slices.Contains(in.DeviceIDs, s.Devices[i].ID) && !slices.Contains(s.Devices[i].GroupIDs, groupID) {
				s.Devices[i].GroupIDs = append(s.Devices[i].GroupIDs, groupID)
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": groupID})
	case r.Method == http.MethodDelete && deviceID != "":
		for i := range s.Devices {
			if s.Devices[i].ID == deviceID {
				s.Devices[i].GroupIDs = slices.DeleteFunc(s.Devices[i].GroupIDs, func(g string) bool { return g == groupID })
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		detail(w, http.StatusNotFound, "Not Found")
	default:
		detail(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (s *Server) revokeKey(w http.ResponseWriter, keyID string) {
	for i := range s.SigningKeys {
		if s.SigningKeys[i].KeyID == keyID && s.SigningKeys[i].RevokedAt == nil {
			now := s.next()
			s.SigningKeys[i].RevokedAt = &now
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	detail(w, http.StatusNotFound, "signing key not found or already revoked")
}

// decode reads a JSON request body, writing a 400 when it is not JSON.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		detail(w, http.StatusBadRequest, "bad json")
		return false
	}
	return true
}

func (s *Server) verify(m Manifest, binary []byte) error {
	meta := m.SigningMetadata
	if meta == nil {
		return fmt.Errorf("signed uploads require signing_metadata")
	}
	for _, k := range s.SigningKeys {
		if k.KeyID != meta.KeyID {
			continue
		}
		pub, err := parsePublicKey(k.PublicKeyPEM)
		if err != nil {
			return err
		}
		sig, err := base64.StdEncoding.DecodeString(meta.Signature)
		if err != nil || !ed25519.Verify(pub, binary, sig) {
			return fmt.Errorf("signature verification failed: the signature does not match this binary and key")
		}
		return nil
	}
	return fmt.Errorf("no signing key '%s' registered for this project", meta.KeyID)
}

func parsePublicKey(text string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, fmt.Errorf("public key is not valid PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("public key is not valid PEM")
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key must be an Ed25519 key")
	}
	return pub, nil
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, id string) {
	d := s.deployment(id)
	if d == nil {
		detail(w, http.StatusNotFound, "Not Found")
		return
	}
	var t api.Transition
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		detail(w, http.StatusBadRequest, "bad json")
		return
	}
	switch t.Action {
	case "ramp":
		if t.RolloutPercentage == nil {
			detail(w, http.StatusBadRequest, "ramp requires rollout_percentage")
			return
		}
		if *t.RolloutPercentage < d.RolloutPercentage {
			detail(w, http.StatusBadRequest, "cannot ramp rollout_percentage downward; create a new deployment")
			return
		}
		d.RolloutPercentage = *t.RolloutPercentage
	case "start":
		if len(d.TargetChannelIDs) == 0 && len(s.Devices) >= 2 && !t.ConfirmAll {
			detail(w, http.StatusBadRequest, fmt.Sprintf("starting this deployment requires confirm_all=true because it targets all %d project devices", len(s.Devices)))
			return
		}
		d.State = api.StateActive
	case "pause":
		d.State = api.StatePaused
	case "resume":
		d.State = api.StateActive
	case "complete":
		d.State = api.StateCompleted
	case "cancel":
		d.State = api.StateCancelled
	default:
		detail(w, http.StatusBadRequest, fmt.Sprintf("unknown action '%s'", t.Action))
		return
	}
	writeJSON(w, http.StatusOK, d)
}
