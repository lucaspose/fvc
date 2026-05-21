// Package buildengine applies FVC build plans to ext4 rootfs images.
//
// It owns filesystem-oriented build steps such as cloning the base image,
// mounting the target image, copying context files, and running the optional
// host-mounted build agent backend. Firecracker-backed build execution stays in
// the daemon because it depends on host networking and daemon runtime policy.
package buildengine
