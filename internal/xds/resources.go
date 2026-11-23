// Package xds serves Envoy xDS resources (CDS/EDS/LDS/RDS/SDS) to Envoy
// proxies over the Aggregated Discovery Service (ADS), per
// https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/operations/dynamic_configuration.
// Resources are parsed as-is from config (see internal/config), not built up
// from a higher-level schema.
package xds

import (
	"fmt"
	"slices"
	"strings"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	"github.com/kassymkhan1/xds/internal/config"
)

// BuildSnapshot turns the CDS/EDS/LDS/RDS/SDS resources into an xDS
// snapshot and checks that every EDS/RDS reference resolves.
func BuildSnapshot(version string, cfg *config.Config) (*cache.Snapshot, error) {
	snap, err := cache.NewSnapshot(version, map[resource.Type][]types.Resource{
		resource.ClusterType:  asResources(cfg.Clusters),
		resource.EndpointType: asResources(cfg.Endpoints),
		resource.ListenerType: asResources(cfg.Listeners),
		resource.RouteType:    asResources(cfg.Routes),
		resource.SecretType:   asResources(cfg.Secrets),
	})
	if err != nil {
		return nil, err
	}
	if err := checkReferences(snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// checkReferences verifies that every ClusterLoadAssignment referenced by an
// EDS cluster and every RouteConfiguration referenced by an RDS listener is
// present. Unlike cache.Snapshot.Consistent it allows unreferenced extras:
// those are harmless (Envoy only ever requests EDS/RDS resources by name),
// and rejecting them would make it impossible to, say, replace the clusters
// through the management API while EDS is still file-backed.
func checkReferences(snap *cache.Snapshot) error {
	for typeURL, refs := range cache.GetAllResourceReferences(snap.Resources) {
		have := snap.GetResources(typeURL)

		var missing []string
		for name := range refs {
			if _, ok := have[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			slices.Sort(missing)
			return fmt.Errorf("unresolved %s references: %s",
				typeURL[strings.LastIndex(typeURL, ".")+1:], strings.Join(missing, ", "))
		}
	}
	return nil
}

func asResources[T types.Resource](items []T) []types.Resource {
	resources := make([]types.Resource, len(items))
	for i, item := range items {
		resources[i] = item
	}
	return resources
}
