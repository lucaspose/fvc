#!/bin/sh
set -eu

OUT="${1:-dist/builder.ext4}"
SIZE_MB="${FVC_BUILDER_ROOTFS_SIZE_MB:-512}"
BASE_IMAGE="${FVC_BUILDER_BASE_IMAGE:-debian:bookworm-slim}"
WORKDIR="$(mktemp -d)"

cleanup() {
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

need docker
need mkfs.ext4
need tar
need truncate

REPO_ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
AGENT="$REPO_ROOT/fvc-build-agent"

if [ ! -x "$AGENT" ]; then
  if command -v go >/dev/null 2>&1; then
    (cd "$REPO_ROOT" && go build -o fvc-build-agent ./cmd/fvc-build-agent)
  else
    echo "fvc-build-agent is missing and go is unavailable" >&2
    exit 1
  fi
fi

ROOTFS="$WORKDIR/rootfs"
mkdir -p "$ROOTFS" "$(dirname "$OUT")"

cat > "$WORKDIR/Dockerfile" <<EOF
FROM ${BASE_IMAGE}
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \\
    busybox-static \\
    ca-certificates \\
    iproute2 \\
    mount \\
    util-linux \\
    && rm -rf /var/lib/apt/lists/*
EOF

docker build -q -t fvc-builder-rootfs:local "$WORKDIR" >/dev/null
container="$(docker create fvc-builder-rootfs:local /bin/true)"
docker export "$container" | tar -C "$ROOTFS" -xf -
docker rm "$container" >/dev/null

install -d -m 0755 "$ROOTFS/usr/local/bin" "$ROOTFS/mnt/fvc-target"
install -m 0755 "$AGENT" "$ROOTFS/usr/local/bin/fvc-build-agent"
install -m 0755 "$REPO_ROOT/packaging/builder/init" "$ROOTFS/init"

truncate -s "${SIZE_MB}M" "$OUT"
mkfs.ext4 -q -F -d "$ROOTFS" "$OUT"

echo "builder rootfs written to $OUT"
