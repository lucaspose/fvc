// Package imagestore owns the on-disk Firecracker image cache.
//
// It stores ext4 root filesystems, sidecar image metadata, kernel downloads,
// and cache pruning helpers. Daemon services should use this package through a
// thin adapter and keep gRPC concerns outside of the store.
package imagestore
