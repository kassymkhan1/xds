// Command xds-server runs an Envoy xDS management server: it loads a
// service inventory from a config file, turns it into CDS/EDS/LDS/RDS
// resources, and streams them to connected Envoy proxies over ADS. The
// config file is polled for changes and hot-reloaded without restarting.
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
		port       uint
		nodeID     string
		debug      bool
		configPath string
	)

	flag.UintVar(&port, "port", 18000, "xDS management server port")
	flag.StringVar(&nodeID, "node-id", "test-id", "node ID this snapshot is served to (must match Envoy's bootstrap node.id)")
	flag.BoolVar(&debug, "debug", false, "enable verbose xDS stream logging")
	flag.StringVar(&configPath, "config", "config.yaml", "path to the service config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	snapCache := cache.NewSnapshotCache(true /* ads */, cache.IDHash{}, nil)

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	version := 1
	if err := applySnapshot(ctx, snapCache, nodeID, cfg, version); err != nil {
		log.Fatalf("failed to apply initial snapshot: %v", err)
	}
	log.Printf("loaded config from %s (%d services, version=%d)", configPath, len(cfg.Services), version)

	go watchConfig(ctx, configPath, func() {
		newCfg, err := config.Load(configPath)
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
		log.Printf("reloaded config from %s (%d services, version=%d)", configPath, len(cfg.Services), version)
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
	snapshot := xds.BuildSnapshot(strconv.Itoa(version), cfg.Services)
	if err := snapshot.Consistent(); err != nil {
		return err
	}
	return snapCache.SetSnapshot(ctx, nodeID, snapshot)
}

// watchConfig polls path's mtime and calls onChange whenever it advances.
// Polling (rather than fsnotify) keeps this dependency-free and works the
// same on every platform the control plane might run on.
func watchConfig(ctx context.Context, path string, onChange func()) {
	var lastMod time.Time
	if info, err := os.Stat(path); err == nil {
		lastMod = info.ModTime()
	}

	ticker := time.NewTicker(configPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastMod) {
				lastMod = info.ModTime()
				onChange()
			}
		}
	}
}
