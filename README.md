# fvc

`fvc` is an early Firecracker microVM manager written in Go. The project currently provides a daemon (`fvcd`) and a CLI (`fvc`) that can run, list, stop, and read logs from microVMs through gRPC.

The current milestone is focused on making the foundation reliable before adding higher-level container workflows.

Additional docs:

- [Architecture](docs/architecture.md)
- [Security model](docs/security.md)

## Current Scope

- `fvc run`: starts a Firecracker microVM from an image reference or a `Vmfile`.
- `fvc build`: builds a local image from a TOML `Fvcfile`.
- `fvc pull`: downloads an image into the local cache.
- `fvc pull --from docker`: pulls a Docker Hub image, converts its layers into
  a Firecracker-ready ext4 rootfs, and imports it as a local FVC image.
- `fvc images`: lists locally cached images.
- `fvc image`: inspects, tags, imports, exports, removes, and prunes local images.
- `fvc volume`: creates, lists, inspects, removes, and prunes persistent named volumes.
- `fvc prune`: removes unused local resources left behind by older cache formats or interrupted runs.
- `fvc stats`: shows host-side CPU, memory usage/limit, PID, status, and uptime for microVMs.
- `fvc ps`: lists known microVMs.
- `fvc inspect`: prints detailed state for one microVM.
- `fvc stop`: stops a microVM by ID.
- `fvc start`: restarts a stopped microVM if its local drive still exists.
- `fvc wait`: waits until a microVM stops.
- `fvc kill`: force-kills a running microVM and cleans host resources.
- `fvc snapshot`: creates, lists, restores, and removes stopped microVM disk snapshots.
- `fvc rm`: removes a stopped microVM and its local files.
- `fvc console`: opens an interactive serial console to a running microVM.
- `fvc exec`: runs a command in a running runtime-managed microVM through the guest agent.
- `fvc logs`: prints VM logs, with `--tail` and `--follow`.
- `fvc doctor`: checks daemon connectivity and Firecracker host requirements.
- `fvcd`: persistent gRPC daemon with SQLite state.
- Image cache with validated image references and atomic downloads.
- Server-side validation for run requests.

Not implemented yet: a real registry protocol.

## Development

Run the test suite:

```sh
make test
```

GitHub Actions runs the same unit test suite, `go vet ./...`, and binary builds
on pushes to `main` and pull requests. A separate manual `KVM functional`
workflow targets self-hosted Linux runners labelled `kvm` so the Firecracker
Docker and jailer matrices can run only on hosts with `/dev/kvm`, `/dev/net/tun`,
Docker Compose, and the loop block driver available.

Run the Firecracker end-to-end suite on a Linux host with KVM:

```sh
make build
make builder-rootfs
sudo FVC_E2E=1 \
  FVC_E2E_BASE_IMAGE=/path/to/base.ext4 \
  FVC_E2E_KERNEL=/path/to/vmlinux.bin \
  make test-e2e
```

The e2e suite starts a temporary `fvcd`, imports the base image, builds an image
with `COPY` and `[[run]]`, boots it with Firecracker, waits for the guest agent,
runs `fvc exec`, checks logs, then stops and removes the VM. It skips by default
unless `FVC_E2E=1` is set, and it requires `/dev/kvm`, `/dev/net/tun`,
Firecracker, a compatible kernel, a compatible base ext4 image, and the builder
rootfs. Override binary and asset paths with `FVC_E2E_FVC`, `FVC_E2E_FVCD`,
`FVC_E2E_FIRECRACKER`, `FVC_E2E_BUILDER_ROOTFS`, and
`FVC_E2E_RUNTIME_INIT`.

Run the functional Docker smoke test against the `fvcd` compose container:

```sh
docker compose -f compose.firecracker.yml up -d --build
make test-functional-docker
```

The script rebuilds local binaries and the builder rootfs by default, copies
rootfs images into the container, configures the builder VM, creates a minimal
`Fvcfile`, imports a base image, builds with `COPY` and `[[run]]`, boots the VM,
runs `fvc exec`, verifies the published HTTP port, checks logs, prints a
`PASS`/`FAIL`/`SKIP` summary, and cleans up. By default it uses
`dist/builder.ext4` both as the builder rootfs and as a minimal test base image.
Use a real runtime rootfs with:

```sh
FVC_TEST_BASE_IMAGE=/path/to/base.ext4 make test-functional-docker
```

Use `FVC_TEST_GUEST_AGENT_MODE=vsock` to force the secure guest-agent path, or
`FVC_TEST_REBUILD_ARTIFACTS=0` to skip the rebuild when iterating on an existing
rootfs.

Run the same functional matrix through the Firecracker jailer:

```sh
make test-functional-docker-jailer
```

This sets `FVC_TEST_JAILER=1`, configures `fvcd` with `/usr/bin/jailer`, verifies
that the builder VM leaves no jailer chroot behind after `fvc build`, verifies
that runtime VMs create a Firecracker API socket inside the jailer chroot, and
checks final chroot cleanup after lifecycle, volume, and auto-remove paths.

Run the optional Docker Hub import test:

```sh
make test-docker-import
```

Pull and convert a Docker Hub image into a local Firecracker rootfs:

```sh
fvc pull --from docker ubuntu:24.04 -t ubuntu-fvc:24.04
fvc run --image ubuntu-fvc:24.04
```

Docker-style shortcuts are also supported:

```sh
fvc pull docker://nginx
fvc run --from docker nginx --name web --publish-all
fvc run docker://hello-world --rm
```

When a Docker image has no tag, FVC uses `:latest` for the local converted
image name, for example `nginx` becomes `nginx:latest`. `fvc run` accepts a
pull policy:

```sh
fvc run --from docker nginx --pull missing
fvc run --from docker nginx --pull always
fvc run --from docker nginx --pull never
```

`missing` is the default and reuses an existing converted image if present.
`always` refreshes the converted image unless that image is referenced by an
existing VM. `never` requires the converted image to already exist locally.

When `--from docker` is used, FVC treats the source as a normal Docker image,
downloads its OCI/Docker manifest and gzip layers from Docker Hub, applies
Docker whiteouts, creates an ext4 filesystem, imports it into the local image
cache, and stores Docker config metadata such as `Cmd`, `Env`, `WorkingDir`,
labels, and exposed TCP ports.

Build the local binaries through Docker:

```sh
make build
```

Build a runnable container image:

```sh
make docker-build
```

Run `fvcd` in Docker for local Firecracker testing:

```sh
docker compose -f compose.firecracker.yml up --build
```

The compose file builds the `systemd-test` image target, boots systemd inside
the container, enables `fvcd.service`, installs `fvc` and `fvcd` in `/usr/bin`,
persists daemon state in the `fvc-data` volume, mounts `/dev/kvm` and
`/dev/net/tun`, and grants the network and mount capabilities required by the
current runtime.

Run CLI checks inside that container:

```sh
docker exec -it fvcd /usr/bin/systemctl status fvcd --no-pager
docker exec -it fvcd /usr/bin/fvc doctor
docker exec -it fvcd /usr/bin/fvc run --image ubuntu --name test-vm --cpu 1 --ram 512
```

Manual Docker run equivalent:

```sh
docker run --rm -it \
  --name fvcd \
  --privileged \
  --cgroupns=host \
  --device /dev/kvm \
  --device /dev/net/tun \
  --sysctl net.ipv4.ip_forward=1 \
  -v fvc-data:/var/lib/fvc \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  --tmpfs /run \
  --tmpfs /run/lock \
  fvc:dev
```

Docker is useful for development and release testing. For a long-running host
install, `fvcd` is expected to run as a Linux daemon under systemd so it can
manage KVM, TAP devices, NAT rules, loop mounts, and persistent state directly
on the host.

Install the host daemon under systemd after building local binaries:

```sh
make build
sudo make install-systemd
sudo systemctl start fvcd
fvc doctor
```

The systemd unit listens on the Unix socket `/run/fvc/fvcd.sock` by default.
The CLI uses that socket automatically when it exists, and falls back to
`127.0.0.1:50051` for Docker/dev setups. Override the transport with
`FVC_GRPC_NETWORK=tcp FVC_GRPC_ADDR=127.0.0.1:50051` or
`FVC_GRPC_TARGET=unix:///run/fvc/fvcd.sock`.

Clean generated local binaries:

```sh
make clean
```

## CLI Output

Commands use a small, consistent output format:

- `[STAGE] ...` shows an operation currently in progress.
- `[OK] ...` confirms a successful operation.
- `[ERR] ...` is printed on failure.

Output uses ANSI colors by default. Disable colors with:

```sh
NO_COLOR=1 fvc ps
```

Long operations such as `fvc run` stream real progress events from the daemon while it prepares the image, kernel, root filesystem, Firecracker process, and VM state.

Example:

```text
[CONFIG] Resolving configuration
  image:   ubuntu
  cpu:     2
  ram:     1024 MB
[IMAGE] Resolving image ubuntu -
[OK] Image ready
[KERNEL] Resolving kernel -
[OK] Kernel ready
[ROOTFS] Cloning root filesystem -
[OK] Root filesystem ready
[NETWORK] Preparing network -
[OK] Network ready: fvc... 172.x.x.x
[FIRECRACKER] Launching Firecracker -
[OK] Firecracker API ready
[BOOT] Configuring boot source -
[OK] Boot source configured
[DRIVE] Attaching root filesystem -
[OK] Root filesystem attached
[NETIF] Attaching network interface -
[OK] Network interface attached
[RESOURCES] Configuring resources cpu=2 memory=1024MB -
[OK] Resources configured
[START] Starting microVM -
[OK] microVM started: <vm-id>
    status: running
```

While a step is running, the spinner stays on that same line. When the daemon reports completion, the line is cleared and replaced by the matching `[OK]` line.

## Configuration

`fvcd` reads configuration from environment variables:

- `FVC_HOME`: daemon data directory, defaults to `/var/lib/fvc`.
- `FVC_GRPC_NETWORK`: gRPC listen network, defaults to `unix`.
- `FVC_GRPC_ADDR`: gRPC bind address, defaults to `/run/fvc/fvcd.sock`.
- `FVC_ALLOW_REMOTE_TCP`: allow `fvcd` to bind TCP on all interfaces, defaults to `false`.
- `FVC_GRPC_TOKEN`: bearer token required by `fvcd` when set. TCP listeners
  require this token unless `FVC_ALLOW_INSECURE_TCP=true` is set for isolated
  development.
- `FVC_ALLOW_INSECURE_TCP`: allow TCP gRPC without `FVC_GRPC_TOKEN`, defaults
  to `false`.
- `FVC_FIRECRACKER_PATH`: Firecracker binary path, defaults to `/usr/local/bin/firecracker`.
- `FVC_JAILER_ENABLED`: launch Firecracker through the Firecracker jailer,
  defaults to `false` for local development.
- `FVC_JAILER_PATH`: jailer binary path, defaults to `/usr/local/bin/jailer`.
- `FVC_JAILER_CHROOT_BASE_DIR`: base directory for per-VM jail roots, defaults
  to `$FVC_HOME/jailer`.
- `FVC_JAILER_UID` / `FVC_JAILER_GID`: uid/gid used by jailed Firecracker
  processes, default to `65534`.
- `FVC_KERNEL_PATH`: kernel path, defaults to `$FVC_HOME/vmlinux.bin`.
- `FVC_IMAGE_BASE_URL`: base URL for images and kernel downloads.
- `FVC_REQUIRE_IMAGE_CHECKSUMS`: require adjacent `.sha256` files for downloaded
  FVC images and kernels, defaults to `true`.
- `FVC_ALLOW_INSECURE_DOWNLOADS`: allow non-HTTPS image/kernel downloads when
  checksum verification is enabled, defaults to `false`.
- `FVC_ALLOW_HOST_IMAGE_PATHS`: allow `fvc image import/export` to read or write
  host paths outside `FVC_HOME`, defaults to `false`.
- `FVC_NETWORK_ENABLED`: automatic TAP/NAT networking, defaults to `true`.
- `FVC_RUNTIME_DIR`: runtime socket and console FIFO directory, defaults to `/run/fvc`.
- `FVC_RUNTIME_GROUP`: optional group that can access runtime logs and console FIFOs.
- `FVC_RUNTIME_INIT_PATH`: host path to the static guest init injected into
  images with runtime metadata, defaults to `/usr/local/bin/fvc-init`.
- `FVC_RUNTIME_ROOT_DEVICE`: root block device passed to guest kernels,
  defaults to `/dev/vda`.
- `FVC_GUEST_AGENT_MODE`: guest agent transport, `vsock` by default. Use `auto`
  to fall back to TCP when no vsock path is present, or `tcp` for legacy
  network-only debugging.
- `FVC_STRICT_RUNTIME_CHECKS`: fail daemon startup if Firecracker host
  requirements are missing, defaults to `false`.

For local development, use a writable data directory:

```sh
FVC_HOME=/tmp/fvc-dev FVC_RUNTIME_DIR=/tmp/fvc-run FVC_GRPC_ADDR=/tmp/fvc-run/fvcd.sock fvcd
```

## Images

`fvc run` expects these files to exist behind `FVC_IMAGE_BASE_URL`:

- `<image>.ext4`, for example `ubuntu.ext4`.
- `<image>.ext4.sha256`, containing the expected SHA-256 digest.
- `vmlinux.bin`, the Firecracker-compatible kernel.
- `vmlinux.bin.sha256`, containing the expected SHA-256 digest.

With the default config, this command:

```sh
fvc run --image ubuntu
```

downloads:

```text
https://fvchubstorage.blob.core.windows.net/images/ubuntu.ext4
https://fvchubstorage.blob.core.windows.net/images/ubuntu.ext4.sha256
https://fvchubstorage.blob.core.windows.net/images/vmlinux.bin
https://fvchubstorage.blob.core.windows.net/images/vmlinux.bin.sha256
```

If the image URL returns 404, either upload that image to the configured storage or point the daemon at another image host:

```sh
FVC_ALLOW_INSECURE_DOWNLOADS=true FVC_IMAGE_BASE_URL=http://127.0.0.1:8080 FVC_HOME=/tmp/fvc-dev fvcd
```

The daemon caches images under `FVC_HOME/cache` using encoded filenames, so prefer changing `FVC_IMAGE_BASE_URL` over manually writing cache files.

Preload an image:

```sh
fvc pull ubuntu
```

List cached images:

```sh
fvc images
```

Inspect an image:

```sh
fvc image inspect ubuntu
```

Tag an image:

```sh
fvc image tag ubuntu ubuntu-copy
```

Import or export an ext4 root filesystem:

```sh
fvc image import ./rootfs.ext4 custom-image
fvc image export custom-image ./custom-image.ext4
```

Show image history metadata:

```sh
fvc image history custom-image
```

Remove an image:

```sh
fvc image rm custom-image
```

`fvc image rm` refuses to remove images referenced by existing microVMs unless `--force` is provided.

Prune unused images:

```sh
fvc image prune --dry-run
fvc image prune --force
```

Images keep sidecar metadata in the local cache. `inspect` computes a `sha256` digest from the ext4 file and shows metadata such as source and creation time.

Clean unused local resources:

```sh
fvc prune --dry-run
fvc prune --force
```

`fvc prune` keeps running VM runtime files and referenced VM drives. It removes legacy duplicate cache files, orphan active root filesystems, and stale runtime files from interrupted runs. Use `--dry-run` to preview every file, and `--force` to skip the confirmation prompt.

## Vmfile

Example:

```toml
[vm]
name = "api"
cpu = 2
ram = 1024
network = "nat"
ports = ["8080:80"]
volumes = ["data:/var/lib/api"]

[image]
source = "ubuntu"
```

Run from a directory containing `Vmfile`:

```sh
fvc run .
```

Override fields from the command line:

```sh
fvc run --image ubuntu --name api -p 8080:80 --cpu 2 --ram 1024
```

Run a short-lived microVM and remove its VM record, writable drive, log, console
input, and vsock file automatically after it exits:

```sh
fvc run --rm --image hello-fvc:latest --name hello
```

Attach a persistent named volume:

```sh
fvc volume create --size 1024 web-data
fvc run --image nginx-fvc:latest -v web-data:/usr/share/nginx/html
fvc run --image busybox-fvc:latest --network none -v cache:/cache:ro
```

FVC volumes are named ext4 disk images stored under the daemon data directory
and attached to Firecracker as secondary block devices. The guest runtime mounts
them at the requested absolute guest path before starting the image command.
The first run creates the volume automatically; later VMs using the same name
reuse the same data. Host bind mounts such as `/host/path:/guest/path` are not
supported yet.

Manage volumes explicitly:

```sh
fvc volume ls
fvc volume inspect web-data
fvc volume rm web-data
fvc volume prune --dry-run
fvc volume prune --force
```

`fvc volume rm` refuses volumes attached to a running microVM. It also refuses
volumes referenced by stopped VM records unless `--force` is used. `volume
prune` only removes volumes that are not referenced by any VM.

Commands that accept a VM identifier also accept the VM name or an unambiguous
ID prefix:

```sh
fvc logs api
fvc stop api
```

Additional VM commands are available for common lifecycle and guest operations:

```sh
fvc restart api
fvc rename api api-v2
fvc update --cpu 2 --ram 1024 api-v2
fvc top api-v2
fvc cp ./config.json api-v2:/etc/app/config.json
fvc cp api-v2:/var/log/app.log ./app.log
```

`fvc update` changes the resources used the next time a stopped microVM starts;
running microVMs must be stopped first. `fvc cp` currently supports regular
files and one VM endpoint per copy operation.

## Fvcfile Build

`Fvcfile` builds a reusable local image from an existing cached image. It uses TOML, like `Vmfile`.

Example:

```toml
[image]
from = "ubuntu"
tag = "ubuntu-web"

[config]
workdir = "/var/www/html"
cmd = ["/usr/bin/python3", "-m", "http.server", "80"]
env = ["PORT=80"]
expose = [80]

[labels]
app = "ubuntu-web"

[[copy]]
src = "index.html"
dest = "/var/www/html/index.html"

[[copy]]
src = "app"
dest = "/opt/app"
```

Build the image:

```sh
fvc build .
```

Override the tag from the CLI:

```sh
fvc build -t ubuntu-web .
```

Run the result:

```sh
fvc run --image ubuntu-web
```

Build contexts can include a `.fvcignore` file. It uses simple path/glob
patterns to skip files during `COPY`, for example `.git`, `*.db`, or `fvc`.

Image metadata from `[config]` and `[labels]` is stored with the image and shown
by:

```sh
fvc image inspect ubuntu-web
fvc image history ubuntu-web
```

Expose metadata does not publish host ports by itself. Publish ports explicitly
with `-p`, or publish every exposed port as `PORT:PORT`:

```sh
fvc run --image ubuntu-web --publish-all
```

The current build implementation supports `[image].from`, `[image].tag`, `-t`,
`[config]`, `[labels]`, `.fvcignore`, `[[copy]]`, and `[[run]]`. It mounts a
temporary clone of the base ext4 image, copies files from the build context,
unmounts it, optionally executes build commands through the isolated builder,
then publishes the final image into `FVC_HOME/cache`.

When an image has runtime metadata from `[config]`, `fvc run` injects the static
`fvc-init` binary and `/etc/fvc/runtime.json` into the cloned root filesystem
before Firecracker starts. The kernel receives `root=/dev/vda rw` and, for those
images, `init=/usr/local/bin/fvc-init`. Inside the guest, `fvc-init` mounts basic virtual
filesystems, applies the static IP from the kernel `ip=` argument, sets
environment variables and workdir, then starts and supervises the configured
command. When that command exits, `fvc-init` prints `FVC_EXIT_CODE=<code>` to
the serial log and powers off the guest cleanly.
It also starts a small authenticated guest agent used by `fvc exec`; the daemon
generates a per-VM token, stores it in SQLite, and passes it to the guest kernel
command line for that boot. By default, the daemon attaches a Firecracker vsock
device and the guest agent listens on AF_VSOCK port `9100`; TCP can still be
selected for legacy debugging with `FVC_GUEST_AGENT_MODE=tcp`.

Because the daemon mounts ext4 images, `fvcd` must run with mount privileges.
`[[run]]` build steps are delegated to `fvc-build-agent`. The default backend is
`microvm`: `fvcd` launches a temporary Firecracker builder VM, attaches the
image being built as a secondary drive, waits for `fvc-build-agent serve` inside
the guest, sends the build plan to `POST /build`, then shuts the builder down.

The builder VM must be provided by the operator for now:

```sh
make builder-rootfs
sudo install -m 0644 dist/builder.ext4 /var/lib/fvc/builder.ext4

FVC_BUILD_BACKEND=microvm \
FVC_BUILDER_ROOTFS_PATH=/var/lib/fvc/builder.ext4 \
FVC_BUILDER_KERNEL_PATH=/var/lib/fvc/vmlinux.bin \
fvc build .
```

The builder rootfs is expected to boot, configure the static IP provided in the
kernel args, and start:

```sh
fvc-build-agent serve --addr :9090
```

Optional builder settings:

- `FVC_BUILDER_AGENT_PORT`: defaults to `9090`.
- `FVC_BUILDER_CPUS`: defaults to `1`.
- `FVC_BUILDER_MEMORY_MB`: defaults to `512`.
- `FVC_BUILDER_TARGET_DEVICE`: defaults to `/dev/vdb`.
- `FVC_BUILDER_TARGET_ROOT`: defaults to `/mnt/fvc-target`.
- `FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS`: daemon timeout for the builder
  `/build` request, defaults to `3600`.
- `FVC_BUILDER_ROOTFS_SIZE_MB`: rootfs generator size, defaults to `512`.
- `FVC_BUILDER_BASE_IMAGE`: rootfs generator base image, defaults to
  `debian:bookworm-slim`.
- `FVC_BUILDER_BOOT_ARGS_EXTRA`: appended to the builder kernel boot args.

`make builder-rootfs` creates a Debian-based Firecracker builder image with
`/init`, `busybox`, `iproute2`, mount tools, and `fvc-build-agent`. The init
script mounts `/proc`, `/sys`, `/dev`, configures the interface from the `ip=`
kernel arg, creates `/mnt/fvc-target`, and starts the agent on port `9090`.

For local development experiments without Firecracker, an explicit host agent
backend can be selected:

```sh
FVC_BUILD_BACKEND=agent \
FVC_ALLOW_INSECURE_HOST_AGENT=true \
FVC_BUILD_AGENT_PATH=/usr/local/bin/fvc-build-agent \
fvc build .
```

The host agent backend reads the same JSON build plan from stdin, runs commands
from the host with the mounted root path as its working tree, and streams
command output. It is intentionally guarded by
`FVC_ALLOW_INSECURE_HOST_AGENT=true` because it is useful for debugging the
agent protocol, not as the production isolation boundary.

## Lifecycle

`fvcd` keeps VM state in SQLite and refreshes it when lifecycle/query commands
run. If a runtime-enabled guest prints `FVC_EXIT_CODE=<code>` and powers off,
the daemon records the VM as `exited`, stores the exit code, cleans network and
runtime sockets, and stops the Firecracker process if needed. If the host
process disappears without a guest exit code, the VM becomes `stopped`.

Stop a running microVM:

```sh
fvc stop <vm-id>
```

Restart a stopped microVM:

```sh
fvc start <vm-id>
```

Wait until a microVM stops:

```sh
fvc wait <vm-id>
fvc wait --timeout 30 <vm-id>
```

Force a running microVM to stop:

```sh
fvc kill <vm-id>
```

Remove a stopped microVM and its local files:

```sh
fvc rm <vm-id>
```

`fvc rm` refuses running microVMs. Stop the VM first, then remove it. For
one-shot workloads, prefer `fvc run --rm ...`; it performs the same local
cleanup automatically after the VM reaches `exited` or `stopped`.

## Snapshots

Create a snapshot from a stopped microVM:

```sh
fvc snapshot create <vm-id> before-upgrade
```

List snapshots:

```sh
fvc snapshot ls <vm-id>
```

Restore a snapshot onto a stopped microVM:

```sh
fvc snapshot restore <vm-id> before-upgrade
```

Remove a snapshot:

```sh
fvc snapshot rm <vm-id> before-upgrade
```

Snapshots live under `FVC_HOME/snapshots/<vm-id>/`. Snapshot create and restore refuse running microVMs so the disk copy stays consistent.

## Stats

Show metrics for running microVMs:

```sh
fvc stats
```

Show one VM:

```sh
fvc stats <vm-id>
```

Refresh continuously:

```sh
fvc stats --watch
```

The daemon reads host process metrics from `/proc`, so CPU and memory usage describe the Firecracker process for each VM.

## Network

When networking is enabled, `fvcd` creates a TAP interface for each VM, assigns a small `/30` subnet, enables IPv4 forwarding, and attaches the TAP to Firecracker before the VM starts. Host firewall rules are isolated behind dedicated `iptables` chains:

- `FVC-PREROUTING`, jumped from `PREROUTING` in the `nat` table.
- `FVC-OUTPUT`, jumped from `OUTPUT` in the `nat` table.
- `FVC-POSTROUTING`, jumped from `POSTROUTING` in the `nat` table.
- `FVC-FORWARD`, jumped from the host `FORWARD` chain.

Per-VM masquerade, published-port DNAT/SNAT, and forwarding rules live in those
dedicated chains so FVC-owned rules are easier to inspect and clean without
mixing them directly into the global host chains.

Each VM can choose its network mode at start time:

```sh
fvc run --image nginx-fvc:latest --network nat --publish-all
fvc run --image hello-fvc:latest --network none --rm
```

- `nat` is the default when `FVC_NETWORK_ENABLED=true`. It creates the TAP device, assigns the guest IP, enables outbound NAT, and allows `-p` or `--publish-all`.
- `none` starts the microVM without a Firecracker network interface. Port publishing is rejected in this mode.
- When `FVC_NETWORK_ENABLED=false`, the default mode becomes `none`. Explicit `--network nat` requires the daemon network support to be enabled.

The daemon generates per-VM values:

- TAP name: `fvc<short-hash>`
- guest interface: `eth0`
- deterministic guest MAC
- deterministic guest IP

This requires the daemon to run with enough privileges to create TAP devices and manage `iptables`. Disable automatic networking for local smoke tests with:

```sh
FVC_NETWORK_ENABLED=false FVC_HOME=/tmp/fvc-dev fvcd
```

If you see `ioctl(TUNSETIFF): Operation not permitted`, restart the daemon with the required privileges. Running only the CLI with `sudo` is not enough because `fvcd` creates the TAP device:

```sh
sudo FVC_HOME=/tmp/fvc-dev ./fvcd
```

Also make sure the host exposes TUN/TAP:

```sh
ls -l /dev/net/tun
sudo modprobe tun
```

## Runtime Permissions

If `fvcd` runs as root, runtime files and VM logs are created by root. To use `fvc console` without `sudo`, create a runtime group and run the daemon with `FVC_RUNTIME_GROUP`:

```sh
sudo groupadd -f fvc
sudo usermod -aG fvc lucas
```

Restart your shell session so the new group is active, then start the daemon:

```sh
sudo FVC_RUNTIME_GROUP=fvc FVC_HOME=/tmp/fvc-dev ./fvcd
```

The daemon will set group ownership on `/run/fvc`, VM logs, and console FIFOs. Runtime directories use `0770`; VM logs and console FIFOs use `0660`. Your user can then run:

```sh
./fvc console <vm-id>
```

The guest rootfs must support kernel IP configuration through boot arguments for the assigned IP to appear automatically inside the VM.

## Access

The default human access path is the Firecracker serial console. It does not require SSH keys, passwords, or modifying the rootfs image.

Attach to a running microVM:

```sh
fvc console <vm-id>
```

Exit with `Ctrl-C`.

The console sends your keyboard input to Firecracker stdin and streams the VM serial output from the VM log. This is the best fit for Firecracker example images.

While attached, `fvc console` puts your local terminal in a no-echo interactive mode so commands are not printed twice. The terminal settings are restored when the command exits.

You can still use SSH manually if the guest image has `openssh-server`, `sshd` running, and credentials configured:

```sh
ssh root@<guest-ip>
```

Get the guest IP with:

```sh
fvc ps --all
```

Inspect all stored state for one VM:

```sh
fvc inspect <vm-id>
```

For machine-to-machine control, runtime-managed images include a small guest
agent. The current agent owns `fvc exec` over Firecracker vsock; future versions
should expand the same protocol to guest health checks, file copy, clean
shutdown, and richer stats.
Run a non-interactive command in a running runtime-managed microVM:

```sh
fvc exec <vm-id-or-name> -- uname -a
fvc exec -w /tmp -e HELLO=world <vm-id-or-name> -- sh -lc 'echo "$HELLO"'
```

`fvc exec` streams guest stdout to local stdout and guest stderr to local
stderr. The CLI exits with the same code as the guest command, so it can be used
from scripts and CI jobs.

Current `exec` requirements:

- the VM must be running;
- the image must use `[config]` runtime metadata so `fvc-init` is injected;
- the guest kernel must support virtio-vsock for the default transport;
- requests require the per-VM bearer token generated by `fvcd`.

The default transport is authenticated HTTP over Firecracker vsock. For older
test images that do not expose `/dev/vsock`, use `FVC_GUEST_AGENT_MODE=auto` or
`FVC_GUEST_AGENT_MODE=tcp`; TCP mode requires VM networking.

## Logs

Print the last 100 lines:

```sh
fvc logs <vm-id>
```

Print a specific number of lines:

```sh
fvc logs --tail 20 <vm-id>
```

Follow new log lines:

```sh
fvc logs --follow <vm-id>
```

Combine flags:

```sh
fvc logs --tail 20 --follow <vm-id>
```

## Sprint 1 Done

- Shared validation for image references, VM names, CPU, and memory.
- Validation enforced in both CLI and daemon.
- Local image cache no longer uses raw user input as a filesystem path.
- Downloads are written to temporary files and published atomically.
- Unit tests cover validation, daemon run request checks, and storage path behavior.

## Sprint 2 Done

- Daemon implements the `StreamLogs` gRPC endpoint.
- CLI exposes `fvc logs <id>`.
- `--tail` controls how many previous lines are printed.
- `--follow` streams new log lines until interrupted.
- Unit tests cover log tailing and VM log path lookup.

## Sprint 3 Done

- Proto exposes `RunStream`.
- Daemon streams run progress events for image, kernel, rootfs, Firecracker, boot, drive, resources, and start.
- CLI displays real daemon progress instead of a generic spinner.
- Legacy unary `Run` remains available for compatibility.

## Sprint 4 Done

- Stopped microVMs keep their local root filesystem.
- Daemon persists `drive_path` in SQLite.
- CLI exposes `fvc start <id>` and `fvc rm <id>`.
- `fvc rm` cleans the local drive, log file, and database row for stopped VMs.

## Sprint 5 Done

- Daemon creates TAP networking automatically.
- Firecracker receives a network interface before VM start.
- Guest IP, TAP name, and MAC are deterministic per VM.
- `stop`, `start`, `rm`, and daemon reconciliation clean up network resources.
- Network setup is covered by tests through a fake command runner.

## Sprint 6 Done

- `fvc ps` includes guest IP and TAP name.
- `fvc inspect <id>` displays status, PID, image, resources, network identity, and runtime paths.
- `Inspect` is exposed through the daemon API.

## Sprint 7 Done

- `fvc pull <image>` preloads an image and kernel through the daemon.
- `fvc images` lists cached images with size and path.
- Image cache listing decodes the safe on-disk filenames back to image references.

## Sprint 8 Done

- `fvc prune` cleans unused local files through the daemon.
- Image listing deduplicates legacy cache files and prefers the safe encoded cache format.
- Prune preserves running VM sockets, console FIFOs, and referenced active drives.

## Sprint 9 Done

- `fvc stats` reports CPU, memory usage/limit, PID, status, and uptime.
- `fvc stats --watch` refreshes the metrics table.
- `fvc wait <id>` blocks until a VM is no longer running.
- `fvc kill <id>` force-stops a VM and cleans host-side runtime/network resources.
- `fvc prune --dry-run` previews cleanup, and `fvc prune --force` skips confirmation.

## Sprint 10 Done

- `fvc snapshot create <id> <name>` copies a stopped VM drive into `FVC_HOME/snapshots`.
- `fvc snapshot ls <id>` lists snapshots with size and path.
- `fvc snapshot restore <id> <name>` restores a snapshot onto a stopped VM drive.
- `fvc snapshot rm <id> <name>` removes a snapshot.
- Snapshot names are validated and snapshot operations are covered by tests.

## Sprint 11 Done

- `fvc build [-t image] [path]` builds a local image from a TOML `Fvcfile`.
- `Fvcfile` supports `[image].from`, `[image].tag`, and `[[copy]]`.
- Build contexts reject path traversal and require absolute guest destinations.
- Built images are published atomically into the local image cache.

## Sprint 12 Done

- `fvc image inspect <image>` reports size, path, digest, source, labels, and creation time.
- `fvc image rm <image>` removes local images and protects images referenced by VMs.
- `fvc image tag <source> <target>` creates a local image tag with history metadata.
- `fvc image import <rootfs.ext4> <image>` imports a regular ext4 rootfs atomically.
- `fvc image export <image> <rootfs.ext4>` exports a cached image atomically.
- `fvc image history <image>` displays metadata history.
- `fvc image prune` removes unused local images with `--dry-run` and `--force`.
