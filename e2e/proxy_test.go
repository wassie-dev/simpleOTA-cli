//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// lossyProxy serves the dev server's API on a new loopback URL. The first
// request with method and a path ending in suffix reaches the server, which
// handles it, and then the connection is closed without a reply.
func lossyProxy(t *testing.T, method, suffix string) string {
	t.Helper()
	target, err := url.Parse(env["SIMPLEOTA_DEV_API_URL"])
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var dropped atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method && strings.HasSuffix(r.URL.Path, suffix) && dropped.CompareAndSwap(false, true) {
			proxy.ServeHTTP(httptest.NewRecorder(), r)
			if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
