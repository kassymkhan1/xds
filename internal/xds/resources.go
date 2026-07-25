// Package xds builds Envoy xDS resources (CDS/EDS/RDS/LDS) and serves them
// to Envoy proxies over the Aggregated Discovery Service (ADS), per
// https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/operations/dynamic_configuration.
package xds

import (
	"time"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	router "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/kassymkhan1/xds/internal/config"
)

// makeConfigSource points dynamic resources (EDS/RDS) back at this control
// plane via ADS, so Envoy fetches every resource type over a single stream.
// ADS is what keeps CDS/EDS/LDS/RDS updates ordered and consistent - see the
// "Dynamic configuration" architecture overview linked above.
func makeConfigSource() *core.ConfigSource {
	return &core.ConfigSource{
		ResourceApiVersion: resource.DefaultAPIVersion,
		ConfigSourceSpecifier: &core.ConfigSource_Ads{
			Ads: &core.AggregatedConfigSource{},
		},
	}
}

// makeCluster builds an EDS-backed cluster: membership comes from a separate
// EndpointDiscoveryService response rather than being embedded here.
func makeCluster(clusterName string) *cluster.Cluster {
	return &cluster.Cluster{
		Name:                 clusterName,
		ConnectTimeout:       durationpb.New(5 * time.Second),
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
		EdsClusterConfig: &cluster.Cluster_EdsClusterConfig{
			EdsConfig: makeConfigSource(),
		},
		LbPolicy: cluster.Cluster_ROUND_ROBIN,
	}
}

// makeEndpoint builds the ClusterLoadAssignment (EDS response) for a
// cluster's upstream pool.
func makeEndpoint(clusterName string, endpoints []config.Endpoint) *endpoint.ClusterLoadAssignment {
	lbEndpoints := make([]*endpoint.LbEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		lbEndpoints = append(lbEndpoints, &endpoint.LbEndpoint{
			HostIdentifier: &endpoint.LbEndpoint_Endpoint{
				Endpoint: &endpoint.Endpoint{
					Address: &core.Address{
						Address: &core.Address_SocketAddress{
							SocketAddress: &core.SocketAddress{
								Protocol: core.SocketAddress_TCP,
								Address:  ep.Host,
								PortSpecifier: &core.SocketAddress_PortValue{
									PortValue: ep.Port,
								},
							},
						},
					},
				},
			},
		})
	}

	return &endpoint.ClusterLoadAssignment{
		ClusterName: clusterName,
		Endpoints: []*endpoint.LocalityLbEndpoints{{
			LbEndpoints: lbEndpoints,
		}},
	}
}

// makeRoute builds a RouteConfiguration (RDS response) that sends all
// traffic on any virtual host to clusterName.
func makeRoute(routeName, clusterName string) *route.RouteConfiguration {
	return &route.RouteConfiguration{
		Name: routeName,
		VirtualHosts: []*route.VirtualHost{{
			Name:    "local_service",
			Domains: []string{"*"},
			Routes: []*route.Route{{
				Match: &route.RouteMatch{
					PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"},
				},
				Action: &route.Route_Route{
					Route: &route.RouteAction{
						ClusterSpecifier: &route.RouteAction_Cluster{Cluster: clusterName},
					},
				},
			}},
		}},
	}
}

// makeHTTPListener builds a Listener (LDS response) with an HTTP connection
// manager whose routes are resolved dynamically via RDS.
func makeHTTPListener(listenerName, routeName, address string, port uint32) *listener.Listener {
	routerConfig, err := anypb.New(&router.Router{})
	if err != nil {
		panic(err)
	}

	manager := &hcm.HttpConnectionManager{
		CodecType:  hcm.HttpConnectionManager_AUTO,
		StatPrefix: "ingress_http",
		RouteSpecifier: &hcm.HttpConnectionManager_Rds{
			Rds: &hcm.Rds{
				ConfigSource:    makeConfigSource(),
				RouteConfigName: routeName,
			},
		},
		HttpFilters: []*hcm.HttpFilter{{
			Name:       "envoy.filters.http.router",
			ConfigType: &hcm.HttpFilter_TypedConfig{TypedConfig: routerConfig},
		}},
	}

	pbst, err := anypb.New(manager)
	if err != nil {
		panic(err)
	}

	return &listener.Listener{
		Name: listenerName,
		Address: &core.Address{
			Address: &core.Address_SocketAddress{
				SocketAddress: &core.SocketAddress{
					Protocol: core.SocketAddress_TCP,
					Address:  address,
					PortSpecifier: &core.SocketAddress_PortValue{
						PortValue: port,
					},
				},
			},
		},
		FilterChains: []*listener.FilterChain{{
			Filters: []*listener.Filter{{
				Name:       "envoy.filters.network.http_connection_manager",
				ConfigType: &listener.Filter_TypedConfig{TypedConfig: pbst},
			}},
		}},
	}
}

// BuildSnapshot turns a service inventory loaded from config into an xDS
// snapshot: one listener + route + EDS cluster + endpoint set per service.
func BuildSnapshot(version string, services []config.Service) *cache.Snapshot {
	clusters := make([]types.Resource, 0, len(services))
	routes := make([]types.Resource, 0, len(services))
	listeners := make([]types.Resource, 0, len(services))
	endpoints := make([]types.Resource, 0, len(services))

	for _, svc := range services {
		clusterName := svc.Name
		routeName := svc.Name + "_route"
		listenerName := svc.Name + "_listener"

		clusters = append(clusters, makeCluster(clusterName))
		routes = append(routes, makeRoute(routeName, clusterName))
		listeners = append(listeners, makeHTTPListener(listenerName, routeName, "0.0.0.0", svc.ListenerPort))
		endpoints = append(endpoints, makeEndpoint(clusterName, svc.Endpoints))
	}

	snap, err := cache.NewSnapshot(version, map[resource.Type][]types.Resource{
		resource.ClusterType:  clusters,
		resource.RouteType:    routes,
		resource.ListenerType: listeners,
		resource.EndpointType: endpoints,
	})
	if err != nil {
		panic(err)
	}
	return snap
}
