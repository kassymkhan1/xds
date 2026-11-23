// Command xds-server runs an Envoy xDS management server: it loads raw
// Envoy resources, one source per xDS API (CDS, EDS, LDS, RDS, SDS), and
// streams them to connected Envoy proxies over ADS. Local config files are
// polled and hot-reloaded independently; clusters and listeners can also be
// replaced at runtime through the HTTP and gRPC management APIs.
//
// Every flag can also be set through an XDS_* environment variable (see
// usage), compatible with inf-xds deployments. Flags win over env.
//
// See: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/operations/dynamic_configuration
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"golang.org/x/sync/errgroup"

	"github.com/kassymkhan1/xds/internal/api/grpcapi"
	"github.com/kassymkhan1/xds/internal/api/httpapi"
	"github.com/kassymkhan1/xds/internal/config"
	"github.com/kassymkhan1/xds/internal/env"
	"github.com/kassymkhan1/xds/internal/xds"
	"github.com/kassymkhan1/xds/pkg/api/pb"
)

const configPollInterval = 2 * time.Second

// Set at build time via -ldflags "-X main.version=... -X main.buildstamp=...".
var (
	version    = "dev"
	buildstamp = ""
)

type options struct {
	grpcAddr string
	httpAddr string
	nodeID   string
	logLevel string
	debug    bool
	bypass   bool
	paths    config.Paths
}

func parseFlags() options {
	var o options

	flag.StringVar(&o.grpcAddr, "grpc-addr",
		env.Addr("XDS_GRPC_API_IP", "XDS_GRPC_API_PORT", ":18000"),
		"xDS + gRPC management API listen address [XDS_GRPC_API_IP, XDS_GRPC_API_PORT]")
	flag.StringVar(&o.httpAddr, "http-addr",
		env.Addr("XDS_HTTP_API_IP", "XDS_HTTP_API_PORT", ":2827"),
		"HTTP management API listen address, empty disables it [XDS_HTTP_API_IP, XDS_HTTP_API_PORT]")
	flag.StringVar(&o.nodeID, "node-id", env.String("XDS_NODE_ID", "test-id"),
		"node ID this snapshot is served to, must match Envoy's bootstrap node.id [XDS_NODE_ID]")
	flag.StringVar(&o.logLevel, "log-level", env.String("XDS_LOG_LEVEL", "info"),
		"log level: debug, info, warn, error [XDS_LOG_LEVEL]")
	flag.BoolVar(&o.debug, "debug", false, "log every xDS stream event (implies -log-level=debug)")
	flag.BoolVar(&o.bypass, "bypass", env.Bool("XDS_BY_PASS", false),
		"log management API updates but don't apply them [XDS_BY_PASS]")

	flag.StringVar(&o.paths.CDS, "config-cds",
		env.Source("XDS_SKIP_INITIAL_CDS", "XDS_CDS_BOOTSTRAP_FILE", "XDS_CDS_BOOTSTRAP_URL", "configs/cds.yaml"),
		"CDS (cluster) file or URL, empty skips it [XDS_CDS_BOOTSTRAP_FILE, XDS_CDS_BOOTSTRAP_URL, XDS_SKIP_INITIAL_CDS]")
	flag.StringVar(&o.paths.EDS, "config-eds", env.String("XDS_EDS_BOOTSTRAP_FILE", "configs/eds.yaml"),
		"EDS (endpoint) file or URL, optional [XDS_EDS_BOOTSTRAP_FILE]")
	flag.StringVar(&o.paths.LDS, "config-lds",
		env.Source("XDS_SKIP_INITIAL_LDS", "XDS_LDS_BOOTSTRAP_FILE", "XDS_LDS_BOOTSTRAP_URL", "configs/lds.yaml"),
		"LDS (listener) file or URL, empty skips it [XDS_LDS_BOOTSTRAP_FILE, XDS_LDS_BOOTSTRAP_URL, XDS_SKIP_INITIAL_LDS]")
	flag.StringVar(&o.paths.RDS, "config-rds", env.String("XDS_RDS_BOOTSTRAP_FILE", "configs/rds.yaml"),
		"RDS (route) file or URL, optional [XDS_RDS_BOOTSTRAP_FILE]")
	flag.StringVar(&o.paths.SDS, "config-sds", env.String("XDS_SDS_BOOTSTRAP_FILE", "configs/sds.yaml"),
		"SDS (secret) file or URL, optional [XDS_SDS_BOOTSTRAP_FILE]")

	port := flag.Uint("port", 0, "deprecated: same as -grpc-addr=:PORT")

	flag.Parse()

	if *port != 0 {
		o.grpcAddr = fmt.Sprintf(":%d", *port)
	}
	if o.debug {
		o.logLevel = "debug"
	}
	return o
}

func main() {
	o := parseFlags()

	if err := setupLogging(o.logLevel); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := run(o); err != nil {
		slog.Error("xds-server failed", "error", err)
		os.Exit(1)
	}
}

func run(o options) error {
	slog.Info("starting xds-server", "version", version, "buildstamp", buildstamp, "node_id", o.nodeID, "bypass", o.bypass)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	snapCache := cache.NewSnapshotCache(true /* ads */, cache.IDHash{}, nil)
	state := xds.NewState(snapCache, o.nodeID, o.bypass)

	if err := reload(ctx, state, o.paths); err != nil {
		return fmt.Errorf("loading initial config: %w", err)
	}

	grpcServer := xds.NewGRPCServer(ctx, snapCache, xds.NewCallbacks(o.debug))
	pb.RegisterXDSServer(grpcServer, grpcapi.New(state))

	lis, err := net.Listen("tcp", o.grpcAddr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", o.grpcAddr, err)
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		watchConfig(ctx, o.paths, func(changed config.Paths) {
			if err := reload(ctx, state, changed); err != nil {
				slog.Error("config reload failed, keeping previous config", "error", err)
			}
		})
		return nil
	})

	g.Go(func() error {
		slog.Info("xDS server listening", "addr", lis.Addr().String())
		return grpcServer.Serve(lis)
	})
	g.Go(func() error {
		<-ctx.Done()
		slog.Info("shutting down")
		grpcServer.GracefulStop()
		return nil
	})

	if o.httpAddr != "" {
		g.Go(func() error {
			return httpapi.New(state).Run(ctx, o.httpAddr)
		})
	}

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// reload loads every source configured in paths and replaces just those
// resource types in state, leaving the rest (e.g. clusters pushed through
// the management API when CDS isn't file-backed) untouched.
func reload(ctx context.Context, state *xds.State, paths config.Paths) error {
	cfg, err := config.Load(ctx, paths)
	if err != nil {
		return err
	}

	_, err = state.Update(ctx, func(c *config.Config) {
		if paths.CDS != "" {
			c.Clusters = cfg.Clusters
		}
		if paths.EDS != "" {
			c.Endpoints = cfg.Endpoints
		}
		if paths.LDS != "" {
			c.Listeners = cfg.Listeners
		}
		if paths.RDS != "" {
			c.Routes = cfg.Routes
		}
		if paths.SDS != "" {
			c.Secrets = cfg.Secrets
		}
	})
	return err
}

// watchConfig polls the mtime of each local config file and calls onChange
// with just the sources whose file changed (appeared, was modified, or was
// deleted), so editing one xDS API's file reloads only that API. URL
// sources are fetched once at startup and not polled. Polling (rather than
// fsnotify) keeps this dependency-free and also catches the symlink swaps
// Kubernetes uses for mounted ConfigMaps.
func watchConfig(ctx context.Context, paths config.Paths, onChange func(changed config.Paths)) {
	last := statConfig(paths)

	ticker := time.NewTicker(configPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := statConfig(paths)
			changed := changedPaths(paths, last, cur)
			last = cur
			if changed != (config.Paths{}) {
				onChange(changed)
			}
		}
	}
}

// statConfig returns the mtime of each local file source that currently
// stats successfully. A missing file is simply omitted, which is itself a
// detectable change.
func statConfig(paths config.Paths) map[string]time.Time {
	stat := make(map[string]time.Time)
	for _, p := range paths.All() {
		if config.IsURL(p) {
			continue
		}
		if info, err := os.Stat(p); err == nil {
			stat[p] = info.ModTime()
		}
	}
	return stat
}

// changedPaths returns paths with every source cleared except those whose
// stat differs between prev and cur.
func changedPaths(paths config.Paths, prev, cur map[string]time.Time) config.Paths {
	pick := func(p string) string {
		if p == "" || config.IsURL(p) {
			return ""
		}
		a, okA := prev[p]
		b, okB := cur[p]
		if okA != okB || !a.Equal(b) {
			return p
		}
		return ""
	}
	return config.Paths{
		CDS: pick(paths.CDS),
		EDS: pick(paths.EDS),
		LDS: pick(paths.LDS),
		RDS: pick(paths.RDS),
		SDS: pick(paths.SDS),
	}
}

func setupLogging(level string) error {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return fmt.Errorf("invalid log level %q: %w", level, err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
	return nil
}
