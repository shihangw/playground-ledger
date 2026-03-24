package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdMetrics(args []string) {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	apiURL := fs.String("api-url", "http://localhost:8080", "API base URL")
	runID := fs.String("run-id", "", "Specific run ID to query (omit to list runs)")
	limit := fs.Int("limit", 10, "Number of recent runs to show")
	fs.Parse(args)

	client := NewLedgerClient(*apiURL)
	if *runID == "" {
		listRuns(client, *limit)
	} else {
		showRun(client, *runID)
	}
}

func listRuns(client *LedgerClient, limit int) {
	fmt.Println("Recent stress test runs:\n")
	runs, err := client.ListRuns(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(runs) == 0 {
		fmt.Println("No runs found.")
		return
	}
	fmt.Printf("%-36s %-10s %-28s %s\n", "Run ID", "Events", "Started", "Ended")
	fmt.Println(strings.Repeat("-", 100))
	for _, run := range runs {
		fmt.Printf("%-36s %-10v %-28v %v\n",
			run["run_id"], run["total_events"], run["started_at"], run["ended_at"])
	}
	fmt.Println("\nUse: ledger-stress metrics --run-id <id> for details")
}

func showRun(client *LedgerClient, runID string) {
	data, err := client.GetRunSummary(runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n--- Stress Run: %s ---\n\n", runID)

	summaryRaw, hasSummary := data["summary"]
	if !hasSummary {
		fmt.Println("No events found for this run.")
		return
	}
	summary, ok := summaryRaw.([]interface{})
	if !ok || len(summary) == 0 {
		fmt.Println("No events found for this run.")
		return
	}

	fmt.Println("By Event Type:")
	fmt.Printf("%-14s %-10s %-10s %-10s %-12s %-12s %-12s %-12s %s\n",
		"Type", "Total", "OK", "Err", "Avg(ms)", "p50(ms)", "p95(ms)", "p99(ms)", "Max(ms)")
	fmt.Println(strings.Repeat("-", 100))

	totalReqs, totalOk, totalErr := 0, 0, 0
	for _, rowRaw := range summary {
		row, ok := rowRaw.(map[string]interface{})
		if !ok {
			continue
		}
		total := jsonInt(row["total_count"])
		okCount := jsonInt(row["success_count"])
		errCount := jsonInt(row["error_count"])
		totalReqs += total
		totalOk += okCount
		totalErr += errCount
		fmt.Printf("%-14s %-10d %-10d %-10d %-12.1f %-12.1f %-12.1f %-12.1f %.1f\n",
			row["event_type"],
			total, okCount, errCount,
			jsonFloat(row["avg_latency_ms"]),
			jsonFloat(row["p50_latency_ms"]),
			jsonFloat(row["p95_latency_ms"]),
			jsonFloat(row["p99_latency_ms"]),
			jsonFloat(row["max_latency_ms"]),
		)
	}
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("%-14s %-10d %-10d %-10d\n", "TOTAL", totalReqs, totalOk, totalErr)
	successRate := 0.0
	if totalReqs > 0 {
		successRate = float64(totalOk) / float64(totalReqs) * 100
	}
	fmt.Printf("\nSuccess Rate: %.1f%%\n", successRate)

	qpsRaw, hasQPS := data["qps"]
	if !hasQPS {
		return
	}
	qps, ok := qpsRaw.([]interface{})
	if !ok || len(qps) == 0 {
		return
	}

	fmt.Printf("\nQPS Over Time (%d seconds):\n", len(qps))
	maxQPS := 0
	for _, qRaw := range qps {
		q, ok := qRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if c := jsonInt(q["request_count"]); c > maxQPS {
			maxQPS = c
		}
	}
	const barWidth = 40
	for _, qRaw := range qps {
		q, ok := qRaw.(map[string]interface{})
		if !ok {
			continue
		}
		count := jsonInt(q["request_count"])
		okCount := jsonInt(q["success_count"])
		barLen := 0
		if maxQPS > 0 {
			barLen = count * barWidth / maxQPS
		}
		second := fmt.Sprintf("%v", q["second"])
		if len(second) >= 19 {
			second = second[11:19] // extract HH:MM:SS
		}
		fmt.Printf("  %s | %-40s %d req (%d ok)\n", second, strings.Repeat("#", barLen), count, okCount)
	}
	fmt.Printf("\nAvg QPS: %.1f\n", float64(totalReqs)/float64(len(qps)))
}

func jsonInt(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func jsonFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}
