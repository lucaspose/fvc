# fvc

`fvc` is an early Firecracker microVM manager written in Go. The project currently provides a daemon (`fvcd`) and a CLI (`fvc`) that can run, list, stop, and read logs from microVMs through gRPC.

The current milestone is focused on making the foundation reliable before adding higher-level Docker-like features.

## Current Scope

- `fvc run`: starts a Firecracker microVM from an image reference or a `Vmfile`.
- `fvc ps`: lists known microVMs.
- `fvc stop`: stops a microVM by ID.
- `fvc start`: restarts a stopped microVM if its local drive still exists.
- `fvc rm`: removes a stopped microVM and its local files.
- `fvc console`: opens an interactive serial console to a running microVM.
- `fvc logs`: prints VM logs, with `--tail` and `--follow`.
- `fvcd`: persistent gRPC daemon with SQLite state.
- Image cache with validated image references and atomic downloads.
- Server-side validation for run requests.

Not implemented yet: networking, port publishing, exec, snapshots, stats, image build, and a real registry protocol.

## Development

Run the test suite:

```sh
make test
```

Build the local binaries through Docker:

```sh
make build
```

Build a runnable container image:

```sh
make docker-build
```

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
- `FVC_GRPC_ADDR`: gRPC bind address, defaults to `127.0.0.1:50051`.
- `FVC_FIRECRACKER_PATH`: Firecracker binary path, defaults to `/usr/local/bin/firecracker`.
- `FVC_KERNEL_PATH`: kernel path, defaults to `$FVC_HOME/vmlinux.bin`.
- `FVC_IMAGE_BASE_URL`: base URL for images and kernel downloads.
- `FVC_NETWORK_ENABLED`: automatic TAP/NAT networking, defaults to `true`.
- `FVC_RUNTIME_GROUP`: optional group that can access runtime logs and console FIFOs.

For local development, use a writable data directory:

```sh
FVC_HOME=/tmp/fvc-dev fvcd
```

## Images

`fvc run` expects two files to exist behind `FVC_IMAGE_BASE_URL`:

- `<image>.ext4`, for example `ubuntu.ext4`.
- `vmlinux.bin`, the Firecracker-compatible kernel.

With the default config, this command:

```sh
fvc run --image ubuntu
```

downloads:

```text
https://fvchubstorage.blob.core.windows.net/images/ubuntu.ext4
https://fvchubstorage.blob.core.windows.net/images/vmlinux.bin
```

If the image URL returns 404, either upload that image to the configured storage or point the daemon at another image host:

```sh
FVC_IMAGE_BASE_URL=http://127.0.0.1:8080 FVC_HOME=/tmp/fvc-dev fvcd
```

The daemon caches images under `FVC_HOME/cache` using encoded filenames, so prefer changing `FVC_IMAGE_BASE_URL` over manually writing cache files.

## Vmfile

Example:

```toml
[vm]
name = "api"
cpu = 2
ram = 1024

[image]
source = "ubuntu"
```

Run from a directory containing `Vmfile`:

```sh
fvc run .
```

## Lifecycle

Stop a running microVM:

```sh
fvc stop <vm-id>
```

Restart a stopped microVM:

```sh
fvc start <vm-id>
```

Remove a stopped microVM and its local files:

```sh
fvc rm <vm-id>
```

`fvc rm` refuses running microVMs. Stop the VM first, then remove it.

## Network

When networking is enabled, `fvcd` creates a TAP interface for each VM, assigns a small `/30` subnet, enables IPv4 forwarding, adds a NAT masquerade rule for the guest IP, and attaches the TAP to Firecracker before the VM starts.

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

If `fvcd` runs as root, console FIFOs and VM logs are created by root. To use `fvc console` without `sudo`, create a runtime group and run the daemon with `FVC_RUNTIME_GROUP`:

```sh
sudo groupadd -f fvc
sudo usermod -aG fvc lucas
```

Restart your shell session so the new group is active, then start the daemon:

```sh
sudo FVC_RUNTIME_GROUP=fvc FVC_HOME=/tmp/fvc-dev ./fvcd
```

The daemon will set group ownership and `0660` permissions on VM logs and console FIFOs. Your user can then run:

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

For machine-to-machine control, the long-term professional path is a small guest agent over Firecracker vsock. That future agent should own features like `fvc exec`, guest health checks, file copy, clean shutdown, and richer stats.

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
