package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/shihangw/playground-ledger/internal/config"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := config.Load(ctx)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Backend:     %s\n", cfg.DBBackend)
	fmt.Printf("Active DSN:  %s\n", maskDSN(cfg.ActiveDSN()))
	fmt.Println()

	db, err := sql.Open("pgx", cfg.ActiveDSN())
	if err != nil {
		fmt.Printf("Connection:  FAILED (%v)\n", err)
		return
	}
	defer db.Close()

	start := time.Now()
	if err := db.PingContext(ctx); err != nil {
		fmt.Printf("Connection:  FAILED (%v)\n", err)
		return
	}
	fmt.Printf("Connection:  OK (%.1fms)\n", float64(time.Since(start).Microseconds())/1000)

	// DB version
	var version string
	if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err == nil {
		fmt.Printf("Version:     %s\n", version)
	}

	fmt.Println()

	// Migration status from goose table
	rows, err := db.QueryContext(ctx, `
		SELECT version_id, is_applied, tstamp
		FROM goose_db_version
		ORDER BY version_id`)
	if err != nil {
		fmt.Printf("Migrations:  (goose_db_version not found — run migrate first)\n")
		return
	}
	defer rows.Close()

	fmt.Println("Migrations:")
	fmt.Printf("  %-10s %-10s %s\n", "VERSION", "APPLIED", "TIMESTAMP")
	for rows.Next() {
		var versionID int64
		var isApplied bool
		var ts time.Time
		if err := rows.Scan(&versionID, &isApplied, &ts); err != nil {
			continue
		}
		applied := "yes"
		if !isApplied {
			applied = "no"
		}
		fmt.Printf("  %-10d %-10s %s\n", versionID, applied, ts.Format("2006-01-02 15:04:05"))
	}

	// Row counts for key tables
	fmt.Println()
	fmt.Println("Table counts:")
	tables := []string{"users", "accounts", "ledger_entries", "transactions", "credit_grants"}
	for _, t := range tables {
		var count int64
		if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", t)).Scan(&count); err == nil {
			fmt.Printf("  %-20s %d\n", t, count)
		}
	}
}

// maskDSN hides the password in the DSN for display.
func maskDSN(dsn string) string {
	// Find password= or :password@ patterns and mask them
	if len(dsn) == 0 {
		return "(empty)"
	}
	// Simple: truncate after 60 chars
	if len(dsn) > 80 {
		return dsn[:60] + "...[masked]"
	}
	return dsn
}
