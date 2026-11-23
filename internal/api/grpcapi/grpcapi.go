// Package grpcapi implements the xds.XDS management service (api/xds.proto):
// pushing and reading raw, binary-encoded Envoy resources over gRPC. It is
// served on the same gRPC server as the xDS discovery services.
package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/kassymkhan1/xds/internal/config"
	"github.com/kassymkhan1/xds/internal/xds"
	"github.com/kassymkhan1/xds/pkg/api/pb"
)

// Server implements pb.XDSServer on top of an xds.State.
type Server struct {
	pb.UnimplementedXDSServer
	state *xds.State
}

// New returns a management Server backed by state.
func New(state *xds.State) *Server {
	return &Server{state: state}
}

// Cluster replaces all served clusters with the ones in r.
func (s *Server) Cluster(ctx context.Context, r *pb.ClusterRequest) (*emptypb.Empty, error) {
	slog.Info("grpc api: clusters update received", "clusters", len(r.GetClusters()))

	clusters := make([]*cluster.Cluster, len(r.GetClusters()))
	for i, raw := range r.GetClusters() {
		c := &cluster.Cluster{}
		if err := proto.Unmarshal(raw.GetRaw(), c); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "cluster %d: %v", i, err)
		}
		clusters[i] = c
	}
	if err := config.ValidateClusters(clusters); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if _, err := s.state.SetClusters(ctx, clusters); err != nil && !errors.Is(err, xds.ErrBypassed) {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &emptypb.Empty{}, nil
}

// GetCDS returns the currently served clusters.
func (s *Server) GetCDS(context.Context, *emptypb.Empty) (*pb.GetCDSResponse, error) {
	res, err := marshalAll(s.state.Clusters())
	if err != nil {
		return nil, err
	}
	return &pb.GetCDSResponse{Clusters: res}, nil
}

// GetLDS returns the currently served listeners.
func (s *Server) GetLDS(context.Context, *emptypb.Empty) (*pb.GetLDSResponse, error) {
	res, err := marshalAll(s.state.Listeners())
	if err != nil {
		return nil, err
	}
	return &pb.GetLDSResponse{Listeners: res}, nil
}

func marshalAll[T proto.Message](items []T) ([]*pb.Resource, error) {
	res := make([]*pb.Resource, len(items))
	for i, item := range items {
		raw, err := proto.Marshal(item)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "marshal resource %d: %v", i, err)
		}
		res[i] = &pb.Resource{Raw: raw}
	}
	return res, nil
}
