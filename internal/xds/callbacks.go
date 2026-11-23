package xds

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
)

// Callbacks logs the xDS stream lifecycle (at debug level, when Debug is
// set) and tracks how many streams are currently open. It satisfies
// server.Callbacks (sotw + delta + rest).
type Callbacks struct {
	Debug bool

	mu            sync.Mutex
	streamsOpen   map[int64]struct{}
	fetchesTotal  int64
	requestsTotal int64
}

func NewCallbacks(debug bool) *Callbacks {
	return &Callbacks{
		Debug:       debug,
		streamsOpen: make(map[int64]struct{}),
	}
}

func (c *Callbacks) logf(format string, args ...any) {
	if c.Debug {
		slog.Debug(fmt.Sprintf(format, args...))
	}
}

func (c *Callbacks) OnStreamOpen(_ context.Context, streamID int64, typeURL string) error {
	c.mu.Lock()
	c.streamsOpen[streamID] = struct{}{}
	c.mu.Unlock()
	c.logf("stream %d open, type=%s", streamID, typeURL)
	return nil
}

func (c *Callbacks) OnStreamClosed(streamID int64, node *core.Node) {
	c.mu.Lock()
	delete(c.streamsOpen, streamID)
	c.mu.Unlock()
	c.logf("stream %d closed, node=%s", streamID, node.GetId())
}

func (c *Callbacks) OnDeltaStreamOpen(_ context.Context, streamID int64, typeURL string) error {
	c.mu.Lock()
	c.streamsOpen[streamID] = struct{}{}
	c.mu.Unlock()
	c.logf("delta stream %d open, type=%s", streamID, typeURL)
	return nil
}

func (c *Callbacks) OnDeltaStreamClosed(streamID int64, node *core.Node) {
	c.mu.Lock()
	delete(c.streamsOpen, streamID)
	c.mu.Unlock()
	c.logf("delta stream %d closed, node=%s", streamID, node.GetId())
}

func (c *Callbacks) OnStreamRequest(streamID int64, req *discovery.DiscoveryRequest) error {
	atomic.AddInt64(&c.requestsTotal, 1)
	c.logf("stream %d request: node=%s type=%s version=%s resources=%v",
		streamID, req.GetNode().GetId(), req.GetTypeUrl(), req.GetVersionInfo(), req.GetResourceNames())
	return nil
}

func (c *Callbacks) OnStreamResponse(_ context.Context, streamID int64, req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
	c.logf("stream %d response: type=%s version=%s", streamID, req.GetTypeUrl(), resp.GetVersionInfo())
}

func (c *Callbacks) OnStreamDeltaRequest(streamID int64, req *discovery.DeltaDiscoveryRequest) error {
	atomic.AddInt64(&c.requestsTotal, 1)
	c.logf("delta stream %d request: node=%s type=%s subscribe=%v unsubscribe=%v",
		streamID, req.GetNode().GetId(), req.GetTypeUrl(), req.GetResourceNamesSubscribe(), req.GetResourceNamesUnsubscribe())
	return nil
}

func (c *Callbacks) OnStreamDeltaResponse(streamID int64, req *discovery.DeltaDiscoveryRequest, resp *discovery.DeltaDiscoveryResponse) {
	c.logf("delta stream %d response: type=%s version=%s", streamID, req.GetTypeUrl(), resp.GetSystemVersionInfo())
}

func (c *Callbacks) OnFetchRequest(_ context.Context, req *discovery.DiscoveryRequest) error {
	atomic.AddInt64(&c.fetchesTotal, 1)
	c.logf("fetch request: node=%s type=%s", req.GetNode().GetId(), req.GetTypeUrl())
	return nil
}

func (c *Callbacks) OnFetchResponse(req *discovery.DiscoveryRequest, resp *discovery.DiscoveryResponse) {
	c.logf("fetch response: type=%s version=%s", req.GetTypeUrl(), resp.GetVersionInfo())
}
