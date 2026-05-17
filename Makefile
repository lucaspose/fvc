IMAGE := fvc
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
GOCACHE ?= /tmp/fvc-go-build

.PHONY: build docker-build proto test clean

build:
	@docker build \
		--target export \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--output type=local,dest=. \
		.

docker-build:
	@docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-t $(IMAGE):$(VERSION) \
		.

proto:
	@protoc --go_out=. --go-grpc_out=. proto/fvc.proto

test:
	@GOCACHE=$(GOCACHE) go test ./...

clean:
	@rm -f fvc fvcd
