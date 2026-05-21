package main

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"
)

type Server struct {
	proto.UnimplementedFvcServiceServer
	DB     *sql.DB
	Config DaemonConfig
	Store  *ImageStore
	Net    *NetworkManager
	Runner CommandRunner
}

func validateListenConfig(cfg DaemonConfig) error {
	if cfg.GRPCNetwork != "tcp" {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("invalid tcp gRPC address %q: %w", cfg.GRPCAddr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		if !cfg.AllowRemoteTCP {
			return fmt.Errorf("refusing to listen on %s without FVC_ALLOW_REMOTE_TCP=true", cfg.GRPCAddr)
		}
	}
	return nil
}

func main() {
	cfg := LoadConfig()
	store := NewImageStore(cfg)
	network := NewNetworkManager(nil)
	if err := store.Init(); err != nil {
		log.Fatalf("Storage initialization error: %v", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		log.Fatalf("Log directory error: %v", err)
	}
	if err := applyRuntimePermissions(cfg.LogDir, cfg.RuntimeGroup, 0770); err != nil {
		log.Fatalf("Log directory permission error: %v", err)
	}
	if err := os.MkdirAll(cfg.RuntimeDir, 0770); err != nil {
		log.Fatalf("Runtime directory error: %v", err)
	}
	if err := applyRuntimePermissions(cfg.RuntimeDir, cfg.RuntimeGroup, 0770); err != nil {
		log.Fatalf("Runtime directory permission error: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("Db creation error: %v", err)
	}
	defer db.Close()
	if err := configureStateDB(db); err != nil {
		log.Fatalf("Db configuration error: %v", err)
	}
	if err := ensureSchema(db); err != nil {
		log.Fatalf("Table creation error: %v", err)
	}
	if err := reconcileState(db, network); err != nil {
		log.Fatalf("State reconciliation error: %v", err)
	}

	checks := RuntimeDiagnostics(cfg)
	for _, check := range checks {
		if check.OK {
			log.Printf("runtime check ok: %s (%s)", check.Name, check.Message)
			continue
		}
		log.Printf("runtime check warning: %s (%s)", check.Name, check.Message)
	}
	if cfg.StrictChecks {
		if err := RuntimeDiagnosticsError(checks); err != nil {
			log.Fatalf("Runtime diagnostics failed: %v", err)
		}
	}
	if err := validateListenConfig(cfg); err != nil {
		log.Fatalf("Listen configuration error: %v", err)
	}

	if cfg.GRPCNetwork == "unix" {
		_ = os.Remove(cfg.GRPCAddr)
	}
	listener, err := net.Listen(cfg.GRPCNetwork, cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("Network error: failed to listen on %s %s: %v", cfg.GRPCNetwork, cfg.GRPCAddr, err)
	}
	if cfg.GRPCNetwork == "unix" {
		if err := applyRuntimePermissions(cfg.GRPCAddr, cfg.RuntimeGroup, 0660); err != nil {
			log.Fatalf("gRPC socket permission error: %v", err)
		}
	}

	log.Printf("fvcd listening on %s %s with data dir %s", cfg.GRPCNetwork, cfg.GRPCAddr, cfg.BaseDir)
	grpcServer := grpc.NewServer()
	proto.RegisterFvcServiceServer(grpcServer, &Server{DB: db, Config: cfg, Store: store, Net: network})
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}
}
