// Package config loads the raw Envoy xDS resources served by the control
// plane. Each xDS API has its own config file: CDS (Cluster), EDS
// (ClusterLoadAssignment), LDS (Listener), RDS (RouteConfiguration), and SDS
// (Secret) - every file a top-level YAML list of that API's resource in its
// native Envoy proto shape. Each is loaded, validated, and hot-reloaded
// independently, so a mistake in one doesn't touch the others' last-good
// config. CDS and LDS are required since there's nothing to serve without
// at least one cluster and one listener; EDS, RDS, and SDS are optional -
// their config files may be absent if nothing references them dynamically.
package config

import (
	"encoding/json"
	"fmt"
	"os"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"
)

// Paths locates each xDS API's config file. CDS and LDS must exist; EDS,
// RDS, and SDS are read if present and skipped (zero resources) if not.
type Paths struct {
	CDS string
	EDS string
	LDS string
	RDS string
	SDS string
}

// Config is the loaded, validated resource set for one xDS snapshot.
type Config struct {
	Clusters  []*cluster.Cluster
	Endpoints []*endpoint.ClusterLoadAssignment
	Listeners []*listener.Listener
	Routes    []*route.RouteConfiguration
	Secrets   []*tls.Secret
}

// Load reads and validates every file in paths and combines them into one
// Config.
func Load(paths Paths) (*Config, error) {
	clusters, err := loadClusters(paths.CDS)
	if err != nil {
		return nil, err
	}

	listeners, err := loadListeners(paths.LDS)
	if err != nil {
		return nil, err
	}

	endpoints, err := loadEndpoints(paths.EDS)
	if err != nil {
		return nil, err
	}

	routes, err := loadRoutes(paths.RDS)
	if err != nil {
		return nil, err
	}

	secrets, err := loadSecrets(paths.SDS)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Clusters:  clusters,
		Endpoints: endpoints,
		Listeners: listeners,
		Routes:    routes,
		Secrets:   secrets,
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

func loadClusters(path string) ([]*cluster.Cluster, error) {
	docs, err := readResourceList(path, true)
	if err != nil {
		return nil, fmt.Errorf("reading cds config %q: %w", path, err)
	}

	clusters := make([]*cluster.Cluster, len(docs))
	for i, doc := range docs {
		c := &cluster.Cluster{}
		if err := protojson.Unmarshal(doc, c); err != nil {
			return nil, fmt.Errorf("parsing cds config %q: resource %d: %w", path, i, err)
		}
		clusters[i] = c
	}
	return clusters, nil
}

func loadListeners(path string) ([]*listener.Listener, error) {
	docs, err := readResourceList(path, true)
	if err != nil {
		return nil, fmt.Errorf("reading lds config %q: %w", path, err)
	}

	listeners := make([]*listener.Listener, len(docs))
	for i, doc := range docs {
		l := &listener.Listener{}
		if err := protojson.Unmarshal(doc, l); err != nil {
			return nil, fmt.Errorf("parsing lds config %q: resource %d: %w", path, i, err)
		}
		listeners[i] = l
	}
	return listeners, nil
}

func loadEndpoints(path string) ([]*endpoint.ClusterLoadAssignment, error) {
	docs, err := readResourceList(path, false)
	if err != nil {
		return nil, fmt.Errorf("reading eds config %q: %w", path, err)
	}

	endpoints := make([]*endpoint.ClusterLoadAssignment, len(docs))
	for i, doc := range docs {
		e := &endpoint.ClusterLoadAssignment{}
		if err := protojson.Unmarshal(doc, e); err != nil {
			return nil, fmt.Errorf("parsing eds config %q: resource %d: %w", path, i, err)
		}
		endpoints[i] = e
	}
	return endpoints, nil
}

func loadRoutes(path string) ([]*route.RouteConfiguration, error) {
	docs, err := readResourceList(path, false)
	if err != nil {
		return nil, fmt.Errorf("reading rds config %q: %w", path, err)
	}

	routes := make([]*route.RouteConfiguration, len(docs))
	for i, doc := range docs {
		r := &route.RouteConfiguration{}
		if err := protojson.Unmarshal(doc, r); err != nil {
			return nil, fmt.Errorf("parsing rds config %q: resource %d: %w", path, i, err)
		}
		routes[i] = r
	}
	return routes, nil
}

func loadSecrets(path string) ([]*tls.Secret, error) {
	docs, err := readResourceList(path, false)
	if err != nil {
		return nil, fmt.Errorf("reading sds config %q: %w", path, err)
	}

	secrets := make([]*tls.Secret, len(docs))
	for i, doc := range docs {
		s := &tls.Secret{}
		if err := protojson.Unmarshal(doc, s); err != nil {
			return nil, fmt.Errorf("parsing sds config %q: resource %d: %w", path, i, err)
		}
		secrets[i] = s
	}
	return secrets, nil
}

// readResourceList reads path as a top-level YAML list and returns each
// element as its own JSON document, ready for protojson.Unmarshal. yaml.v3
// decodes mappings as map[string]interface{}, so re-marshaling through
// encoding/json (rather than a YAML-specific JSON converter) is enough to
// produce valid protojson input, including "@type" keys for typed_config.
//
// If required is false, a missing file yields an empty (not error) result -
// that xDS API simply has nothing to serve.
func readResourceList(path string, required bool) ([]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !required && os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var items []interface{}
	if err := yaml.Unmarshal(data, &items); err != nil {
		return nil, err
	}

	docs := make([]json.RawMessage, len(items))
	for i, item := range items {
		b, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("resource %d: %w", i, err)
		}
		docs[i] = b
	}
	return docs, nil
}

func (c *Config) validate() error {
	if len(c.Clusters) == 0 {
		return fmt.Errorf("no clusters defined")
	}
	if len(c.Listeners) == 0 {
		return fmt.Errorf("no listeners defined")
	}

	if err := requireUniqueNames("cluster", namesOf(c.Clusters, func(c *cluster.Cluster) string { return c.Name })); err != nil {
		return err
	}
	if err := requireUniqueNames("listener", namesOf(c.Listeners, func(l *listener.Listener) string { return l.Name })); err != nil {
		return err
	}
	if err := requireUniqueNames("endpoint cluster_name", namesOf(c.Endpoints, func(e *endpoint.ClusterLoadAssignment) string { return e.ClusterName })); err != nil {
		return err
	}
	if err := requireUniqueNames("route", namesOf(c.Routes, func(r *route.RouteConfiguration) string { return r.Name })); err != nil {
		return err
	}
	if err := requireUniqueNames("secret", namesOf(c.Secrets, func(s *tls.Secret) string { return s.Name })); err != nil {
		return err
	}

	return nil
}

func namesOf[T any](items []T, name func(T) string) []string {
	names := make([]string, len(items))
	for i, item := range items {
		names[i] = name(item)
	}
	return names
}

func requireUniqueNames(kind string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("%s missing name", kind)
		}
		if seen[name] {
			return fmt.Errorf("duplicate %s name %q", kind, name)
		}
		seen[name] = true
	}
	return nil
}
