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
	ProjectID     = "11111111-1111-4111-8111-111111111111"
	OtherID       = "22222222-2222-4222-8222-222222222222"
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
	Deployments []api.Deployment
	States      map[string][]api.DeviceState
	// Requests counts calls by "METHOD path".
	Requests map[string]int
	// LastManifest is the manifest of the last release.
	LastManifest Manifest
	// CorruptUploads flips a byte of each uploaded binary, as if it changed
	// in transit.
	CorruptUploads bool
	// FailNext makes the next requests to a path fail with these statuses,
	// one per request.
	FailNext map[string][]int

	clock int
}

// New starts a fake with one project owned by the personal token.
func New(t *testing.T) *Server {
	s := &Server{
		Projects: []api.Project{
			{ID: ProjectID, Name: "Fleet", Slug: "fleet", DefaultFramework: "esp_idf"},
		},
		States:   map[string][]api.DeviceState{},
		Requests: map[string]int{},
		FailNext: map[string][]int{},
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
	if token != PersonalToken && token != ProjectToken {
		detail(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if !strings.HasSuffix(r.URL.Path, "/") || len(parts) == 0 || parts[0] != "projects" {
		detail(w, http.StatusNotFound, "Not Found")
		return
	}
	if len(parts) == 1 {
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
	k := api.SigningKey{ID: fmt.Sprintf("key-%d", len(s.SigningKeys)+1), ProjectID: ProjectID, KeyID: in.KeyID, Algorithm: "ed25519", PublicKeyPEM: *in.PublicKeyPEM}
	s.SigningKeys = append(s.SigningKeys, k)
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength <= 0 {
		detail(w, http.StatusBadRequest, "missing Content-Length")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		detail(w, http.StatusBadRequest, err.Error())
		return
	}
	// Like the server, reject unknown manifest keys.
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(r.FormValue("manifest")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		detail(w, http.StatusBadRequest, "manifest invalid: "+err.Error())
		return
	}
	f, _, err := r.FormFile("binary")
	if err != nil {
		detail(w, http.StatusBadRequest, "binary missing")
		return
	}
	binary, _ := io.ReadAll(f)
	if s.CorruptUploads {
		binary[len(binary)-1] ^= 0xff
	}
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	if m.SHA256 != nil && strings.ToLower(*m.SHA256) != checksum {
		detail(w, http.StatusBadRequest, fmt.Sprintf("sha256 mismatch: expected %s, received %s", strings.ToLower(*m.SHA256), checksum))
		return
	}
	if m.SecurityMode == "signed" {
		if err := s.verify(m, binary); err != nil {
			detail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	pct, _ := strconv.Atoi(r.FormValue("rollout_percentage"))
	channel := r.FormValue("channel_name")
	if channel != "stable" {
		detail(w, http.StatusBadRequest, fmt.Sprintf("channel '%s' not found on this project", channel))
		return
	}
	s.LastManifest = m
	build := len(s.Artifacts) + 1
	a := api.Artifact{ID: fmt.Sprintf("art-%d", build), BuildNumber: build, VersionLabel: m.VersionLabel, ChecksumSHA256: checksum, FileSize: int64(len(binary)), SecurityMode: m.SecurityMode}
	s.Artifacts = append(s.Artifacts, a)
	d := api.Deployment{
		ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", build), ProjectID: ProjectID, ArtifactID: a.ID,
		ArtifactBuildNumber: build, RolloutStrategy: "immediate", RolloutPercentage: pct, State: api.StateActive, CreatedAt: s.next(),
	}
	s.Deployments = append([]api.Deployment{d}, s.Deployments...)
	writeJSON(w, http.StatusCreated, api.ReleaseResult{ArtifactID: a.ID, BuildNumber: build, DeploymentID: d.ID, State: d.State, Channel: channel})
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
