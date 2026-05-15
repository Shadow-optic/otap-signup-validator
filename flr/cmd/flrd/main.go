// flrd: the Federated Lambda Registry daemon. Brings up a single FLR node
// (BadgerStore + crypto + registry engine + federation manager + xlat manager
// + API server) and runs until terminated.
//
// Minimal feature set for the integrated OTAP demo:
//   - HTTP REST API on :8080 (incl. /v1/schemas endpoints).
//   - gRPC API on :9090 (lease/translation/operator endpoints).
//   - Generates a fresh ECDSA P-256 key on first run, persists to ./data/<operator>.pem.
//   - BadgerDB store at ./data/<operator>-db.
//
// For production deployment, replace the key generation with a proper KMS
// integration; this binary is intentionally simple.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/otap/flr/internal/api"
	"github.com/otap/flr/internal/config"
	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/federation"
	"github.com/otap/flr/internal/registry"
	"github.com/otap/flr/internal/xlat"
)

var (
	flagOperator = flag.String("operator", "op-alice", "operator ID for this node")
	flagDataDir  = flag.String("data", "./data", "data directory")
	flagHTTP     = flag.String("http", ":8080", "HTTP REST address")
	flagGRPC     = flag.String("grpc", ":9090", "gRPC address")
)

func main() {
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("flrd failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	if err := os.MkdirAll(*flagDataDir, 0o755); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	keyPath := filepath.Join(*flagDataDir, *flagOperator+".pem")
	dbPath := filepath.Join(*flagDataDir, *flagOperator+"-db")

	// Load or generate operator key.
	keyPEM, err := loadOrCreateKey(keyPath, logger)
	if err != nil {
		return fmt.Errorf("key: %w", err)
	}
	cryptoEng, err := crypto.NewEngine(*flagOperator, keyPEM)
	if err != nil {
		return fmt.Errorf("crypto: %w", err)
	}

	// Open BadgerStore.
	store, err := registry.NewBadgerStore(dbPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer store.Close()

	regEng := registry.NewEngine(store, cryptoEng, *flagOperator)
	fedClient := federation.NewClient(10 * time.Second)
	fedMgr := federation.NewManager(regEng, cryptoEng, fedClient, *flagOperator)
	xlatMgr := xlat.NewManager(store)

	// Config: minimum required to run the API server.
	cfg := &config.Config{
		Server: config.ServerConfig{
			GRPCAddr:     *flagGRPC,
			HTTPAddr:     *flagHTTP,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			EnableAuth:   false,
			MaxConn:      1000,
		},
		Federation: config.FederationConfig{
			GossipInterval: 30 * time.Second,
			SyncTimeout:    10 * time.Second,
			MaxPeers:       50,
		},
	}

	srv, err := api.NewServer(cfg, regEng, fedMgr, xlatMgr, cryptoEng, logger)
	if err != nil {
		return fmt.Errorf("api server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	logger.Info("flrd ready",
		"operator", *flagOperator,
		"http", *flagHTTP,
		"grpc", *flagGRPC,
		"data", *flagDataDir,
	)

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	logger.Info("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}

func loadOrCreateKey(path string, logger *slog.Logger) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		logger.Info("loaded existing operator key", "path", path)
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	logger.Info("generating new operator key", "path", path)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("persist key: %w", err)
	}
	return keyPEM, nil
}
