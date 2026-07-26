// Command xds-server runs an Envoy xDS management server: it loads raw
// Envoy resources, one config file per xDS API (CDS, EDS, LDS, RDS, SDS),
// and streams them to connected Envoy proxies over ADS. Each config file is
// polled independently and hot-reloaded without restarting.
//
// See: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/operations/dynamic_configuration
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"

	"github.com/kassymkhan1/xds/internal/config"
	"github.com/kassymkhan1/xds/internal/xds"
)

const configPollInterval = 2 * time.Second

func main() {
	var (
		port   uint
		nodeID string
		debug  bool
		paths  config.Paths
	)

	flag.UintVar(&port, "port", 18000, "xDS management server port")
	flag.StringVar(&nodeID, "node-id", "test-id", "node ID this snapshot is served to (must match Envoy's bootstrap node.id)")
	flag.BoolVar(&debug, "debug", false, "enable verbose xDS stream logging")
	flag.StringVar(&paths.CDS, "config-cds", "configs/cds.yaml", "path to the CDS (cluster) config file")
	flag.StringVar(&paths.EDS, "config-eds", "configs/eds.yaml", "path to the EDS (endpoint) config file (optional)")
	flag.StringVar(&paths.LDS, "config-lds", "configs/lds.yaml", "path to the LDS (listener) config file")
	flag.StringVar(&paths.RDS, "config-rds", "configs/rds.yaml", "path to the RDS (route) config file (optional)")
	flag.StringVar(&paths.SDS, "config-sds", "configs/sds.yaml", "path to the SDS (secret) config file (optional)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	snapCache := cache.NewSnapshotCache(true /* ads */, cache.IDHash{}, nil)

	cfg, err := config.Load(paths)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	version := 1
	if err := applySnapshot(ctx, snapCache, nodeID, cfg, version); err != nil {
		log.Fatalf("failed to apply initial snapshot: %v", err)
	}
	logLoaded("loaded", paths, cfg, version)

	go watchConfig(ctx, watchedPaths(paths), func() {
		newCfg, err := config.Load(paths)
		if err != nil {
			log.Printf("config reload failed, keeping previous config: %v", err)
			return
		}

		version++
		if err := applySnapshot(ctx, snapCache, nodeID, newCfg, version); err != nil {
			log.Printf("failed to apply reloaded config: %v", err)
			return
		}
		cfg = newCfg
		logLoaded("reloaded", paths, cfg, version)
	})

	callbacks := xds.NewCallbacks(debug)
	grpcServer := xds.NewGRPCServer(ctx, snapCache, callbacks)

	lis, err := xds.Listen(port)
	if err != nil {
		log.Fatalf("failed to listen on port %d: %v", port, err)
	}

	go func() {
		<-ctx.Done()
		log.Println("shutting down xDS server")
		grpcServer.GracefulStop()
	}()

	log.Printf("xDS server listening on :%d (node-id=%q)", port, nodeID)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("xDS server error: %v", err)
	}
}

func applySnapshot(ctx context.Context, snapCache cache.SnapshotCache, nodeID string, cfg *config.Config, version int) error {
	snapshot := xds.BuildSnapshot(strconv.Itoa(version), cfg)
	if err := snapshot.Consistent(); err != nil {
		return err
	}
	return snapCache.SetSnapshot(ctx, nodeID, snapshot)
}

func logLoaded(verb string, paths config.Paths, cfg *config.Config, version int) {
	log.Printf("%s config from %s, %s, %s, %s, %s (%d clusters, %d endpoints, %d listeners, %d routes, %d secrets, version=%d)",
		verb, paths.CDS, paths.EDS, paths.LDS, paths.RDS, paths.SDS,
		len(cfg.Clusters), len(cfg.Endpoints), len(cfg.Listeners), len(cfg.Routes), len(cfg.Secrets), version)
}

func watchedPaths(paths config.Paths) []string {
	return []string{paths.CDS, paths.EDS, paths.LDS, paths.RDS, paths.SDS}
}

// watchConfig polls each of paths' mtime and calls onChange whenever any of
// them advances - so editing just one xDS API's file still triggers a
// reload, independently of the others. A path that doesn't exist (an
// optional EDS/RDS/SDS file) is simply skipped until it appears. Polling
// (rather than fsnotify) keeps this dependency-free and works the same on
// every platform the control plane might run on.
func watchConfig(ctx context.Context, paths []string, onChange func()) {
	last := statConfig(paths)

	ticker := time.NewTicker(configPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := statConfig(paths)
			if !sameStat(last, cur) {
				last = cur
				onChange()
			}
		}
	}
}

// statConfig returns the mtime of each path that currently stat's
// successfully, keyed by path. A path that fails to stat (e.g. missing or
// deleted) is simply omitted, which is itself a detectable change.
func statConfig(paths []string) map[string]time.Time {
	stat := make(map[string]time.Time, len(paths))
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			stat[p] = info.ModTime()
		}
	}
	return stat
}

func sameStat(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for name, mod := range a {
		if !b[name].Equal(mod) {
			return false
		}
	}
	return true
}
