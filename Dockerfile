# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.buildstamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o /out/xds-server ./cmd/xds-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/xds-server /usr/local/bin/xds-server
COPY --from=builder /src/configs /etc/xds/configs

# 18000: xDS (ADS) + gRPC management API, 2827: HTTP management API
EXPOSE 18000 2827
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/xds-server"]
CMD ["-grpc-addr=:18000", \
     "-http-addr=:2827", \
     "-config-cds=/etc/xds/configs/cds.yaml", \
     "-config-eds=/etc/xds/configs/eds.yaml", \
     "-config-lds=/etc/xds/configs/lds.yaml", \
     "-config-rds=/etc/xds/configs/rds.yaml", \
     "-config-sds=/etc/xds/configs/sds.yaml"]
