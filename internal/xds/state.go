package xds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"

	"github.com/kassymkhan1/xds/internal/config"
)

// ErrBypassed is returned for a management API update that was received
// but deliberately not applied because the server runs in bypass mode.
var ErrBypassed = errors.New("bypass mode: update not applied")

// State is the single source of truth for the resources served to one
// Envoy node. Config file reloads and management API pushes (HTTP/gRPC) all
// go through it, so every change produces exactly one new, consistent
// snapshot with a strictly increasing version. An update that would make
// the snapshot inconsistent is rejected and the previous config stays live.
type State struct {
	cache  cache.SnapshotCache
	nodeID string
	bypass bool

	mu      sync.Mutex
	version uint64
	cfg     config.Config
}

// NewState returns a State publishing snapshots for nodeID into snapCache.
// With bypass set, management API updates are logged and dropped (config
// files are still applied).
func NewState(snapCache cache.SnapshotCache, nodeID string, bypass bool) *State {
	return &State{cache: snapCache, nodeID: nodeID, bypass: bypass}
}

// Update applies mutate to a copy of the current config and publishes the
// result as a new snapshot. It returns the new version.
func (s *State) Update(ctx context.Context, mutate func(*config.Config)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cfg
	mutate(&next)

	version := strconv.FormatUint(s.version+1, 10)
	snap, err := BuildSnapshot(version, &next)
	if err != nil {
		return "", fmt.Errorf("building snapshot: %w", err)
	}
	if err := s.cache.SetSnapshot(ctx, s.nodeID, snap); err != nil {
		return "", fmt.Errorf("setting snapshot: %w", err)
	}

	s.version++
	s.cfg = next
	slog.Info("snapshot updated",
		"node_id", s.nodeID, "version", version,
		"clusters", len(next.Clusters), "endpoints", len(next.Endpoints),
		"listeners", len(next.Listeners), "routes", len(next.Routes), "secrets", len(next.Secrets))
	return version, nil
}

// SetClusters replaces all served clusters (management API).
func (s *State) SetClusters(ctx context.Context, clusters []*cluster.Cluster) (string, error) {
	if s.bypass {
		slog.Info("bypass: ignoring clusters update", "clusters", len(clusters))
		return "", ErrBypassed
	}
	return s.Update(ctx, func(c *config.Config) { c.Clusters = clusters })
}

// SetListeners replaces all served listeners (management API).
func (s *State) SetListeners(ctx context.Context, listeners []*listener.Listener) (string, error) {
	if s.bypass {
		slog.Info("bypass: ignoring listeners update", "listeners", len(listeners))
		return "", ErrBypassed
	}
	return s.Update(ctx, func(c *config.Config) { c.Listeners = listeners })
}

// Clusters returns the currently served clusters.
func (s *State) Clusters() []*cluster.Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Clusters
}

// Listeners returns the currently served listeners.
func (s *State) Listeners() []*listener.Listener {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Listeners
}
