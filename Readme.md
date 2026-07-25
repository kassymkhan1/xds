# xds

Production-ready control plane for [Envoy Proxy](https://www.envoyproxy.io/), implementing the **xDS** (Discovery Service) protocol. The server dynamically configures Envoy proxies across a cluster/service mesh without restarting them or dropping connections.

## What it does

The control plane serves configuration to connected Envoy proxies over gRPC (xDS API), implementing the core discovery services:

- **LDS** (Listener Discovery Service) — listener configuration
- **RDS** (Route Discovery Service) — routing rules
- **CDS** (Cluster Discovery Service) — upstream clusters
- **EDS** (Endpoint Discovery Service) — endpoints within clusters
- **SDS** (Secret Discovery Service) — TLS certificates and secrets

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
go run ./cmd/xds-server -debug      # loads config.yaml, starts the xDS server on :18000
```

Service inventory lives in [config.yaml](config.yaml) — one entry per listener/cluster:

```yaml
services:
  - name: backend_cluster
    listener_port: 10000
    endpoints:
      - host: 127.0.0.1
        port: 8080
```

The file is polled every 2s and hot-reloaded: edit and save it while
`xds-server` is running and the new snapshot is pushed to connected Envoys
without a restart. Point at a different file with `-config path/to/file.yaml`.

Try it against a real Envoy using the example bootstrap, which points Envoy
at this control plane over ADS:

```sh
envoy -c example/bootstrap.yaml
```

### Docker

```sh
docker build -t xds-server .
docker run --rm -p 18000:18000 -v "$(pwd)/config.yaml:/etc/xds/config.yaml" xds-server -debug
```

## Project Structure

```
cmd/xds-server/         entrypoint: flags, config load/reload loop, gRPC server lifecycle
internal/config/
  config.go               service inventory schema + YAML loader/validator
internal/xds/
  resources.go            builders for Cluster/ClusterLoadAssignment/RouteConfiguration/Listener
  server.go                gRPC server wiring (ADS + individual xDS services, keepalive)
  callbacks.go              xDS stream lifecycle logging (server.Callbacks implementation)
config.yaml              default service inventory (see above)
example/bootstrap.yaml   sample Envoy bootstrap that connects to this control plane via ADS
```

`internal/xds.BuildSnapshot` turns a `[]config.Service` into one listener →
route → EDS cluster → endpoint set per service. To source the inventory from
somewhere other than a file (a service registry, Kubernetes Endpoints/Services,
etc.), swap out `internal/config.Load` for your own loader and keep calling
`cache.SetSnapshot` on change.
