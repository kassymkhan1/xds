package xds

import (
	"context"
	"time"

	clusterservice "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointservice "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	listenerservice "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routeservice "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	runtimeservice "github.com/envoyproxy/go-control-plane/envoy/service/runtime/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	grpcKeepaliveTime        = 30 * time.Second
	grpcKeepaliveTimeout     = 5 * time.Second
	grpcKeepaliveMinTime     = 30 * time.Second
	grpcMaxConcurrentStreams = 1_000_000
)

// NewGRPCServer wires an xDS control plane server (backed by snapCache and
// callbacks) into a *grpc.Server, registering both the Aggregated Discovery
// Service and the individual per-resource discovery services for clients
// that don't speak ADS. Further services (the management API) can be
// registered on the returned server before Serve.
func NewGRPCServer(ctx context.Context, snapCache cache.Cache, callbacks server.Callbacks) *grpc.Server {
	xdsSrv := server.NewServer(ctx, snapCache, callbacks)

	// gRPC's default HTTP/2 stream limits are too low for a management
	// server multiplexing many Envoy xDS streams over one connection.
	grpcServer := grpc.NewServer(
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    grpcKeepaliveTime,
			Timeout: grpcKeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             grpcKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
	)

	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(grpcServer, xdsSrv)
	endpointservice.RegisterEndpointDiscoveryServiceServer(grpcServer, xdsSrv)
	clusterservice.RegisterClusterDiscoveryServiceServer(grpcServer, xdsSrv)
	routeservice.RegisterRouteDiscoveryServiceServer(grpcServer, xdsSrv)
	listenerservice.RegisterListenerDiscoveryServiceServer(grpcServer, xdsSrv)
	secretservice.RegisterSecretDiscoveryServiceServer(grpcServer, xdsSrv)
	runtimeservice.RegisterRuntimeDiscoveryServiceServer(grpcServer, xdsSrv)

	return grpcServer
}
