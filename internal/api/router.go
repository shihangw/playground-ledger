package api

import (
	"net/http"

	"github.com/anthropics/playground-ledger/internal/api/handlers"
	"github.com/anthropics/playground-ledger/internal/api/middleware"
	"github.com/anthropics/playground-ledger/internal/wallet"
)

// NewRouter creates and configures the HTTP router
func NewRouter(walletService *wallet.Service) http.Handler {
	mux := http.NewServeMux()

	walletHandler := handlers.NewWalletHandler(walletService)

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

	// Wallet operations (require idempotency key)
	protected.HandleFunc("POST /v1/accounts/{account_id}/deposit", walletHandler.Deposit)
	protected.HandleFunc("POST /v1/accounts/{account_id}/withdraw", walletHandler.Withdraw)
	protected.HandleFunc("POST /v1/accounts/{account_id}/transfer", walletHandler.Transfer)

	// Apply middleware
	var handler http.Handler = protected
	handler = middleware.IdempotencyMiddleware(handler)
	handler = middleware.AuthMiddleware(handler)

	// Mount protected routes
	mux.Handle("/", handler)

	return mux
}
