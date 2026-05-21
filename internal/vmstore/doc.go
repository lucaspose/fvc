// Package vmstore owns the SQLite schema and persistence helpers for microVM
// state.
//
// The package intentionally avoids daemon runtime concerns such as process
// signaling and network device cleanup. Callers provide those checks as small
// callbacks when reconciling stale state.
package vmstore
