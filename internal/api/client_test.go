package api_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wassie-dev/simpleOTA-cli/internal/api"
	"github.com/wassie-dev/simpleOTA-cli/internal/fakeapi"
)

func noSleep(context.Context, time.Duration) error { return nil }

func newClient(url, token string) *api.Client {
	return api.New(url, token, "simpleota-cli/test", api.WithSleep(noSleep))
}

func TestSendsBearerTokenAndUserAgent(t *testing.T) {
	var auth, ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, ua = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "[]")
	}))
	defer srv.Close()
	if _, err := newClient(srv.URL, fakeapi.PersonalToken).ListProjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer "+fakeapi.PersonalToken || ua != "simpleota-cli/test" {
		t.Fatalf("auth=%q ua=%q", auth, ua)
	}
}

func TestGetRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `[{"id":"p","slug":"fleet"}]`)
	}))
	defer srv.Close()
	ps, err := newClient(srv.URL, fakeapi.PersonalToken).ListProjects(context.Background())
	if err != nil || len(ps) != 1 || calls.Load() != 3 {
		t.Fatalf("projects=%v err=%v calls=%d", ps, err, calls.Load())
	}
}

func TestGetGivesUpAfterBoundedAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := newClient(srv.URL, fakeapi.PersonalToken).ListProjects(context.Background())
	var ae *api.Error
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusBadGateway || calls.Load() != 3 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestGetDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"detail":"Not Found"}`)
	}))
	defer srv.Close()
	_, err := newClient(srv.URL, fakeapi.PersonalToken).GetProject(context.Background(), fakeapi.ProjectID)
	if err == nil || calls.Load() != 1 || !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestPostIsNeverRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newClient(srv.URL, fakeapi.ProjectToken)
	_, err := c.Release(context.Background(), fakeapi.ProjectID, api.ReleaseRequest{
		ManifestJSON: []byte(`{}`), FileName: "app.bin", Binary: []byte{0xe9}, RolloutPercentage: 100, ChannelName: "stable",
	})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("release: err=%v calls=%d", err, calls.Load())
	}
	calls.Store(0)
	_, err = c.TransitionDeployment(context.Background(), fakeapi.ProjectID, "d", api.Transition{Action: "pause"})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("transition: err=%v calls=%d", err, calls.Load())
	}
}

func TestReleaseSendsMultipartWithContentLength(t *testing.T) {
	var gotLen int64
	var manifest, pct, channel, filename string
	var binary []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLen = r.ContentLength
		if len(r.TransferEncoding) > 0 {
			t.Errorf("unexpected transfer encoding %v", r.TransferEncoding)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		manifest, pct, channel = r.FormValue("manifest"), r.FormValue("rollout_percentage"), r.FormValue("channel_name")
		f, h, err := r.FormFile("binary")
		if err != nil {
			t.Error(err)
		} else {
			filename = h.Filename
			binary, _ = io.ReadAll(f)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"artifact_id":"a","build_number":7,"deployment_id":"d","state":"active","channel":"beta"}`)
	}))
	defer srv.Close()
	res, err := newClient(srv.URL, fakeapi.ProjectToken).Release(context.Background(), fakeapi.ProjectID, api.ReleaseRequest{
		ManifestJSON: []byte(`{"framework":"esp_idf"}`), FileName: "app.bin", Binary: []byte{0xe9, 1, 2}, RolloutPercentage: 25, ChannelName: "beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.BuildNumber != 7 || gotLen <= 0 || manifest != `{"framework":"esp_idf"}` || pct != "25" || channel != "beta" || filename != "app.bin" || string(binary) != "\xe9\x01\x02" {
		t.Fatalf("res=%+v len=%d manifest=%q pct=%q channel=%q filename=%q binary=%v", res, gotLen, manifest, pct, channel, filename, binary)
	}
}

func TestErrorDetail(t *testing.T) {
	tests := []struct {
		body, want string
	}{
		{`{"detail":"channel 'x' not found on this project"}`, "server returned 400: channel 'x' not found on this project"},
		{`{"detail":[{"loc":["body"],"msg":"field required"}]}`, `server returned 400: [{"loc":["body"],"msg":"field required"}]`},
		{`<html>boom</html>`, "POST /api/v1/projects/" + fakeapi.ProjectID + "/deployments/d/transition/: server returned 400"},
	}
	for _, tt := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, tt.body)
		}))
		_, err := newClient(srv.URL, fakeapi.ProjectToken).TransitionDeployment(context.Background(), fakeapi.ProjectID, "d", api.Transition{Action: "pause"})
		srv.Close()
		if err == nil || err.Error() != tt.want {
			t.Errorf("err = %v, want %q", err, tt.want)
		}
	}
}

func TestAuthErrors(t *testing.T) {
	fake := fakeapi.New(t)
	_, err := newClient(fake.URL, "soto_pat_wrongwrongwrongwrong").ListProjects(context.Background())
	var ae *api.Error
	if !errors.As(err, &ae) || !ae.IsAuth() {
		t.Fatalf("err = %v, want auth error", err)
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/", http.StatusFound)
	}))
	defer srv.Close()
	_, err := newClient(srv.URL, fakeapi.PersonalToken).ListProjects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterSigningKeyRefusesEmptyPublicKey(t *testing.T) {
	fake := fakeapi.New(t)
	_, err := newClient(fake.URL, fakeapi.PersonalToken).RegisterSigningKey(context.Background(), fakeapi.ProjectID, "k", " ")
	if err == nil {
		t.Fatal("want error")
	}
	if n := fake.Count("POST /api/v1/projects/" + fakeapi.ProjectID + "/signing-keys/"); n != 0 {
		t.Fatalf("request must not be sent, got %d", n)
	}
}

func TestOversizedResponseIsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[")
		chunk := strings.Repeat(`{"id":"x"},`, 1<<16)
		for i := 0; i < 20; i++ {
			_, _ = io.WriteString(w, chunk)
		}
	}))
	defer srv.Close()
	_, err := newClient(srv.URL, fakeapi.PersonalToken).ListProjects(context.Background())
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsUUID(t *testing.T) {
	if !api.IsUUID("5f0c2b1e-1d2c-4b7a-9a3e-0123456789ab") || api.IsUUID("5f0c2b1e") || api.IsUUID("../projects") {
		t.Fatal("IsUUID wrong")
	}
}

func TestCertificateErrorsAreNotRetried(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	_, err := newClient(srv.URL, fakeapi.PersonalToken).ListProjects(context.Background())
	var certErr *tls.CertificateVerificationError
	if !errors.As(err, &certErr) {
		t.Fatalf("err = %v, want a certificate error", err)
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("connections = %d, want 1 (no retry)", n)
	}
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{&api.Error{StatusCode: 503}, true},
		{&api.Error{StatusCode: 429}, true},
		{&api.Error{StatusCode: 404}, false},
		{&api.Error{StatusCode: 401}, false},
		{fmt.Errorf("wrapped: %w", &api.Error{StatusCode: 502}), true},
		{errors.New("plain"), false},
	}
	for _, tt := range tests {
		if got := api.IsTransient(tt.err); got != tt.want {
			t.Errorf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
