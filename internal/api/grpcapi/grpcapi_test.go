package grpcapi

import (
	"context"
	"net"
	"testing"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/kassymkhan1/xds/internal/xds"
	"github.com/kassymkhan1/xds/pkg/api/pb"
)

func newClient(t *testing.T) pb.XDSClient {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	state := xds.NewState(cache.NewSnapshotCache(true, cache.IDHash{}, nil), "node", false)
	pb.RegisterXDSServer(srv, New(state))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return pb.NewXDSClient(conn)
}

func TestClusterRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := newClient(t)

	raw, err := proto.Marshal(&cluster.Cluster{Name: "echo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Cluster(ctx, &pb.ClusterRequest{Clusters: []*pb.Resource{{Raw: raw}}}); err != nil {
		t.Fatal(err)
	}

	resp, err := client.GetCDS(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetClusters()) != 1 {
		t.Fatalf("got %d clusters, want 1", len(resp.GetClusters()))
	}
	got := &cluster.Cluster{}
	if err := proto.Unmarshal(resp.GetClusters()[0].GetRaw(), got); err != nil || got.GetName() != "echo" {
		t.Errorf("GetCDS returned %v (err %v)", got, err)
	}

	lds, err := client.GetLDS(ctx, &emptypb.Empty{})
	if err != nil || len(lds.GetListeners()) != 0 {
		t.Errorf("GetLDS = %v, %v", lds, err)
	}
}

func TestClusterInvalid(t *testing.T) {
	client := newClient(t)

	_, err := client.Cluster(context.Background(), &pb.ClusterRequest{Clusters: []*pb.Resource{{Raw: []byte("garbage")}}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}

	raw, _ := proto.Marshal(&cluster.Cluster{}) // missing name
	_, err = client.Cluster(context.Background(), &pb.ClusterRequest{Clusters: []*pb.Resource{{Raw: raw}}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}
