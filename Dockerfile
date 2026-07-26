# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/xds-server ./cmd/xds-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/xds-server /usr/local/bin/xds-server
COPY --from=builder /src/configs /etc/xds/configs

EXPOSE 18000
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/xds-server"]
CMD ["-port=18000", \
     "-config-cds=/etc/xds/configs/cds.yaml", \
     "-config-eds=/etc/xds/configs/eds.yaml", \
     "-config-lds=/etc/xds/configs/lds.yaml", \
     "-config-rds=/etc/xds/configs/rds.yaml", \
     "-config-sds=/etc/xds/configs/sds.yaml"]