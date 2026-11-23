package xds

import (
	"context"
	"errors"
	"testing"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	"github.com/kassymkhan1/xds/internal/config"
)

func TestStateVersionsAndConsistency(t *testing.T) {
	ctx := context.Background()
	snapCache := cache.NewSnapshotCache(true, cache.IDHash{}, nil)
	s := NewState(snapCache, "node", false)

	for _, want := range []string{"1", "2"} {
		v, err := s.SetClusters(ctx, []*cluster.Cluster{{Name: "a"}})
		if err != nil {
			t.Fatal(err)
		}
		if v != want {
			t.Errorf("version = %s, want %s", v, want)
		}
	}

	// An EDS cluster without a matching ClusterLoadAssignment is
	// inconsistent: it must be rejected and the previous config kept.
	eds := &cluster.Cluster{
		Name:                 "b",
		ClusterDiscoveryType: &cluster.Cluster_Type{Type: cluster.Cluster_EDS},
	}
	if _, err := s.SetClusters(ctx, []*cluster.Cluster{eds}); err == nil {
		t.Fatal("expected inconsistent snapshot to be rejected")
	}
	if got := s.Clusters(); len(got) != 1 || got[0].GetName() != "a" {
		t.Errorf("previous clusters not kept: %v", got)
	}

	snap, err := snapCache.GetSnapshot("node")
	if err != nil {
		t.Fatal(err)
	}
	if v := snap.GetVersion(resource.ClusterType); v != "2" {
		t.Errorf("served version = %s, want 2", v)
	}

}

func TestStateBypass(t *testing.T) {
	s := NewState(cache.NewSnapshotCache(true, cache.IDHash{}, nil), "node", true)
	if _, err := s.SetClusters(context.Background(), []*cluster.Cluster{{Name: "a"}}); !errors.Is(err, ErrBypassed) {
		t.Fatalf("err = %v, want ErrBypassed", err)
	}
	if len(s.Clusters()) != 0 {
		t.Error("bypassed update was applied")
	}
}

func TestStateAllowsUnreferencedEndpoints(t *testing.T) {
	ctx := context.Background()
	s := NewState(cache.NewSnapshotCache(true, cache.IDHash{}, nil), "node", false)

	if _, err := s.Update(ctx, func(c *config.Config) {
		c.Endpoints = []*endpoint.ClusterLoadAssignment{{ClusterName: "unused"}}
	}); err != nil {
		t.Fatalf("unreferenced endpoints rejected: %v", err)
	}
}
