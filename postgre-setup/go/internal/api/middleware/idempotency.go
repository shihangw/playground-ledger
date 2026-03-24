package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

const IdempotencyKeyHeader = "Idempotency-Key"

type idempotencyKeyType string

const IdempotencyKeyCtx idempotencyKeyType = "idempotency_key"

// IdempotencyMiddleware extracts and validates idempotency keys from requests
func IdempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only require idempotency key for mutating requests
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			keyStr := r.Header.Get(IdempotencyKeyHeader)
			if keyStr == "" {
				http.Error(w, `{"error": "missing Idempotency-Key header"}`, http.StatusBadRequest)
				return
			}

			key, err := uuid.Parse(keyStr)
			if err != nil {
				http.Error(w, `{"error": "invalid Idempotency-Key format, must be UUID"}`, http.StatusBadRequest)
				return
			}

			ctx := context.WithValue(r.Context(), IdempotencyKeyCtx, key)
			r = r.WithContext(ctx)
		}

		next.ServeHTTP(w, r)
	})
}

// GetIdempotencyKey extracts idempotency key from context
func GetIdempotencyKey(ctx context.Context) (uuid.UUID, bool) {
	key, ok := ctx.Value(IdempotencyKeyCtx).(uuid.UUID)
	return key, ok
}
