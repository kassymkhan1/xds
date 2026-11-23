// Package httpapi serves the HTTP management API: health checks and
// reading/replacing the served clusters and listeners as YAML or JSON.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/kassymkhan1/xds/internal/config"
	"github.com/kassymkhan1/xds/internal/xds"
)

const maxBodySize = 32 << 20

// API is the HTTP management API backed by an xds.State.
type API struct {
	state *xds.State
}

// New returns an API backed by state.
func New(state *xds.State) *API {
	return &API{state: state}
}

// Handler returns the API's routes.
//
//	GET  /health      liveness probe
//	GET  /clusters    served clusters, as a JSON list
//	GET  /listeners   served listeners, as a JSON list
//	POST /clusters    replace all clusters  (body: YAML/JSON list of Cluster)
//	POST /listeners   replace all listeners (body: YAML/JSON list of Listener)
//
// The inf-xds paths /get_clusters, /get_listeners, /set_clusters and
// /set_listeners are kept as aliases.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", a.health)

	for _, p := range []string{"/clusters", "/get_clusters"} {
		mux.HandleFunc("GET "+p, a.getClusters)
	}
	for _, p := range []string{"/listeners", "/get_listeners"} {
		mux.HandleFunc("GET "+p, a.getListeners)
	}
	for _, p := range []string{"/clusters", "/set_clusters"} {
		mux.HandleFunc("POST "+p, a.setClusters)
	}
	for _, p := range []string{"/listeners", "/set_listeners"} {
		mux.HandleFunc("POST "+p, a.setListeners)
	}

	return mux
}

// Run serves the API on addr until ctx is cancelled, then shuts down
// gracefully.
func (a *API) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("http api listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http api: %w", err)
	}
	return nil
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) getClusters(w http.ResponseWriter, _ *http.Request) {
	writeResources(w, a.state.Clusters())
}

func (a *API) getListeners(w http.ResponseWriter, _ *http.Request) {
	writeResources(w, a.state.Listeners())
}

func (a *API) setClusters(w http.ResponseWriter, r *http.Request) {
	set(w, r, "clusters", config.ParseClusters, a.state.SetClusters)
}

func (a *API) setListeners(w http.ResponseWriter, r *http.Request) {
	set(w, r, "listeners", config.ParseListeners, a.state.SetListeners)
}

func set[T any](
	w http.ResponseWriter, r *http.Request, kind string,
	parse func([]byte) ([]T, error),
	apply func(context.Context, []T) (string, error),
) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySize))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, err)
		return
	}

	items, err := parse(body)
	if err != nil {
		slog.Warn("http api: rejected update", "kind", kind, "error", err)
		writeError(w, http.StatusBadRequest, err)
		return
	}

	slog.Info("http api: update received", "kind", kind, "count", len(items))

	version, err := apply(r.Context(), items)
	switch {
	case errors.Is(err, xds.ErrBypassed):
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "bypassed"})
	case err != nil:
		slog.Warn("http api: rejected update", "kind", kind, "error", err)
		writeError(w, http.StatusUnprocessableEntity, err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "applied", "version": version})
	}
}

var marshalOpts = protojson.MarshalOptions{UseProtoNames: true}

func writeResources[T proto.Message](w http.ResponseWriter, items []T) {
	out := make([]json.RawMessage, len(items))
	for i, item := range items {
		b, err := marshalOpts.Marshal(item)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		out[i] = b
	}
	writeJSON(w, http.StatusOK, out)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
