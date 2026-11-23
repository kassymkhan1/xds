# xds

Production-ready control plane for [Envoy Proxy](https://www.envoyproxy.io/), implementing the **xDS** (Discovery Service) protocol. The server dynamically configures Envoy proxies across a cluster/service mesh without restarting them or dropping connections.

## What it does

The control plane serves configuration to connected Envoy proxies over gRPC (xDS API). Each xDS API is configured from its own file, loaded, validated, and hot-reloaded independently of the others:

- **CDS** (Cluster Discovery Service) — upstream clusters, from [configs/cds.yaml](configs/cds.yaml)
- **EDS** (Endpoint Discovery Service) — endpoints within clusters, from [configs/eds.yaml](configs/eds.yaml) *(optional)*
- **LDS** (Listener Discovery Service) — listeners, from [configs/lds.yaml](configs/lds.yaml)
- **RDS** (Route Discovery Service) — routing rules, from [configs/rds.yaml](configs/rds.yaml) *(optional)*
- **SDS** (Secret Discovery Service) — TLS certificates and secrets, from [configs/sds.yaml](configs/sds.yaml) *(optional)*

CDS and LDS are required - there's nothing to serve without at least one
cluster and one listener. EDS, RDS, and SDS are optional: a cluster can embed
its endpoints directly in `load_assignment` instead of pointing at EDS, and a
listener can embed its `route_config` directly instead of pointing at RDS: if
you don't reference one of those APIs, its config file doesn't need to exist.

On top of the file-based config, clusters and listeners can be replaced at
runtime through an HTTP and a gRPC **management API** (ported from
`inf-xds`), and config sources can be fetched from a URL and templated with
environment variables.

Implemented in Go, on top of the official [envoyproxy/go-control-plane](https://github.com/envoyproxy/go-control-plane) library.

## Status

Under active development.

## Stack

- Go
- gRPC / Protobuf (Envoy API v3)
- go-control-plane (envoy snapshot cache)

## Getting Started

Requires Go 1.26+.

```sh
go mod tidy                        # fetch go-control-plane, grpc, protobuf
go run ./cmd/xds-server -debug      # loads configs/*.yaml, xDS on :18000, HTTP API on :2827
```

Each config file is a top-level YAML list of raw Envoy resources in that
API's native shape - the same fields you'd write in a static Envoy
bootstrap, not a simplified schema. The default `configs/` wires a cluster
through EDS and a listener through RDS end to end:

```yaml
# configs/cds.yaml - list of Cluster: https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/cluster.proto
- name: backend_cluster
  connect_timeout: 5s
  type: EDS
  eds_cluster_config:
    eds_config: {ads: {}}
  lb_policy: ROUND_ROBIN
```

```yaml
# configs/eds.yaml - list of ClusterLoadAssignment: https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/endpoint/v3/endpoint.proto
- cluster_name: backend_cluster    # must match a CDS entry's name
  endpoints:
    - lb_endpoints:
        - endpoint:
            address:
              socket_address: {address: 127.0.0.1, port_value: 8080}
```

```yaml
# configs/lds.yaml - list of Listener: https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/listener/v3/listener.proto
- name: backend_cluster_listener
  address:
    socket_address: {address: 0.0.0.0, port_value: 10000}
  filter_chains:
    - filters:
        - name: envoy.filters.network.http_connection_manager
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
            stat_prefix: ingress_http
            rds:
              route_config_name: backend_cluster_route
              config_source: {ads: {}}
            http_filters:
              - name: envoy.filters.http.router
                typed_config: {"@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router}
```

```yaml
# configs/rds.yaml - list of RouteConfiguration: https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/route/v3/route.proto
- name: backend_cluster_route    # must match the listener's rds.route_config_name
  virtual_hosts:
    - name: local_service
      domains: ["*"]
      routes:
        - match: {prefix: "/"}
          route: {cluster: backend_cluster}
```

Referencing an extension type not already compiled in (another HTTP/network
filter, a transport socket, etc.) needs a matching blank import added to
`internal/config/register.go`, so `protojson` can resolve its `@type` when
parsing the YAML.

Every resource is checked against Envoy's own proto validation rules and
for unique names, and every EDS/RDS reference must resolve, so a broken
config is rejected by the control plane (keeping the last-good one live)
instead of being NACKed by every Envoy.

Local files are polled every 2s and hot-reloaded independently: editing
and saving one pushes a new snapshot with only that xDS API reloaded, and a
failure parsing it doesn't touch the others' last-good config. Point at
different sources with `-config-cds`, `-config-eds`, `-config-lds`,
`-config-rds`, `-config-sds`; an empty value skips that API entirely.

### Sources and templating

A source can be a local file or an `http(s)://` URL (fetched once at
startup, not polled). Any `{{ env "NAME" }}` placeholder is replaced with
the environment variable `NAME` before parsing; an unset variable is an
error, not an empty string:

```yaml
- name: upstream
  type: STRICT_DNS
  load_assignment:
    cluster_name: upstream
    endpoints:
      - lb_endpoints:
          - endpoint:
              address:
                socket_address: {address: '{{ env "UPSTREAM_HOST" }}', port_value: 8080}
```

### Configuration

Every flag has an environment variable fallback (the names used by
`inf-xds`, so existing deployments keep working). Flags win over env.

| Flag | Env | Default | |
|---|---|---|---|
| `-grpc-addr` | `XDS_GRPC_API_IP`, `XDS_GRPC_API_PORT` | `:18000` | xDS (ADS) + gRPC management API |
| `-http-addr` | `XDS_HTTP_API_IP`, `XDS_HTTP_API_PORT` | `:2827` | HTTP management API, empty disables |
| `-node-id` | `XDS_NODE_ID` | `test-id` | must match Envoy's `node.id` |
| `-log-level` | `XDS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `-debug` | | `false` | log every xDS stream event |
| `-bypass` | `XDS_BY_PASS` | `false` | log management API updates but don't apply them |
| `-config-cds` | `XDS_CDS_BOOTSTRAP_FILE` / `_URL`, `XDS_SKIP_INITIAL_CDS` | `configs/cds.yaml` | required unless empty/skipped |
| `-config-lds` | `XDS_LDS_BOOTSTRAP_FILE` / `_URL`, `XDS_SKIP_INITIAL_LDS` | `configs/lds.yaml` | required unless empty/skipped |
| `-config-eds` | `XDS_EDS_BOOTSTRAP_FILE` | `configs/eds.yaml` | optional |
| `-config-rds` | `XDS_RDS_BOOTSTRAP_FILE` | `configs/rds.yaml` | optional |
| `-config-sds` | `XDS_SDS_BOOTSTRAP_FILE` | `configs/sds.yaml` | optional |

`-port N` is still accepted as a deprecated alias for `-grpc-addr=:N`.

### Management API

HTTP (`-http-addr`). POST bodies are a YAML or JSON list of resources in the
same shape as the config files; GET returns a JSON list.

| | | |
|---|---|---|
| `GET /health` | | liveness probe |
| `GET /clusters` | alias `/get_clusters` | served clusters |
| `GET /listeners` | alias `/get_listeners` | served listeners |
| `POST /clusters` | alias `/set_clusters` | replace all clusters |
| `POST /listeners` | alias `/set_listeners` | replace all listeners |

A POST answers `200 {"status":"applied","version":"N"}`, `400` for an
unparsable or invalid body, `422` when the result would leave an EDS/RDS
reference unresolved, and `202 {"status":"bypassed"}` in bypass mode.

```sh
curl -X POST --data-binary @configs/cds.yaml localhost:2827/clusters
```

gRPC: the `xds.XDS` service from [api/xds.proto](api/xds.proto) is served on
the xDS port, with `Cluster` (replace clusters), `GetCDS` and `GetLDS`;
resources travel as binary-encoded Envoy protos. Regenerate the Go code with
`make proto`.

Config files and the management API write to the same state, last writer
wins: an API push stays in effect until the corresponding file changes
(reloading `eds.yaml` doesn't touch clusters pushed through the API). To
manage clusters or listeners purely through the API, skip their file with
an empty `-config-cds=` / `-config-lds=`.

Try it against a real Envoy using the example bootstrap, which points Envoy
at this control plane over ADS:

```sh
envoy -c example/bootstrap.yaml
```

### Docker

```sh
docker build -t xds-server .
docker run --rm -p 18000:18000 -p 2827:2827 -v "$(pwd)/configs:/etc/xds/configs" xds-server -debug
```

## Project Structure

```
cmd/xds-server/          entrypoint: flags/env, per-file reload loop, server lifecycle
api/xds.proto            gRPC management API definition
pkg/api/pb/              generated Go code for api/xds.proto (make proto)
internal/config/
  config.go               CDS/EDS/LDS/RDS/SDS loaders and parsers (raw Envoy resources)
  source.go               file / http(s) URL sources
  template.go             {{ env "NAME" }} expansion
  validate.go             proto validation + unique names
  register.go             blank imports registering extension proto types for protojson
internal/xds/
  state.go                shared resource state, one consistent snapshot per change
  resources.go            wraps resources into an xDS snapshot, checks references
  server.go               gRPC server wiring (ADS + individual xDS services, keepalive)
  callbacks.go            xDS stream lifecycle logging (server.Callbacks implementation)
internal/api/
  httpapi/                HTTP management API
  grpcapi/                gRPC management API (xds.XDS)
internal/env/            XDS_* environment variable helpers
configs/                 default config: cds.yaml, eds.yaml, lds.yaml, rds.yaml, sds.yaml
example/bootstrap.yaml   sample Envoy bootstrap that connects to this control plane via ADS
```

`internal/xds.BuildSnapshot` just wraps the parsed resource slices into an
xDS snapshot - no transformation, since `config.Load` already parsed them
into the exact proto shapes Envoy expects. To source resources from
somewhere other than a file (a service registry, Kubernetes CRDs, etc.),
push them through the management API, or call `xds.State.Update` from
your own loader.
