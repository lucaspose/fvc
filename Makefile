IMAGE := fvc
BINARY := fvc
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")

.PHONY: build run clean

build:
	@docker build \
		--target export \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--output type=local,dest=. \
		.

clean:
	@rm -f $(BINARY)
