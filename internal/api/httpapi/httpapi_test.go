package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"

	"github.com/kassymkhan1/xds/internal/xds"
)

const clustersYAML = `
- name: echo
  connect_timeout: 0.25s
  type: STRICT_DNS
  load_assignment:
    cluster_name: echo
    endpoints:
      - lb_endpoints:
          - endpoint:
              address:
                socket_address: {address: 127.0.0.1, port_value: 8090}
`

func newServer(bypass bool) *httptest.Server {
	state := xds.NewState(cache.NewSnapshotCache(true, cache.IDHash{}, nil), "node", bypass)
	return httptest.NewServer(New(state).Handler())
}

func TestSetAndGetClusters(t *testing.T) {
	srv := newServer(false)
	defer srv.Close()

	for _, path := range []string{"/clusters", "/set_clusters"} {
		resp := post(t, srv.URL+path, clustersYAML)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s: status %d", path, resp.StatusCode)
		}
	}

	for _, path := range []string{"/clusters", "/get_clusters"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var got []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if len(got) != 1 || got[0]["name"] != "echo" || got[0]["connect_timeout"] != "0.250s" {
			t.Errorf("GET %s = %v", path, got)
		}
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	srv := newServer(false)
	defer srv.Close()

	tests := map[string]struct {
		path, body string
		want       int
	}{
		"bad yaml":      {"/clusters", "- name: [", http.StatusBadRequest},
		"unknown field": {"/clusters", "- name: a\n  bogus: 1", http.StatusBadRequest},
		"inconsistent":  {"/clusters", "- name: a\n  type: EDS\n  eds_cluster_config: {eds_config: {ads: {}}}", http.StatusUnprocessableEntity},
		"bad listener":  {"/listeners", "- address: {}", http.StatusBadRequest},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if resp := post(t, srv.URL+tt.path, tt.body); resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestBypassAndHealth(t *testing.T) {
	srv := newServer(true)
	defer srv.Close()

	if resp := post(t, srv.URL+"/clusters", clustersYAML); resp.StatusCode != http.StatusAccepted {
		t.Errorf("bypass status = %d, want 202", resp.StatusCode)
	}

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d", resp.StatusCode)
	}
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/yaml", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}
