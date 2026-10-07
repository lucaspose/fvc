# FVC Architecture

FVC is split into four binaries:

- `fvc`: user-facing CLI.
- `fvcd`: long-running daemon that owns VM state, image storage, networking, and Firecracker processes.
- `fvc-init`: minimal guest init injected into runtime-managed root filesystems.
- `fvc-build-agent`: guest-side build executor used by isolated `[[run]]` build steps.

## CLI Layout

The user-facing CLI lives in `cmd/fvc` and is grouped by command surface:

- `main.go`: command registration, usage text, and the injectable `runCLI` dispatch path used by tests.
- `client.go`: gRPC target resolution and daemon reachability checks.
- `workflows.go`: high-level `run`, `build`, and `pull` workflows with streamed progress output.
- `image.go`: image subcommands and image detail rendering.
- `snapshot.go`: snapshot subcommands.
- `vm_lifecycle.go`: VM state-changing commands such as `stop`, `start`, `restart`, `rm`, `kill`, `wait`, `rename`, and `update`.
- `vm_query.go`: VM read-only commands and renderers for `ps`, `inspect`, and `stats`.
- `cp.go`: host-to-guest and guest-to-host file copy command handling.
- `guest_process.go`: guest process inspection command handling.
- `guest_exec_stream.go`: shared guest exec streaming used by commands that need process output.
- `maintenance.go`: top-level image listing and local prune commands.
- `exec.go`, `logs.go`, `console.go`, `doctor.go`: focused command handlers.
- `vmfile.go`, `flags.go`, `prompt.go`: shared CLI parsing and prompt helpers.

The CLI command handlers intentionally stay in the `cmd/fvc` package because it
is the executable `main` package. Cross-command behavior that is not tied to
argument parsing or gRPC calls should move to `internal/<domain>` packages
instead of creating broad helper packages under `cmd`.

## Daemon Layout

The daemon code currently lives in `cmd/fvcd` and is grouped by responsibility:

- `main.go`: process wiring, configuration, storage initialization, gRPC startup.
- `run_service.go`: `Run`/`RunStream` request validation and microVM start orchestration.
- `lifecycle_service.go`: VM lifecycle gRPC adapters, host process signaling, runtime file cleanup, and Firecracker restart orchestration.
- `firecracker.go`: Firecracker API helpers, socket waiting, boot arguments, and network attachment payloads.
- `firecracker_runtime.go`: Firecracker process launch and machine configuration.
- `vm_query_service.go`: `ps`, `inspect`, `stats`, and `wait` gRPC adapters plus host process metrics.
- `pull_prune_service.go`: daemon pull flow, Docker import dispatch, image listing, and local prune operations.
- `image_service.go`: image inspect/tag/import/export/remove/prune/history adapters and VM/image usage checks.
- `snapshot_service.go`: stopped-VM snapshot create/list/restore/remove adapters and snapshot drive path resolution.
- `volumes.go`: named ext4 volume creation, inspection, usage tracking, protected remove, and prune.
- `proto_mapping.go`: daemon-owned conversion from image and snapshot domain structs to protobuf details.
- `logs_service.go`: log streaming, tail/follow helpers, and daemon diagnostics RPC.
- `state.go`: thin adapter around VM state persistence and startup reconciliation.

The daemon package still owns host state and policy. Code that can be isolated without knowing about SQLite, gRPC, or daemon configuration is moved into `internal`.

## Internal Packages

- `internal/dockerimport`: Docker Hub/OCI image conversion. It resolves manifests, downloads and verifies layers, applies tar whiteouts safely, builds an ext4 root filesystem, and returns portable image metadata. It does not write into the FVC image cache directly; `cmd/fvcd` decides how to import the result.
- `internal/fcapi`: Firecracker API Unix-socket client helpers for boot source, drives, network interfaces, machine config, instance start, and socket readiness checks.
- `internal/fcvsock`: Firecracker vsock payload/CID helpers and host-side vsock dialer used by guest-agent communication.
- `internal/buildplan`: Fvcfile and .fvcignore parsing plus build-plan validation. It owns guest path validation, copy source safety, runtime metadata validation, and run-step normalization.
- `internal/buildagent`: JSON payload and HTTP client helpers for `fvc-build-agent`, including health checks, token header handling, and timeout-aware build requests.
- `internal/buildengine`: build-plan execution against ext4 rootfs images: base image cloning, mounted copy steps, local-agent backend dispatch, backend abstraction, and image publication.
- `internal/buildervm`: Firecracker-backed build backend. It launches the builder microVM, attaches the target image, configures Firecracker, waits for the builder agent, and posts the build plan. The daemon injects host network setup/cleanup.
- `internal/cliui`: reusable terminal rendering for the CLI, including colored status labels, key/value rows, byte/duration formatting, and streamed progress lines.
- `internal/guestruntime`: guest runtime config, random seed generation, and mounted-rootfs installation of `fvc-init` plus `/etc/fvc/runtime.json`.
- `internal/hostprune`: host filesystem cleanup for orphan active drives and stale runtime files based on daemon SQLite state. The daemon `Prune` RPC adapts these package results into protobuf output.
- `internal/jailfs`: jailer filesystem helpers shared by the daemon and the builder: chroot base directory checks, file ownership for bind-mounted files, and identification of jailed Firecracker processes.
- `internal/hostnet`: host TAP/NAT/port publishing setup, deterministic guest IP/MAC derivation, network cleanup, and permission-hint wrapping.
- `internal/imagestore`: on-disk Firecracker image cache, image metadata sidecars, kernel downloads, VM drive snapshots, and image/cache pruning helpers.
- `internal/rootfs`: mounted-rootfs file operations with symlink-safe path creation and no-follow writes used by build copies and runtime init injection.
- `internal/storeio`: atomic file transfer primitives shared by image cache, snapshot, build, and kernel download paths.
- `internal/vmstore`: SQLite VM schema setup, DB connection policy, VM reference/name lookup, VM detail/stats queries, snapshot preflight reads, lifecycle reads/mutations, port-list decoding, and stale VM reconciliation.
- `internal`: shared validation and response helpers that are still used by both CLI and daemon code.

The next extraction candidates are deeper command-specific tests around the split CLI handlers and any daemon helper that can move without taking SQLite, gRPC, or host-resource policy with it.

## Refactor Rules

- Do not mix package moves with behavioral changes.
- Keep tests green after each small move.
- Prefer small internal packages with clear ownership over broad utility packages.
- Keep CLI command code focused on parsing and gRPC calls; shared terminal rendering belongs in `internal/cliui`.
- Keep daemon code responsible for host resources: SQLite, Firecracker, TAP/NAT, mounts, and runtime files.

## Test Layout

Tests should follow the same ownership boundaries as the production files:

- CLI handler coverage lives in `cmd/fvc/command_handlers_test.go`, with reusable fake gRPC clients and streams in `cmd/fvc/command_test_helpers_test.go`.
- Daemon RPC and helper tests live next to their service area: `run_service_test.go`, `vm_query_service_test.go`, `logs_service_test.go`, `snapshot_service_test.go`, `image_service_test.go`, `prune_service_test.go`, and state/schema checks in `state_schema_test.go`.
- Package-level internals use package-specific tests under `internal/<package>`.

## Validation

After each refactor step:

```sh
GOCACHE=/tmp/fvc-go-build go test ./...
GOCACHE=/tmp/fvc-go-build go vet ./...
GOCACHE=/tmp/fvc-go-build go build ./cmd/fvc ./cmd/fvcd ./cmd/fvc-init ./cmd/fvc-build-agent
```

After behavior-affecting changes, also run the functional Docker suite.
