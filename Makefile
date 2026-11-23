.PHONY: build run test tidy fmt vet proto

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/xds-server ./cmd/xds-server

run:
	go run ./cmd/xds-server -debug

test:
	go test -race ./...

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

# Regenerates pkg/api/pb from api/xds.proto. Needs buf, protoc-gen-go and
# protoc-gen-go-grpc on PATH:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
proto:
	buf generate
