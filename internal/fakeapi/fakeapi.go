// Package fakeapi is an in-memory httptest stand-in for the SimpleOTA
// developer API, for unit tests of the client and commands.
package fakeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
