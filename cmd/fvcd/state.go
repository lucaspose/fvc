package main

import (
	"database/sql"

	"github.com/lucaspose/fvc/internal/vmstore"
)

func ensureSchema(db *sql.DB) error {
	return vmstore.EnsureSchema(db)
}

func configureStateDB(db *sql.DB) error {
	return vmstore.ConfigureDB(db)
}

func reconcileState(db *sql.DB, network *NetworkManager) error {
	return vmstore.Reconcile(db, processMatches, func(vm vmstore.StaleVM) {
		if network == nil {
			return
		}
		cfg := NetworkConfig{TapName: vm.TapName, GuestIP: vm.GuestIP, MAC: vm.MAC}
		_ = network.CleanupPublishedPorts(cfg, splitStoredPorts(vm.PortsValue))
		_ = network.Cleanup(cfg)
	})
}
