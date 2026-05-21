#!/bin/sh
set -eu

CONTAINER="${FVC_TEST_CONTAINER:-fvcd}"
COMPOSE_FILE="${FVC_TEST_COMPOSE_FILE:-compose.firecracker.yml}"
BASE_IMAGE="${FVC_TEST_BASE_IMAGE:-dist/builder.ext4}"
BUILDER_ROOTFS="${FVC_TEST_BUILDER_ROOTFS:-dist/builder.ext4}"
GUEST_AGENT_MODE="${FVC_TEST_GUEST_AGENT_MODE:-auto}"
REBUILD_ARTIFACTS="${FVC_TEST_REBUILD_ARTIFACTS:-1}"
TEST_ID="${FVC_TEST_ID:-$(date +%s)}"
BASE_NAME="${FVC_TEST_BASE_NAME:-fvc-test-base-$TEST_ID}"
IMAGE_NAME="${FVC_TEST_IMAGE_NAME:-fvc-test-image-$TEST_ID}"
VM_NAME="${FVC_TEST_VM_NAME:-fvc-test-vm-$TEST_ID}"
PROJECT_DIR="/var/lib/fvc/functional-$TEST_ID"
REMOTE_BASE="/var/lib/fvc/$BASE_NAME.ext4"
REMOTE_BUILDER="/var/lib/fvc/builder-functional-$TEST_ID.ext4"
RESULTS_FILE="${TMPDIR:-/tmp}/fvc-functional-results-$TEST_ID.$$"
BUILD_LOG="${TMPDIR:-/tmp}/fvc-functional-build-$TEST_ID.$$.log"
CURRENT_STEP=""
FAILED_RECORDED=0

: > "$RESULTS_FILE"

log() {
  printf '[TEST] %s\n' "$*"
}

record_result() {
  printf '%s|%s|%s\n' "$1" "$2" "$3" >> "$RESULTS_FILE"
}

begin_step() {
  CURRENT_STEP="$1"
  log "$1"
}

pass_step() {
  record_result "PASS" "$1" "$2"
  CURRENT_STEP=""
}

skip_step() {
  record_result "SKIP" "$1" "$2"
  CURRENT_STEP=""
}

fail() {
  if [ "$FAILED_RECORDED" -eq 0 ]; then
    step="$CURRENT_STEP"
    if [ -z "$step" ]; then
      step="functional test"
    fi
    record_result "FAIL" "$step" "$*"
    FAILED_RECORDED=1
  fi
  printf '[ERR] %s\n' "$*" >&2
  exit 1
}

print_summary() {
  pass_count=0
  fail_count=0
  skip_count=0

  printf '\n'
  printf '[SUMMARY] FVC functional Docker test\n'
  printf '  test id:          %s\n' "$TEST_ID"
  printf '  container:        %s\n' "$CONTAINER"
  printf '  guest agent mode: %s\n' "$GUEST_AGENT_MODE"
  printf '  rebuild artifacts:%s\n' " $REBUILD_ARTIFACTS"
  printf '  base image:       %s\n' "$BASE_IMAGE"
  printf '  builder rootfs:   %s\n' "$BUILDER_ROOTFS"
  printf '\n'
  printf '[SUMMARY] Step results\n'

  while IFS='|' read -r line_status name detail; do
    [ -n "$line_status" ] || continue
    case "$line_status" in
      PASS) pass_count=$((pass_count + 1)) ;;
      FAIL) fail_count=$((fail_count + 1)) ;;
      SKIP) skip_count=$((skip_count + 1)) ;;
    esac
    printf '  [%s] %s' "$line_status" "$name"
    if [ -n "$detail" ]; then
      printf ' - %s' "$detail"
    fi
    printf '\n'
  done < "$RESULTS_FILE"

  printf '\n'
  printf '[SUMMARY] Totals: %s passed, %s failed, %s skipped\n' "$pass_count" "$fail_count" "$skip_count"
}

finish() {
  exit_code=$?
  trap - EXIT INT TERM
  if [ "$exit_code" -ne 0 ] && [ "$FAILED_RECORDED" -eq 0 ]; then
    step="$CURRENT_STEP"
    if [ -z "$step" ]; then
      step="functional test"
    fi
    record_result "FAIL" "$step" "command exited with status $exit_code"
    FAILED_RECORDED=1
  fi
  cleanup
  print_summary
  rm -f "$RESULTS_FILE" "$BUILD_LOG"
  exit "$exit_code"
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

docker_exec() {
  docker exec "$CONTAINER" "$@"
}

docker_exec_sh() {
  docker exec "$CONTAINER" /bin/sh -lc "$1"
}

docker_exec_fvc() {
  docker exec "$CONTAINER" /usr/bin/fvc "$@"
}

sync_binaries() {
  log "syncing local binaries into container"
  for bin in fvc fvcd fvc-build-agent fvc-init; do
    if [ ! -x "./$bin" ]; then
      fail "missing local binary ./$bin; run make build first"
    fi
    docker cp "./$bin" "$CONTAINER:/tmp/$bin"
  done
  docker_exec_sh '
    set -eu
    systemctl stop fvcd
    install -m 0755 /tmp/fvc /usr/bin/fvc
    install -m 0755 /tmp/fvcd /usr/bin/fvcd
    install -m 0755 /tmp/fvc-build-agent /usr/bin/fvc-build-agent
    install -m 0755 /tmp/fvc-init /usr/bin/fvc-init
    systemctl start fvcd
  '
}

prepare_loop_devices() {
  log "checking loop devices"
  if ! docker_exec_sh "grep -q '^ *7 loop$' /proc/devices"; then
    fail "loop block driver is not available in the container; run 'sudo modprobe loop' on the host and restart docker compose"
  fi
  docker_exec_sh '
    set -eu
    if [ ! -e /dev/loop-control ]; then
      mknod -m 660 /dev/loop-control c 10 237
    fi
    i=0
    while [ "$i" -lt 16 ]; do
      if [ ! -e "/dev/loop$i" ]; then
        mknod -m 660 "/dev/loop$i" b 7 "$i"
      fi
      i=$((i + 1))
    done
    losetup -f >/dev/null
  '
}

ensure_fvcd_service_caps() {
  log "checking fvcd service capabilities"
  docker_exec_sh '
    set -eu
    service=/etc/systemd/system/fvcd.service
    if ! grep -q "CAP_DAC_OVERRIDE" "$service"; then
      sed -i "s/^CapabilityBoundingSet=.*/CapabilityBoundingSet=CAP_NET_ADMIN CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_DAC_OVERRIDE/" "$service"
      sed -i "s/^AmbientCapabilities=.*/AmbientCapabilities=CAP_NET_ADMIN CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_DAC_OVERRIDE/" "$service"
      systemctl daemon-reload
      systemctl restart fvcd
    fi
  '
}

ensure_http_client() {
  log "checking HTTP client"
  if docker_exec_sh "command -v curl >/dev/null 2>&1"; then
    return 0
  fi
  docker_exec_sh '
    set -eu
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends curl
    rm -rf /var/lib/apt/lists/*
  '
}

cleanup() {
  set +e
  docker_exec_fvc stop "$VM_NAME" >/dev/null 2>&1
  docker_exec_fvc rm "$VM_NAME" >/dev/null 2>&1
  docker_exec_fvc image rm "$IMAGE_NAME" --force >/dev/null 2>&1
  docker_exec_fvc image rm "$BASE_NAME" --force >/dev/null 2>&1
  docker_exec_sh "rm -rf '$PROJECT_DIR' '$REMOTE_BASE' '$REMOTE_BUILDER'" >/dev/null 2>&1
}

wait_for_exec() {
  i=0
  while [ "$i" -lt 90 ]; do
    if docker_exec_fvc exec "$VM_NAME" -- /bin/sh -lc 'printf ok' 2>/tmp/fvc-functional-exec.err | grep -q ok; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  docker_exec_fvc logs "$VM_NAME" --tail 200 || true
  if [ -f /tmp/fvc-functional-exec.err ]; then
    cat /tmp/fvc-functional-exec.err >&2 || true
  fi
  fail "guest exec did not become ready"
}

wait_for_http() {
  i=0
  while [ "$i" -lt 60 ]; do
    if docker_exec_sh "curl -fsS --max-time 2 http://127.0.0.1/index.html" 2>/tmp/fvc-functional-http.err | grep -q 'hello from fvc functional test'; then
      return 0
    fi
    i=$((i + 1))
    sleep 1
  done
  if [ -f /tmp/fvc-functional-http.err ]; then
    cat /tmp/fvc-functional-http.err >&2 || true
  fi
  docker_exec_fvc inspect "$VM_NAME" >&2 || true
  docker_exec_fvc exec "$VM_NAME" -- /bin/sh -lc 'ps w; busybox wget -qO- http://127.0.0.1/index.html || true' >&2 || true
  docker_exec_sh "iptables -t nat -S | grep -E 'DNAT|SNAT|MASQUERADE' || true; iptables -S FORWARD || true" >&2 || true
  fail "published HTTP port did not serve the expected response"
}

trap finish EXIT INT TERM

begin_step "checking docker prerequisites"
need docker
pass_step "checking docker prerequisites" "docker is available"

begin_step "building local artifacts"
if [ "$REBUILD_ARTIFACTS" = "1" ]; then
  if ! make build builder-rootfs > "$BUILD_LOG" 2>&1; then
    cat "$BUILD_LOG" >&2 || true
    fail "local artifact rebuild failed"
  fi
  pass_step "building local artifacts" "local binaries and builder rootfs rebuilt"
else
  skip_step "building local artifacts" "FVC_TEST_REBUILD_ARTIFACTS=$REBUILD_ARTIFACTS"
fi

begin_step "checking rootfs inputs"
if [ ! -f "$BASE_IMAGE" ]; then
  fail "base image not found: $BASE_IMAGE; set FVC_TEST_BASE_IMAGE=/path/to/base.ext4 or run make builder-rootfs"
fi
if [ ! -f "$BUILDER_ROOTFS" ]; then
  fail "builder rootfs not found: $BUILDER_ROOTFS; set FVC_TEST_BUILDER_ROOTFS=/path/to/builder.ext4 or run make builder-rootfs"
fi
pass_step "checking rootfs inputs" "base and builder rootfs images are available"

begin_step "starting docker container"
if ! docker ps --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  docker compose -f "$COMPOSE_FILE" up -d --build
  pass_step "starting docker container" "started $CONTAINER from $COMPOSE_FILE"
else
  pass_step "starting docker container" "$CONTAINER is already running"
fi

begin_step "syncing local binaries"
sync_binaries
pass_step "syncing local binaries" "fvc, fvcd, fvc-build-agent and fvc-init installed in container"

begin_step "checking daemon"
docker_exec /usr/bin/systemctl is-active --quiet fvcd || fail "fvcd service is not active"
ensure_fvcd_service_caps
prepare_loop_devices
ensure_http_client
docker_exec_fvc doctor
pass_step "checking daemon" "fvcd is active, runtime diagnostics are ready, and curl is available"

begin_step "copying rootfs images"
docker cp "$BASE_IMAGE" "$CONTAINER:$REMOTE_BASE"
docker cp "$BUILDER_ROOTFS" "$CONTAINER:$REMOTE_BUILDER"
pass_step "copying rootfs images" "$REMOTE_BASE and $REMOTE_BUILDER copied"

begin_step "configuring fvcd build backend"
docker_exec_sh "sed -i '/^FVC_BUILDER_ROOTFS_PATH=/d;/^FVC_BUILD_BACKEND=/d;/^FVC_GUEST_AGENT_MODE=/d' /etc/fvc/fvcd.env && \
  printf '%s\n' \
    'FVC_BUILDER_ROOTFS_PATH=$REMOTE_BUILDER' \
    'FVC_BUILD_BACKEND=microvm' \
    'FVC_GUEST_AGENT_MODE=$GUEST_AGENT_MODE' >> /etc/fvc/fvcd.env"
docker_exec systemctl restart fvcd
sleep 1
docker_exec_fvc doctor
pass_step "configuring fvcd build backend" "microvm backend configured with guest agent mode $GUEST_AGENT_MODE"

begin_step "creating build context"
docker_exec_sh "rm -rf '$PROJECT_DIR' && mkdir -p '$PROJECT_DIR' && printf 'hello from fvc functional test\n' > '$PROJECT_DIR/index.html'"
docker_exec_sh "cat > '$PROJECT_DIR/Fvcfile' <<EOF
[image]
from = \"$BASE_NAME\"
tag = \"$IMAGE_NAME\"

[config]
workdir = \"/tmp/fvc-test\"
cmd = [\"/bin/sh\", \"-lc\", \"echo FVC_FUNCTIONAL_READY; exec busybox httpd -f -p 0.0.0.0:80 -h /tmp/fvc-test\"]
env = [\"FVC_FUNCTIONAL=1\"]
expose = [80]

[labels]
test = \"functional-docker\"

[[copy]]
src = \"index.html\"
dest = \"/tmp/fvc-test/index.html\"

[[run]]
command = \"test -f /tmp/fvc-test/index.html && printf built >/tmp/fvc-test/built.txt\"
workdir = \"/tmp/fvc-test\"
timeout_seconds = 60
EOF"
pass_step "creating build context" "$PROJECT_DIR contains Fvcfile and index.html"

begin_step "importing base image"
docker_exec_fvc image import "$REMOTE_BASE" "$BASE_NAME"
pass_step "importing base image" "$BASE_NAME imported"

begin_step "building image"
docker_exec_sh "cd '$PROJECT_DIR' && /usr/bin/fvc build -t '$IMAGE_NAME' ."
pass_step "building image" "$IMAGE_NAME built from $BASE_NAME"

begin_step "running VM"
docker_exec_fvc run --image "$IMAGE_NAME" --name "$VM_NAME" --publish-all
pass_step "running VM" "$VM_NAME started with published exposed ports"

begin_step "waiting for guest agent"
wait_for_exec
pass_step "waiting for guest agent" "guest exec endpoint is reachable"

begin_step "checking exec output"
EXEC_OUTPUT="$(docker_exec_fvc exec "$VM_NAME" -- /bin/sh -lc 'cat /tmp/fvc-test/index.html && cat /tmp/fvc-test/built.txt')"
printf '%s\n' "$EXEC_OUTPUT"
printf '%s\n' "$EXEC_OUTPUT" | grep -q 'hello from fvc functional test' || fail "missing copied file output"
printf '%s\n' "$EXEC_OUTPUT" | grep -q 'built' || fail "missing run-step output"
pass_step "checking exec output" "copy and run artifacts are visible inside the guest"

begin_step "checking published HTTP port"
wait_for_http
pass_step "checking published HTTP port" "http://127.0.0.1/index.html reached the microVM through published port 80"

begin_step "checking logs"
docker_exec_fvc logs "$VM_NAME" --tail 100 | tee /tmp/fvc-functional-logs.txt
grep -q 'FVC_FUNCTIONAL_READY' /tmp/fvc-functional-logs.txt || fail "readiness marker missing from logs"
pass_step "checking logs" "FVC_FUNCTIONAL_READY marker found"

begin_step "checking lifecycle"
docker_exec_fvc ps --all
docker_exec_fvc inspect "$VM_NAME"
docker_exec_fvc stop "$VM_NAME"
docker_exec_fvc rm "$VM_NAME"
pass_step "checking lifecycle" "ps, inspect, stop and rm succeeded"

cleanup
pass_step "cleanup" "temporary VM, images and rootfs copies removed"
log "functional Docker test passed"
