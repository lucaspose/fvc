IMAGE := fvc
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")

.PHONY: build docker-build clean

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

clean:
	@rm -f fvc fvcd
