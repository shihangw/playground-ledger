package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anthropics/playground-ledger/internal/api"
	"github.com/anthropics/playground-ledger/internal/api/middleware"
	"github.com/anthropics/playground-ledger/internal/config"
	"github.com/anthropics/playground-ledger/internal/ledger"
	"github.com/anthropics/playground-ledger/internal/wallet"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize WorkOS
	middleware.InitWorkOS(cfg.WorkOSAPIKey, cfg.WorkOSClientID)

	// Connect to database
	pool, err := pgxpool.New(ctx, cfg.CRDBDSN)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	log.Println("Connected to CockroachDB")

	// Initialize services
	ledgerService := ledger.NewService(pool)
	walletService := wallet.NewService(pool, ledgerService)

	// Create router
	router := api.NewRouter(walletService)

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
