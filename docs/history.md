# Development history

What each development sprint delivered, oldest first.

## Sprint 1

- Shared validation for image references, VM names, CPU, and memory.
- Validation enforced in both CLI and daemon.
- Local image cache no longer uses raw user input as a filesystem path.
- Downloads are written to temporary files and published atomically.
- Unit tests cover validation, daemon run request checks, and storage path behavior.

## Sprint 2

- Daemon implements the `StreamLogs` gRPC endpoint.
- CLI exposes `fvc logs <id>`.
- `--tail` controls how many previous lines are printed.
- `--follow` streams new log lines until interrupted.
- Unit tests cover log tailing and VM log path lookup.

## Sprint 3

- Proto exposes `RunStream`.
- Daemon streams run progress events for image, kernel, rootfs, Firecracker, boot, drive, resources, and start.
- CLI displays real daemon progress instead of a generic spinner.
- Legacy unary `Run` remains available for compatibility.

## Sprint 4

- Stopped microVMs keep their local root filesystem.
- Daemon persists `drive_path` in SQLite.
- CLI exposes `fvc start <id>` and `fvc rm <id>`.
- `fvc rm` cleans the local drive, log file, and database row for stopped VMs.

## Sprint 5

- Daemon creates TAP networking automatically.
- Firecracker receives a network interface before VM start.
- Guest IP, TAP name, and MAC are deterministic per VM.
- `stop`, `start`, `rm`, and daemon reconciliation clean up network resources.
- Network setup is covered by tests through a fake command runner.

## Sprint 6

- `fvc ps` includes guest IP and TAP name.
- `fvc inspect <id>` displays status, PID, image, resources, network identity, and runtime paths.
- `Inspect` is exposed through the daemon API.

## Sprint 7

- `fvc pull <image>` preloads an image and kernel through the daemon.
- `fvc images` lists cached images with size and path.
- Image cache listing decodes the safe on-disk filenames back to image references.

## Sprint 8

- `fvc prune` cleans unused local files through the daemon.
- Image listing deduplicates legacy cache files and prefers the safe encoded cache format.
- Prune preserves running VM sockets, console FIFOs, and referenced active drives.

## Sprint 9

- `fvc stats` reports CPU, memory usage/limit, PID, status, and uptime.
- `fvc stats --watch` refreshes the metrics table.
- `fvc wait <id>` blocks until a VM is no longer running.
- `fvc kill <id>` force-stops a VM and cleans host-side runtime/network resources.
- `fvc prune --dry-run` previews cleanup, and `fvc prune --force` skips confirmation.

## Sprint 10

- `fvc snapshot create <id> <name>` copies a stopped VM drive into `FVC_HOME/snapshots`.
- `fvc snapshot ls <id>` lists snapshots with size and path.
- `fvc snapshot restore <id> <name>` restores a snapshot onto a stopped VM drive.
- `fvc snapshot rm <id> <name>` removes a snapshot.
- Snapshot names are validated and snapshot operations are covered by tests.

## Sprint 11

- `fvc build [-t image] [path]` builds a local image from a TOML `Fvcfile`.
- `Fvcfile` supports `[image].from`, `[image].tag`, and `[[copy]]`.
- Build contexts reject path traversal and require absolute guest destinations.
- Built images are published atomically into the local image cache.

## Sprint 12

- `fvc image inspect <image>` reports size, path, digest, source, labels, and creation time.
- `fvc image rm <image>` removes local images and protects images referenced by VMs.
- `fvc image tag <source> <target>` creates a local image tag with history metadata.
- `fvc image import <rootfs.ext4> <image>` imports a regular ext4 rootfs atomically.
- `fvc image export <image> <rootfs.ext4>` exports a cached image atomically.
- `fvc image history <image>` displays metadata history.
- `fvc image prune` removes unused local images with `--dry-run` and `--force`.
