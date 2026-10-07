# fvc

![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Firecracker](https://img.shields.io/badge/Firecracker-microVMs-FF9900)
![gRPC](https://img.shields.io/badge/API-gRPC-244c5a)
![License](https://img.shields.io/badge/license-AGPL--3.0-blue)

**fvc** runs [Firecracker](https://firecracker-microvm.github.io/) microVMs with the ergonomics of a container tool.
Each workload gets the isolation of a real virtual machine, but you build, run and manage it with commands that feel like Docker: `fvc build`, `fvc run`, `fvc ps`, `fvc exec`, `fvc logs`.

It is made of a daemon, **`fvcd`**, that owns Firecracker processes, images, networking and state (SQLite), and a CLI, **`fvc`**, that talks to it over gRPC.

---

## Features

- **Lifecycle** — `run`, `ps`, `inspect`, `stop`, `start`, `wait`, `kill`, `rm`, with live progress streamed from the daemon
- **Images** — local image cache with checksum-verified, atomic downloads; `fvc build` from a TOML `Fvcfile`; Docker Hub images converted into Firecracker-ready ext4 root filesystems (`fvc pull --from docker`)
- **Access** — interactive serial console (`fvc console`) and command execution through a guest agent over vsock (`fvc exec`)
- **Storage** — persistent named volumes and disk snapshots
- **Networking** — per-VM TAP device, NAT and published ports, isolated in dedicated `FVC-*` iptables chains
- **Observability** — `fvc logs --follow`, `fvc stats --watch`, `fvc doctor` to check host requirements
- **Hardening** — optional Firecracker jailer, unix-socket gRPC by default, token auth for TCP, strict validation of image references, paths and resources

---

## Requirements

- Linux with KVM (`/dev/kvm`) and `/dev/net/tun`
- [Firecracker](https://github.com/firecracker-microvm/firecracker/releases) (and optionally its `jailer`)
- Root privileges for `fvcd` (TAP devices, iptables, loop mounts)
- Go 1.26+ to build from source, or Docker to build with `make build`

---

## Quick start

```sh
# build fvc, fvcd, fvc-init and fvc-build-agent (uses Docker)
make build

# terminal 1: start the daemon with writable dev directories
sudo FVC_HOME=/tmp/fvc-dev FVC_RUNTIME_DIR=/tmp/fvc-run FVC_GRPC_ADDR=/tmp/fvc-run/fvcd.sock ./fvcd

# terminal 2: point the CLI at the daemon, check the host, then boot a VM
export FVC_GRPC_ADDR=/tmp/fvc-run/fvcd.sock
./fvc doctor
./fvc pull --from docker ubuntu:24.04 -t ubuntu-fvc:24.04
./fvc run --image ubuntu-fvc:24.04
./fvc ps
./fvc console <vm-id>
```

The CLI needs access to the daemon socket: run it as root, or see [Runtime Permissions](#runtime-permissions).

A small web example lives in [`examples/web`](examples/web): an `Fvcfile` that copies a page into an Ubuntu image and serves it, and a `Vmfile` for `fvc run`.

To install `fvc` and `fvcd` as a systemd service, see `make install-systemd`.

---

## Documentation

- [Architecture](docs/architecture.md) — components, packages and data flow
- [Security model](docs/security.md) — threat model, jailer, networking and gRPC access
- [Development](docs/development.md) — unit, end-to-end and functional test suites
- [History](docs/history.md) — what each development sprint delivered
- Usage reference below: [CLI output](#cli-output) · [Configuration](#configuration) · [Images](#images) · [Vmfile](#vmfile) · [Fvcfile](#fvcfile-build) · [Lifecycle](#lifecycle) · [Snapshots](#snapshots) · [Stats](#stats) · [Network](#network) · [Access](#access) · [Logs](#logs)

---

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

---

## License

Copyright (C) 2026 Lucas POSE

Licensed under the [GNU Affero General Public License v3.0](LICENSE).
You may use, modify and share this project, but any modified version, including
one offered as a network service, must be released under the same license.
