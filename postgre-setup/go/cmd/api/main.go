package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shihangw/playground-ledger/internal/api"
	"github.com/shihangw/playground-ledger/internal/config"
	"github.com/shihangw/playground-ledger/internal/grants"
	"github.com/shihangw/playground-ledger/internal/ledger"
	"github.com/shihangw/playground-ledger/internal/wallet"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database
	log.Printf("Using DB backend: %s", cfg.DBBackend)
	if cfg.DBPoolDSN != "" {
		log.Printf("Using connection pooler DSN (DB_POOL_DSN)")
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.ActiveDSN())
	if err != nil {
		log.Fatalf("Failed to parse DSN: %v", err)
	}
	poolCfg.MaxConns = int32(cfg.DBPoolSize)
	poolCfg.MinConns = int32(cfg.DBMinConns)
	if cfg.DBSimpleProtocol {
		poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		log.Printf("Using simple query protocol (pgBouncer transaction mode)")
	}
	// Set session settings for Postgres/AlloyDB only (CRDB uses OCC and doesn't support these).
	// synchronous_commit=off: skip WAL flush wait per commit — roughly doubles write throughput.
	// Data is still written to WAL; at most ~200ms of commits can be lost on a crash.
	if cfg.DBBackend == "alloydb" || cfg.DBBackend == "postgres" {
		poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET lock_timeout = '200ms'; SET synchronous_commit = off")
			return err
		}
		log.Printf("lock_timeout=200ms, synchronous_commit=off enabled")
	}
	log.Printf("Pool max_conns=%d min_conns=%d", poolCfg.MaxConns, poolCfg.MinConns)
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Verify connection with a 10s timeout so a bad DSN fails fast
	pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
	defer pingCancel()
	if err := pool.Ping(pingCtx); err != nil {
		log.Fatalf("Failed to ping database (%s): %v", cfg.DBBackend, err)
	}
	log.Printf("Connected to database (%s)", cfg.DBBackend)

	// Log pool stats every 5 seconds so we can see connection pressure
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var lastWaited int64
		for range ticker.C {
			s := pool.Stat()
			waited := s.EmptyAcquireCount()
			waitedDelta := waited - lastWaited
			lastWaited = waited
			log.Printf("[pool] total=%d idle=%d active=%d waited/5s=%d maxConns=%d",
				s.TotalConns(), s.IdleConns(), s.AcquiredConns(), waitedDelta, s.MaxConns())
		}
	}()

	// Initialize services
	ledgerService := ledger.NewService(pool)
	walletService := wallet.NewService(pool, ledgerService)
	grantsService := grants.NewService(pool)

	// Create router
	router := api.NewRouter(api.RouterConfig{
		WalletService: walletService,
		GrantsService: grantsService,
		Pool:          pool,
	})

	// Create server
	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting server on port %s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
