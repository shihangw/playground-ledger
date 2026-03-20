package api

import (
	"net/http"

	"github.com/shihangw/playground-ledger/internal/api/handlers"
	"github.com/shihangw/playground-ledger/internal/api/middleware"
	"github.com/shihangw/playground-ledger/internal/wallet"
)

// RouterConfig contains configuration for the router
type RouterConfig struct {
	WalletService *wallet.Service
}

// NewRouter creates and configures the HTTP router
func NewRouter(cfg RouterConfig) http.Handler {
	mux := http.NewServeMux()

	walletHandler := handlers.NewWalletHandler(cfg.WalletService)

	// Health check (no auth)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	})

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

	// Apply middleware
	var handler http.Handler = protected
	handler = middleware.IdempotencyMiddleware(handler)
	handler = middleware.AuthMiddleware(handler)
	handler = middleware.CORSMiddleware(handler)

	// Mount routes
	finalMux := http.NewServeMux()
	finalMux.Handle("/health", middleware.CORSMiddleware(mux))
	finalMux.Handle("/", handler)

	return finalMux
}
