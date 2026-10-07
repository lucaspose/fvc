# Development

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
