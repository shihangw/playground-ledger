package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "seed":
		cmdSeed(os.Args[2:])
	case "stress":
		cmdStress(os.Args[2:])
	case "verify":
		cmdVerify(os.Args[2:])
	case "metrics":
		cmdMetrics(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `ledger-stress: stress testing CLI for the playground ledger

Usage:
  ledger-stress <command> [flags]

Commands:
  seed      Seed the database with test users
  stress    Run stress tests against the ledger
  verify    Verify ledger correctness
  metrics   Query stress test metrics from the server`)
}

func cmdSeed(args []string) {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	apiURL := fs.String("api-url", "http://localhost:8080", "API base URL")
	count := fs.Int("count", 100, "Number of users to create")
	prefix := fs.String("prefix", "stress", "User ID prefix")
	initialBalance := fs.String("initial-balance", "10000", "Initial balance per user")
	fs.Parse(args)

	if *count < 1 {
		fmt.Fprintln(os.Stderr, "--count must be a positive number")
		os.Exit(1)
	}

	client := NewLedgerClient(*apiURL)
	fmt.Printf("Seeding %d users with prefix %q and $%s balance...\n", *count, *prefix, *initialBalance)
	fmt.Printf("API: %s\n\n", *apiURL)

	const batchSize = 10000
	remaining := *count
	startIndex := 0
	totalCreated := 0
	start := time.Now()

	for remaining > 0 {
		batch := remaining
		if batch > batchSize {
			batch = batchSize
		}
		result, err := client.Seed(batch, *prefix, *initialBalance, startIndex)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			os.Exit(1)
		}
		totalCreated += result.Created
		remaining -= batch
		startIndex += batch
		fmt.Printf("\r  seeded %d/%d...", totalCreated, *count)
	}
	fmt.Printf("\nCreated %d users in %.1fs\n", totalCreated, time.Since(start).Seconds())
}
