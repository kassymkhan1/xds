// Package config loads the raw Envoy xDS resources served by the control
// plane. Each xDS API has its own source: CDS (Cluster), EDS
// (ClusterLoadAssignment), LDS (Listener), RDS (RouteConfiguration), and SDS
// (Secret). A source is a local file or an http(s) URL holding a YAML (or
// JSON) list of that API's resources in their native Envoy proto shape; a
// single resource not wrapped in a list is accepted too. `{{ env "NAME" }}`
// placeholders are expanded before parsing (see ExpandEnv).
//
// Every resource is checked against Envoy's proto validation rules and for
// unique names, so a bad config is rejected here instead of being NACKed by
// every connected Envoy.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// Paths locates each xDS API's source (file path or http(s) URL). An empty
// path means that API isn't loaded from config at all - e.g. clusters and
// listeners that are only pushed through the management API.
//
// A configured CDS or LDS source must exist and define at least one
// resource. EDS, RDS, and SDS files are optional: a missing file yields zero
// resources.
type Paths struct {
	CDS string
	EDS string
	LDS string
	RDS string
	SDS string
}

// All returns every configured (non-empty) source.
func (p Paths) All() []string {
	var all []string
	for _, s := range []string{p.CDS, p.EDS, p.LDS, p.RDS, p.SDS} {
		if s != "" {
			all = append(all, s)
		}
	}
	return all
}

// Config is a loaded, validated resource set for one xDS snapshot.
type Config struct {
	Clusters  []*cluster.Cluster
	Endpoints []*endpoint.ClusterLoadAssignment
	Listeners []*listener.Listener
	Routes    []*route.RouteConfiguration
	Secrets   []*tls.Secret
}

// Load reads and validates every configured source in paths. Resource types
// whose path is empty are left nil.
func Load(ctx context.Context, paths Paths) (*Config, error) {
	var (
		cfg  Config
		errs []error
	)

	loadInto(ctx, &errs, &cfg.Clusters, "cds", paths.CDS, true, ParseClusters)
	loadInto(ctx, &errs, &cfg.Endpoints, "eds", paths.EDS, false, ParseEndpoints)
	loadInto(ctx, &errs, &cfg.Listeners, "lds", paths.LDS, true, ParseListeners)
	loadInto(ctx, &errs, &cfg.Routes, "rds", paths.RDS, false, ParseRoutes)
	loadInto(ctx, &errs, &cfg.Secrets, "sds", paths.SDS, false, ParseSecrets)

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// loadInto loads src into *dst unless src is empty (not configured),
// appending any error to *errs so every broken source is reported at once.
func loadInto[T any](ctx context.Context, errs *[]error, dst *[]T, api, src string, required bool, parse func([]byte) ([]T, error)) {
	if src == "" {
		return
	}
	items, err := load(ctx, api, src, required, parse)
	if err != nil {
		*errs = append(*errs, err)
		return
	}
	*dst = items
}

func load[T any](ctx context.Context, api, src string, required bool, parse func([]byte) ([]T, error)) ([]T, error) {
	data, err := readSource(ctx, src)
	if err != nil {
		if !required && isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s config %q: %w", api, src, err)
	}

	items, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s config %q: %w", api, src, err)
	}
	if required && len(items) == 0 {
		return nil, fmt.Errorf("%s config %q: no resources defined", api, src)
	}
	return items, nil
}

// ParseClusters parses a YAML/JSON list of Envoy Clusters.
func ParseClusters(data []byte) ([]*cluster.Cluster, error) {
	return parse(data, func() *cluster.Cluster { return &cluster.Cluster{} }, ValidateClusters)
}

// ParseEndpoints parses a YAML/JSON list of Envoy ClusterLoadAssignments.
func ParseEndpoints(data []byte) ([]*endpoint.ClusterLoadAssignment, error) {
	return parse(data, func() *endpoint.ClusterLoadAssignment { return &endpoint.ClusterLoadAssignment{} }, ValidateEndpoints)
}

// ParseListeners parses a YAML/JSON list of Envoy Listeners.
func ParseListeners(data []byte) ([]*listener.Listener, error) {
	return parse(data, func() *listener.Listener { return &listener.Listener{} }, ValidateListeners)
}

// ParseRoutes parses a YAML/JSON list of Envoy RouteConfigurations.
func ParseRoutes(data []byte) ([]*route.RouteConfiguration, error) {
	return parse(data, func() *route.RouteConfiguration { return &route.RouteConfiguration{} }, ValidateRoutes)
}

// ParseSecrets parses a YAML/JSON list of Envoy Secrets.
func ParseSecrets(data []byte) ([]*tls.Secret, error) {
	return parse(data, func() *tls.Secret { return &tls.Secret{} }, ValidateSecrets)
}

func parse[T proto.Message](data []byte, newT func() T, validate func([]T) error) ([]T, error) {
	docs, err := decodeList(data)
	if err != nil {
		return nil, err
	}

	items := make([]T, len(docs))
	for i, doc := range docs {
		item := newT()
		if err := protojson.Unmarshal(doc, item); err != nil {
			return nil, fmt.Errorf("resource %d: %w", i, err)
		}
		items[i] = item
	}

	if err := validate(items); err != nil {
		return nil, err
	}
	return items, nil
}

// decodeList expands env placeholders in data, parses it as YAML (a superset
// of JSON) and returns each resource as its own JSON document, ready for
// protojson.Unmarshal. The top level may be a list of resources, a single
// resource, or empty. yaml.v3 decodes mappings as map[string]any, so
// re-marshaling through encoding/json produces valid protojson input,
// including "@type" keys for typed_config.
func decodeList(data []byte) ([]json.RawMessage, error) {
	data, err := ExpandEnv(data)
	if err != nil {
		return nil, err
	}

	var top any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, err
	}

	var items []any
	switch v := top.(type) {
	case nil:
		return nil, nil
	case []any:
		items = v
	case map[string]any:
		items = []any{v}
	default:
		return nil, fmt.Errorf("expected a list of resources, got %T", top)
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
