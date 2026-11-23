package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInfXDSTestCases(t *testing.T) {
	t.Parallel()

	clusters, err := ParseClusters(mustRead(t, "testdata/cds.yaml"))
	if err != nil {
		t.Fatalf("ParseClusters: %v", err)
	}
	if got := names(clusters, func(i int) string { return clusters[i].GetName() }); got != "echo,dedicated" {
		t.Errorf("cluster order = %s, want echo,dedicated", got)
	}

	for _, f := range []string{"testdata/lds1.yaml", "testdata/lds2.yaml"} {
		if _, err := ParseListeners(mustRead(t, f)); err != nil {
			t.Errorf("ParseListeners(%s): %v", f, err)
		}
	}
}

func TestParseAcceptedShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"yaml list", "- name: a\n- name: b\n", "a,b"},
		{"json array", `[{"name": "a"}, {"name": "b"}]`, "a,b"},
		{"single resource", "name: a\n", "a"},
		{"empty", "", ""},
		{"braces inside strings", `[{"name": "a}{b"}]`, "a}{b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseClusters([]byte(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			if n := names(got, func(i int) string { return got[i].GetName() }); n != tt.want {
				t.Errorf("names = %q, want %q", n, tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"duplicate name": "- name: a\n- name: a\n",
		"missing name":   "- connect_timeout: 1s\n",
		"unknown field":  "- name: a\n  no_such_field: 1\n",
		"pgv violation":  "- name: a\n  connect_timeout: -1s\n",
		"scalar":         "42\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseClusters([]byte(input)); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("XDS_TEST_HOST", "10.0.0.1")
	t.Setenv("XDS_TEST_PORT", "8080")

	got, err := ExpandEnv([]byte(`addr: {{ env "XDS_TEST_HOST" }}:{{env "XDS_TEST_PORT"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := "addr: 10.0.0.1:8080"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}

	if _, err := ExpandEnv([]byte(`{{ env "XDS_TEST_DEFINITELY_UNSET" }}`)); err == nil {
		t.Error("expected error for unset variable")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	cds := filepath.Join(dir, "cds.yaml")
	lds := filepath.Join(dir, "lds.yaml")
	mustWrite(t, cds, string(mustRead(t, "testdata/cds.yaml")))
	mustWrite(t, lds, string(mustRead(t, "testdata/lds1.yaml")))

	t.Run("optional files missing", func(t *testing.T) {
		cfg, err := Load(context.Background(), Paths{CDS: cds, LDS: lds, EDS: filepath.Join(dir, "nope.yaml")})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Clusters) != 2 || len(cfg.Listeners) != 1 || len(cfg.Endpoints) != 0 {
			t.Errorf("unexpected config: %d clusters, %d listeners, %d endpoints",
				len(cfg.Clusters), len(cfg.Listeners), len(cfg.Endpoints))
		}
	})

	t.Run("required file missing", func(t *testing.T) {
		if _, err := Load(context.Background(), Paths{CDS: filepath.Join(dir, "nope.yaml")}); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("required file empty", func(t *testing.T) {
		empty := filepath.Join(dir, "empty.yaml")
		mustWrite(t, empty, "# nothing\n")
		if _, err := Load(context.Background(), Paths{CDS: empty}); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("skipped sources stay nil", func(t *testing.T) {
		cfg, err := Load(context.Background(), Paths{LDS: lds})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Clusters != nil {
			t.Error("clusters should be nil when CDS is not configured")
		}
	})

	t.Run("url source", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/cds" {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(mustRead(t, "testdata/cds.yaml"))
		}))
		defer srv.Close()

		cfg, err := Load(context.Background(), Paths{CDS: srv.URL + "/cds"})
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Clusters) != 2 {
			t.Errorf("got %d clusters, want 2", len(cfg.Clusters))
		}

		if _, err := Load(context.Background(), Paths{CDS: srv.URL + "/missing"}); err == nil {
			t.Error("expected error for 404")
		}
	})
}

func names[T any](items []T, name func(int) string) string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = name(i)
	}
	return strings.Join(out, ",")
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
