package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"

	"github.com/shihangw/playground-ledger/internal/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	var command string
	flag.StringVar(&command, "cmd", "up", "migration command (up, down, status, reset)")
	flag.Parse()

	ctx := context.Background()

	// Load configuration
	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database
	db, err := sql.Open("pgx", cfg.ActiveDSN())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Set migrations directory
	migrationsDir := "internal/db/migrations"
	if dir := os.Getenv("MIGRATIONS_DIR"); dir != "" {
		migrationsDir = dir
	}

	// Set dialect
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("Failed to set dialect: %v", err)
	}

	// Run command
	switch command {
	case "up":
		if err := goose.Up(db, migrationsDir); err != nil {
			log.Fatalf("Migration up failed: %v", err)
		}
	case "down":
		if err := goose.Down(db, migrationsDir); err != nil {
			log.Fatalf("Migration down failed: %v", err)
		}
	case "status":
		if err := goose.Status(db, migrationsDir); err != nil {
			log.Fatalf("Migration status failed: %v", err)
		}
	case "reset":
		if err := goose.Reset(db, migrationsDir); err != nil {
			log.Fatalf("Migration reset failed: %v", err)
		}
	default:
		log.Fatalf("Unknown command: %s", command)
	}
}
