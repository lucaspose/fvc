IMAGE := fvc
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
GOCACHE ?= /tmp/fvc-go-build

.PHONY: build docker-build proto test test-e2e test-docker-import test-functional-docker test-functional-docker-jailer builder-rootfs install-systemd clean

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

test-e2e:
	@FVC_E2E=1 GOCACHE=$(GOCACHE) go test ./test/e2e -v -timeout 15m

test-docker-import:
	@FVC_DOCKER_IMPORT_E2E=1 GOCACHE=$(GOCACHE) go test ./internal/dockerimport -run TestDockerImportFromDockerHubE2E -v -timeout 10m

test-functional-docker:
	@./test/functional/docker-functional.sh

test-functional-docker-jailer:
	@FVC_TEST_JAILER=1 ./test/functional/docker-functional.sh

builder-rootfs:
	@./packaging/builder/build-builder-rootfs.sh

install-systemd:
	@./packaging/install-systemd.sh

clean:
	@rm -f fvc fvcd fvc-build-agent fvc-init
