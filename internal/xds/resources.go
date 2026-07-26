// Package xds serves Envoy xDS resources (CDS/EDS/LDS/RDS/SDS) to Envoy
// proxies over the Aggregated Discovery Service (ADS), per
// https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/operations/dynamic_configuration.
// Resources are parsed as-is from config (see internal/config), not built up
// from a higher-level schema.
package xds

import (
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	"github.com/kassymkhan1/xds/internal/config"
)

// BuildSnapshot turns the loaded CDS/EDS/LDS/RDS/SDS resources into an xDS
// snapshot.
func BuildSnapshot(version string, cfg *config.Config) *cache.Snapshot {
	snap, err := cache.NewSnapshot(version, map[resource.Type][]types.Resource{
		resource.ClusterType:  asResources(cfg.Clusters),
		resource.EndpointType: asResources(cfg.Endpoints),
		resource.ListenerType: asResources(cfg.Listeners),
		resource.RouteType:    asResources(cfg.Routes),
		resource.SecretType:   asResources(cfg.Secrets),
	})
	if err != nil {
		panic(err)
	}
	return snap
}

func asResources[T types.Resource](items []T) []types.Resource {
	resources := make([]types.Resource, len(items))
	for i, item := range items {
		resources[i] = item
	}
	return resources
}
