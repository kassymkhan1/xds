.PHONY: build run tidy fmt vet

build:
	go build -o bin/xds-server ./cmd/xds-server

run:
	go run ./cmd/xds-server -debug

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...
