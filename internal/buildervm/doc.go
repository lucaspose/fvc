// Package buildervm runs isolated build steps inside a Firecracker microVM.
//
// It is the Firecracker-backed [[run]] backend for the build engine. Host
// network allocation is injected by the daemon, while Firecracker API setup and
// build-agent communication live here.
package buildervm
