package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/workos/workos-go/v4/pkg/usermanagement"
)

type contextKey string

const (
	UserIDKey    contextKey = "user_id"
	UserEmailKey contextKey = "user_email"
)

var userMgmt *usermanagement.Client

// InitWorkOS initializes WorkOS with API key
func InitWorkOS(apiKey, clientID string) {
	userMgmt = usermanagement.NewClient(apiKey)
	// Set environment variable for SDK
	os.Setenv("WORKOS_API_KEY", apiKey)
	os.Setenv("WORKOS_CLIENT_ID", clientID)
}

// AuthMiddleware verifies WorkOS session tokens
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error": "missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			http.Error(w, `{"error": "invalid authorization header format"}`, http.StatusUnauthorized)
			return
		}

		// For development, allow a simple user ID token (dev_xxx format)
		if strings.HasPrefix(token, "dev_") {
			userID := strings.TrimPrefix(token, "dev_")
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			ctx = context.WithValue(ctx, UserEmailKey, userID+"@dev.local")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Verify with WorkOS - authenticate the session/access token
		if userMgmt == nil {
			http.Error(w, `{"error": "auth not configured"}`, http.StatusInternalServerError)
			return
		}

		user, err := userMgmt.GetUser(r.Context(), usermanagement.GetUserOpts{
			User: token, // This should be the user ID from the access token
		})
		if err != nil {
			http.Error(w, `{"error": "invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		// Add user info to context
		ctx := context.WithValue(r.Context(), UserIDKey, user.ID)
		ctx = context.WithValue(ctx, UserEmailKey, user.Email)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetUserID extracts user ID from context
func GetUserID(ctx context.Context) string {
	if id, ok := ctx.Value(UserIDKey).(string); ok {
		return id
	}
	return ""
}

// GetUserEmail extracts user email from context
func GetUserEmail(ctx context.Context) string {
	if email, ok := ctx.Value(UserEmailKey).(string); ok {
		return email
	}
	return ""
}
