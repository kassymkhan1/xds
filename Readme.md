# xds

Production-ready control plane for [Envoy Proxy](https://www.envoyproxy.io/), implementing the **xDS** (Discovery Service) protocol. The server dynamically configures Envoy proxies across a cluster/service mesh without restarting them or dropping connections.

## What it does

The control plane serves configuration to connected Envoy proxies over gRPC (xDS API). Each xDS API is configured from its own file, loaded, validated, and hot-reloaded independently of the others:

- **CDS** (Cluster Discovery Service) — upstream clusters, from [configs/cds.yaml](configs/cds.yaml)
- **EDS** (Endpoint Discovery Service) — endpoints within clusters, from [configs/eds.yaml](configs/eds.yaml) *(optional)*
- **LDS** (Listener Discovery Service) — listeners, from [configs/lds.yaml](configs/lds.yaml)
- **RDS** (Route Discovery Service) — routing rules, from [configs/rds.yaml](configs/rds.yaml) *(optional)*
- **SDS** (Secret Discovery Service) — TLS certificates and secrets, from `configs/sds.yaml` *(optional, no default file)*

CDS and LDS are required - there's nothing to serve without at least one
cluster and one listener. EDS, RDS, and SDS are optional: a cluster can embed
its endpoints directly in `load_assignment` instead of pointing at EDS, and a
listener can embed its `route_config` directly instead of pointing at RDS: if
you don't reference one of those APIs, its config file doesn't need to exist.

Implemented in Go, on top of the official [envoyproxy/go-control-plane](https://github.com/envoyproxy/go-control-plane) library.

## Status

Under active development.

## Stack

- Go
- gRPC / Protobuf (Envoy API v3)
- go-control-plane (envoy snapshot cache)

## Getting Started

Requires Go 1.25+.

```sh
go mod tidy                        # fetch go-control-plane, grpc, protobuf
go run ./cmd/xds-server -debug      # loads configs/{cds,eds,lds,rds}.yaml, starts the xDS server on :18000
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

`configs/sds.yaml` (Secret resources) has no default file since there's no
TLS material to ship as an example - point `-config-sds` at one if you need
it.

Referencing an extension type not already compiled in (another HTTP/network
filter, a transport socket, etc.) needs a matching blank import added to
`internal/config/register.go`, so `protojson` can resolve its `@type` when
parsing the YAML.

All five files are polled every 2s and hot-reloaded independently: editing
and saving any one while `xds-server` is running pushes a new snapshot to
connected Envoys without a restart, and a failure parsing one file doesn't
touch the others' last-good config. Point at different files with
`-config-cds`, `-config-eds`, `-config-lds`, `-config-rds`, `-config-sds`.

Try it against a real Envoy using the example bootstrap, which points Envoy
at this control plane over ADS:

```sh
envoy -c example/bootstrap.yaml
```

### Docker

```sh
docker build -t xds-server .
docker run --rm -p 18000:18000 -v "$(pwd)/configs:/etc/xds/configs" xds-server -debug
```

## Project Structure

```
cmd/xds-server/         entrypoint: flags, per-file config load/reload loop, gRPC server lifecycle
internal/config/
  config.go               CDS/EDS/LDS/RDS/SDS YAML loaders + validator (raw Envoy resources, see above)
  register.go              blank imports registering extension proto types for protojson
internal/xds/
  resources.go            wraps loaded resources into an xDS snapshot
  server.go                gRPC server wiring (ADS + individual xDS services, keepalive)
  callbacks.go              xDS stream lifecycle logging (server.Callbacks implementation)
configs/                 default config (see above): cds.yaml, eds.yaml, lds.yaml, rds.yaml
example/bootstrap.yaml   sample Envoy bootstrap that connects to this control plane via ADS
```

`internal/xds.BuildSnapshot` just wraps the parsed resource slices into an
xDS snapshot - no transformation, since `config.Load` already parsed them
into the exact proto shapes Envoy expects. To source resources from
somewhere other than a file (a service registry, Kubernetes CRDs, etc.),
swap out `internal/config.Load` for your own loader and keep calling
`cache.SetSnapshot` on change.
