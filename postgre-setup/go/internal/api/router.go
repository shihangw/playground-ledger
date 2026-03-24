package api

import (
	"net/http"

	"github.com/shihangw/playground-ledger/internal/api/handlers"
	"github.com/shihangw/playground-ledger/internal/api/middleware"
	"github.com/shihangw/playground-ledger/internal/grants"
	"github.com/shihangw/playground-ledger/internal/wallet"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RouterConfig contains configuration for the router
type RouterConfig struct {
	WalletService *wallet.Service
	GrantsService *grants.Service
	Pool          *pgxpool.Pool
}

// NewRouter creates and configures the HTTP router
func NewRouter(cfg RouterConfig) http.Handler {
	mux := http.NewServeMux()

	walletHandler := handlers.NewWalletHandler(cfg.WalletService, int(cfg.Pool.Config().MaxConns))
	adminHandler := handlers.NewAdminHandler(cfg.WalletService, cfg.Pool)
	stressHandler := handlers.NewStressHandler(cfg.Pool)
	grantsHandler := handlers.NewGrantsHandler(cfg.GrantsService)

	// Health check (no auth)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

	// Batch endpoint — no auth, for high-throughput stress testing
	mux.HandleFunc("POST /v1/batch", walletHandler.Batch)

	// Admin routes (no auth for stress testing)
	mux.HandleFunc("POST /v1/admin/seed", adminHandler.Seed)
	mux.HandleFunc("GET /v1/admin/reconcile", adminHandler.Reconcile)

	// Stress test event logging and metrics (no auth)
	mux.HandleFunc("POST /v1/admin/stress/events", stressHandler.LogEvents)
	mux.HandleFunc("GET /v1/admin/stress/runs", stressHandler.ListRuns)
	mux.HandleFunc("GET /v1/admin/stress/runs/{run_id}", stressHandler.GetRunSummary)

	// Grant admin routes (no auth for expiration trigger)
	mux.HandleFunc("POST /v1/admin/grants/expire", grantsHandler.ExpireGrants)

	// Protected routes
	protected := http.NewServeMux()

	// Account routes
	protected.HandleFunc("GET /v1/accounts", walletHandler.GetAccounts)
	protected.HandleFunc("GET /v1/accounts/{account_id}", walletHandler.GetAccount)
	protected.HandleFunc("GET /v1/accounts/{account_id}/transactions", walletHandler.GetTransactions)

	// User directory (for sending money)
	protected.HandleFunc("GET /v1/users", walletHandler.GetAllUsers)

	// Wallet operations (require idempotency key)
	protected.HandleFunc("POST /v1/accounts/{account_id}/deposit", walletHandler.Deposit)
	protected.HandleFunc("POST /v1/accounts/{account_id}/withdraw", walletHandler.Withdraw)
	protected.HandleFunc("POST /v1/accounts/{account_id}/transfer", walletHandler.Transfer)

	// Grant operations
	protected.HandleFunc("POST /v1/accounts/{account_id}/grants", grantsHandler.IssueGrant)
	protected.HandleFunc("POST /v1/accounts/{account_id}/grants/drawdown", grantsHandler.Drawdown)
	protected.HandleFunc("GET /v1/accounts/{account_id}/grants", grantsHandler.ListGrants)
	protected.HandleFunc("GET /v1/accounts/{account_id}/grants/balance", grantsHandler.GetBalance)
	protected.HandleFunc("POST /v1/accounts/{account_id}/pay", grantsHandler.WaterfallDebit)

	// Apply middleware
	var handler http.Handler = protected
	handler = middleware.IdempotencyMiddleware(handler)
	handler = middleware.AuthMiddleware(handler)
	handler = middleware.CORSMiddleware(handler)

	// Mount routes
	finalMux := http.NewServeMux()
	finalMux.Handle("/health", middleware.CORSMiddleware(mux))
	finalMux.Handle("/v1/admin/", middleware.CORSMiddleware(mux))
	finalMux.HandleFunc("POST /v1/batch", walletHandler.Batch)
	finalMux.Handle("/", handler)

	return finalMux
}
