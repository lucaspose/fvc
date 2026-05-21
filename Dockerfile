# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/fvc ./cmd/fvc

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/fvcd ./cmd/fvcd

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/fvc-build-agent ./cmd/fvc-build-agent

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/fvc-init ./cmd/fvc-init

FROM scratch AS export
COPY --from=builder /out/fvc /fvc
COPY --from=builder /out/fvcd /fvcd
COPY --from=builder /out/fvc-build-agent /fvc-build-agent
COPY --from=builder /out/fvc-init /fvc-init

FROM debian:bookworm-slim AS firecracker

ARG TARGETARCH=amd64
ARG FIRECRACKER_VERSION=v1.15.1

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    tar \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) fc_arch="x86_64"; fc_sha256="d4a32ab2322d887ca1bc4a4e7afa9cc35393e6362dfc2b3becb389d362e4275a" ;; \
      arm64) fc_arch="aarch64"; fc_sha256="00654ac1e702a22744121ea9f10a4f792ebd7c3a744cba587dfac9fcb79b41a5" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    archive="firecracker-${FIRECRACKER_VERSION}-${fc_arch}.tgz"; \
    url="https://github.com/firecracker-microvm/firecracker/releases/download/${FIRECRACKER_VERSION}/${archive}"; \
    curl -fsSL "${url}" -o "/tmp/${archive}"; \
    echo "${fc_sha256}  /tmp/${archive}" | sha256sum -c -; \
    mkdir -p /tmp/firecracker; \
    tar -xzf "/tmp/${archive}" -C /tmp/firecracker --strip-components=1; \
    install -m 0755 "/tmp/firecracker/firecracker-${FIRECRACKER_VERSION}-${fc_arch}" /usr/local/bin/firecracker; \
    install -m 0755 "/tmp/firecracker/jailer-${FIRECRACKER_VERSION}-${fc_arch}" /usr/local/bin/jailer; \
    rm -rf "/tmp/${archive}" /tmp/firecracker

FROM debian:bookworm-slim AS final

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    iproute2 \
    iptables \
    procps \
    tini \
    util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/fvc /usr/bin/fvc
COPY --from=builder /out/fvcd /usr/bin/fvcd
COPY --from=builder /out/fvc-build-agent /usr/bin/fvc-build-agent
COPY --from=builder /out/fvc-init /usr/bin/fvc-init
COPY --from=firecracker /usr/local/bin/firecracker /usr/bin/firecracker
COPY --from=firecracker /usr/local/bin/jailer /usr/bin/jailer

RUN mkdir -p /var/lib/fvc /run/fvc

ENV FVC_HOME=/var/lib/fvc \
    FVC_RUNTIME_DIR=/run/fvc \
    FVC_GRPC_NETWORK=tcp \
    FVC_GRPC_ADDR=127.0.0.1:50051 \
    FVC_FIRECRACKER_PATH=/usr/bin/firecracker

EXPOSE 50051
VOLUME ["/var/lib/fvc"]
STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/bin/tini", "--", "fvcd"]

FROM debian:bookworm AS systemd-test

ENV container=docker

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    dbus \
    iproute2 \
    iptables \
    kmod \
    procps \
    systemd \
    systemd-sysv \
    util-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/fvc /usr/bin/fvc
COPY --from=builder /out/fvcd /usr/bin/fvcd
COPY --from=builder /out/fvc-build-agent /usr/bin/fvc-build-agent
COPY --from=builder /out/fvc-init /usr/bin/fvc-init
COPY --from=firecracker /usr/local/bin/firecracker /usr/bin/firecracker
COPY --from=firecracker /usr/local/bin/jailer /usr/bin/jailer
RUN mkdir -p /etc/fvc
COPY packaging/systemd/fvcd.service /etc/systemd/system/fvcd.service
COPY packaging/systemd/fvcd.env.example /etc/fvc/fvcd.env

RUN set -eux; \
    groupadd --system fvc; \
    sed -i 's/^FVC_RUNTIME_GROUP=.*/FVC_RUNTIME_GROUP=/' /etc/fvc/fvcd.env; \
    mkdir -p /var/lib/fvc /run/fvc; \
    chmod 0770 /run/fvc; \
    ln -s /etc/systemd/system/fvcd.service /etc/systemd/system/multi-user.target.wants/fvcd.service; \
    ln -sf /dev/null /etc/systemd/system/dev-hugepages.mount; \
    ln -sf /dev/null /etc/systemd/system/sys-fs-fuse-connections.mount; \
    ln -sf /dev/null /etc/systemd/system/systemd-logind.service; \
    ln -sf /dev/null /etc/systemd/system/getty.target; \
    ln -sf /dev/null /etc/systemd/system/console-getty.service

STOPSIGNAL SIGRTMIN+3
VOLUME ["/var/lib/fvc", "/sys/fs/cgroup"]
CMD ["/sbin/init"]
