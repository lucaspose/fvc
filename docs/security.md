# Security Model

FVC is designed around a small trusted host daemon (`fvcd`) and isolated
Firecracker microVMs. The daemon still needs elevated host privileges for KVM,
TAP networking, loop mounts, and ext4 image preparation, so host-side file and
command handling must stay narrow.

## Host Daemon Boundary

- `fvcd` should run as a systemd service with `FVC_GRPC_NETWORK=unix` and a
  socket under `/run/fvc`.
- TCP gRPC listeners require `FVC_GRPC_TOKEN` by default. `FVC_ALLOW_INSECURE_TCP`
  exists only for isolated development environments.
- Runtime sockets and console files should be owned by the configured
  `FVC_RUNTIME_GROUP`.
- The packaged service limits filesystem writes to `/var/lib/fvc` and
  `/run/fvc`, hides home directories, enables a private `/tmp`, blocks realtime
  scheduling and SUID/SGID changes, and restricts system calls to the native
  architecture.
- The daemon keeps only the capabilities needed by the current runtime:
  `CAP_NET_ADMIN`, `CAP_SYS_ADMIN`, `CAP_SYS_RESOURCE`, and `CAP_DAC_OVERRIDE`.
- When `FVC_JAILER_ENABLED=true`, `fvcd` launches Firecracker through the
  Firecracker jailer. Each VM gets a per-VM chroot under
  `FVC_JAILER_CHROOT_BASE_DIR`; the daemon bind-mounts only the kernel, VM
  rootfs, and attached volume images into that chroot, then configures
  Firecracker with chroot-local paths.
- Files shared between VMs and only read by Firecracker (kernel, builder rootfs)
  stay owned by the daemon user and are exposed to the jailer through the group
  with read-only permissions, so a jailed process cannot modify or `chmod`
  them. Only per-VM writable files (VM drive, volumes) are handed to the jailer
  uid. Files are changed through an `O_NOFOLLOW` descriptor to defeat symlink
  swaps.
- The chroot base directory must be a real directory owned by the daemon user
  and not writable by group or others; `fvcd` refuses to start jails otherwise.
- A jailed Firecracker process is only adopted if its command line, real uid
  and `/proc/<pid>/root` all match the expected jail, so a look-alike process
  started by another user is ignored.
- Known limitation: all jailed VMs currently share `FVC_JAILER_UID` /
  `FVC_JAILER_GID`. A process escaping one jail could reach the drives of other
  VMs. Allocating a distinct uid/gid per VM, as recommended by Firecracker, is
  the next hardening step.

## Host Command Allowlist

Host command execution is limited to the commands required by the runtime:

- `ip`
- `iptables`
- `sysctl`
- `mount`
- `umount`
- `truncate`
- `mkfs.ext4`

Network setup keeps FVC-owned rules in dedicated `iptables` chains. The daemon
creates `FVC-PREROUTING`, `FVC-OUTPUT`, and `FVC-POSTROUTING` in the `nat`
table plus `FVC-FORWARD` in the filter table, then installs idempotent jumps
from the global host chains. VM-specific NAT, DNAT, SNAT, and forwarding entries
are added to those FVC chains instead of directly into `PREROUTING`, `OUTPUT`,
`POSTROUTING`, or `FORWARD`.
The jumps are appended, not inserted first, so existing host firewall rules
(policies, DROP rules) keep precedence over FVC rules.

The Firecracker, jailer, and runtime-init binaries are launched from configured
absolute paths. FVC refuses symlink paths and non-executable files for those
host binaries before VM startup.

## Image Integrity and Host Paths

Downloaded FVC images and kernels require HTTPS plus adjacent `.sha256` files by
default. The daemon verifies the digest before publishing the downloaded file
into the local cache. `FVC_ALLOW_INSECURE_DOWNLOADS=true` exists only for local
development mirrors.

`fvc image import` and `fvc image export` are restricted to paths under
`FVC_HOME` by default. Set `FVC_ALLOW_HOST_IMAGE_PATHS=true` only when the daemon
is running in a trusted single-user environment and arbitrary host filesystem
access is acceptable.

## Docker Image Conversion

Docker layers are treated as untrusted input. During conversion FVC:

- verifies registry blob digests when descriptors provide `sha256` digests;
- rejects layer paths that escape the temporary rootfs;
- refuses writes through symlink path components or symlink file targets;
- skips host device nodes and FIFOs from layers;
- applies Docker whiteouts within the rootfs boundary;
- enforces per-layer, per-file, rootfs expansion, and entry-count limits.

Docker images are converted to a normal local FVC ext4 image before they are
booted. After conversion, lifecycle commands operate on the local FVC image.

## Build Isolation

The default `[[run]]` build backend is `microvm`. Build commands run in a
temporary Firecracker builder VM through `fvc-build-agent`, not directly on the
host. The host-agent backend exists only for debugging and requires
`FVC_ALLOW_INSECURE_HOST_AGENT=true`. The build-agent HTTP server requires
`FVC_BUILD_AGENT_TOKEN` and rejects build requests without the matching
`X-FVC-Build-Token` header.
