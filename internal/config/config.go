package config

import (
	"context"
	"fmt"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

// Config holds all application configuration
type Config struct {
	// Database
	CRDBDSN string

	// WorkOS
	WorkOSAPIKey   string
	WorkOSClientID string

	// Server
	Port        string
	Environment string
	ProjectID   string
}

// Load loads configuration from environment and GCP Secret Manager
func Load(ctx context.Context) (*Config, error) {
	cfg := &Config{
		Port:        getEnv("PORT", "8080"),
		Environment: getEnv("ENV", "development"),
		ProjectID:   getEnv("GCP_PROJECT_ID", "sw-playground-ledger"),
	}

	// Set GOOGLE_APPLICATION_CREDENTIALS if custom path is provided
	if credPath := os.Getenv("PLAYGROUND_GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credPath)
	}

	// In development, allow loading from environment variables directly
	if cfg.Environment == "development" {
		cfg.CRDBDSN = os.Getenv("CRDB_DSN")
		cfg.WorkOSAPIKey = os.Getenv("WORKOS_API_KEY")
		cfg.WorkOSClientID = os.Getenv("WORKOS_CLIENT_ID")

		// If env vars are set, use them
		if cfg.CRDBDSN != "" && cfg.WorkOSAPIKey != "" {
			return cfg, nil
		}
	}

	// Load from Secret Manager using service account credentials
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer client.Close()

	// Load CRDB DSN
	cfg.CRDBDSN, err = getSecret(ctx, client, cfg.ProjectID, "CRDB_DSN")
	if err != nil {
		return nil, fmt.Errorf("failed to load CRDB_DSN: %w", err)
	}

	// Load WorkOS secrets
	cfg.WorkOSAPIKey, err = getSecret(ctx, client, cfg.ProjectID, "WORKOS_API_KEY")
	if err != nil {
		return nil, fmt.Errorf("failed to load WORKOS_API_KEY: %w", err)
	}

	cfg.WorkOSClientID, err = getSecret(ctx, client, cfg.ProjectID, "WORKOS_CLIENT_ID")
	if err != nil {
		return nil, fmt.Errorf("failed to load WORKOS_CLIENT_ID: %w", err)
	}

	return cfg, nil
}

func getSecret(ctx context.Context, client *secretmanager.Client, projectID, secretName string) (string, error) {
	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretName)

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: name,
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		return "", err
	}

	return string(result.Payload.Data), nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
