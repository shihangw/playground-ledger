package config

import (
	"context"
	"fmt"
	"os"
	"strconv"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

// Config holds all application configuration
type Config struct {
	// Database
	AlloyDBDSN string
	DBPoolSize int // max connections in the pool (DB_POOL_SIZE, default 600)
	DBMinConns int // min warm connections (DB_MIN_CONNS, default 10)

	// DB_POOL_DSN: if set, overrides AlloyDBDSN with a connection pooler endpoint
	// (e.g. AlloyDB's built-in pgBouncer). Also enables simple-protocol mode.
	DBPoolDSN        string
	DBSimpleProtocol bool // use PostgreSQL simple query protocol (required for pgBouncer transaction mode)

	// Server
	Port        string
	Environment string
	ProjectID   string
}

// ActiveDSN returns the DSN to connect with.
// If DB_POOL_DSN is set, it takes precedence (points at the connection pooler).
func (c *Config) ActiveDSN() string {
	if c.DBPoolDSN != "" {
		return c.DBPoolDSN
	}
	return c.AlloyDBDSN
}

// Load loads configuration from environment and GCP Secret Manager
func Load(ctx context.Context) (*Config, error) {
	poolSize := 600
	if v := os.Getenv("DB_POOL_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			poolSize = n
		}
	}
	minConns := 10
	if v := os.Getenv("DB_MIN_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			minConns = n
		}
	}
	cfg := &Config{
		Port:             getEnv("PORT", "8080"),
		Environment:      getEnv("ENV", "development"),
		ProjectID:        getEnv("GCP_PROJECT_ID", "sw-playground-ledger"),
		DBPoolSize:       poolSize,
		DBMinConns:       minConns,
		DBPoolDSN:        os.Getenv("DB_POOL_DSN"),
		DBSimpleProtocol: os.Getenv("DB_SIMPLE_PROTOCOL") == "true",
	}

	// Set GOOGLE_APPLICATION_CREDENTIALS if custom path is provided
	if credPath := os.Getenv("PLAYGROUND_GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credPath)
	}

	// In development, allow loading from environment variables directly
	if cfg.Environment == "development" {
		cfg.AlloyDBDSN = os.Getenv("ALLOYDB_DSN")
		if cfg.ActiveDSN() != "" {
			return cfg, nil
		}
	}

	// Load from Secret Manager using service account credentials
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret manager client: %w", err)
	}
	defer client.Close()

	cfg.AlloyDBDSN, err = getSecret(ctx, client, cfg.ProjectID, "ALLOYDB_DSN")
	if err != nil {
		return nil, fmt.Errorf("failed to load ALLOYDB_DSN: %w", err)
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
