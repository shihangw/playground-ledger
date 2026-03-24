package main

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type Reporter struct {
	mu           sync.Mutex
	totalTxns    int
	successCount int
	errorCount   int
	errorsByType map[string]int
	latencies    []float64
	httpReqs     int
	startTime    time.Time
	endTime      time.Time

	lastTxnCount  int
	lastHttpCount int
	lastReportAt  time.Time
	ticker        *time.Ticker
	done          chan struct{}
}

func NewReporter() *Reporter {
	return &Reporter{errorsByType: make(map[string]int)}
}

func (r *Reporter) Start() {
	r.mu.Lock()
	r.startTime = time.Now()
	r.lastReportAt = r.startTime
	r.mu.Unlock()

	r.done = make(chan struct{})
	r.ticker = time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-r.ticker.C:
				r.printLive()
			case <-r.done:
				return
			}
		}
	}()
}

func (r *Reporter) RecordHTTP() {
	r.mu.Lock()
	r.httpReqs++
	r.mu.Unlock()
}

func (r *Reporter) Record(success bool, latencyMs float64, errorType string) {
	r.mu.Lock()
	r.totalTxns++
	r.latencies = append(r.latencies, latencyMs)
	if success {
		r.successCount++
	} else {
		r.errorCount++
		if errorType != "" {
			r.errorsByType[errorType]++
		}
	}
	r.mu.Unlock()
}

func (r *Reporter) Stop() {
	r.ticker.Stop()
	close(r.done)
	r.mu.Lock()
	r.endTime = time.Now()
	r.mu.Unlock()
}

func (r *Reporter) printLive() {
	r.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(r.startTime).Seconds()
	recentDuration := now.Sub(r.lastReportAt).Seconds()
	recentTxns := r.totalTxns - r.lastTxnCount
	recentHttp := r.httpReqs - r.lastHttpCount
	r.lastTxnCount = r.totalTxns
	r.lastHttpCount = r.httpReqs
	r.lastReportAt = now

	total := r.totalTxns
	httpTotal := r.httpReqs
	success := r.successCount
	errCount := r.errorCount
	contention := r.errorsByType["contention"]
	latsCopy := make([]float64, len(r.latencies))
	copy(latsCopy, r.latencies)
	r.mu.Unlock()

	txnPerSec := 0.0
	reqPerSec := 0.0
	if recentDuration > 0 {
		txnPerSec = float64(recentTxns) / recentDuration
		reqPerSec = float64(recentHttp) / recentDuration
	}

	p50 := percentile(latsCopy, 50)
	p95 := percentile(latsCopy, 95)
	p99 := percentile(latsCopy, 99)

	contentionRate := 0.0
	if total > 0 {
		contentionRate = float64(contention) / float64(total) * 100
	}

	isBatch := httpTotal > 0 && total > httpTotal*3/2
	var reqPart string
	if isBatch {
		reqPart = fmt.Sprintf("req/s: %.0f | txn/s: %.0f", reqPerSec, txnPerSec)
	} else {
		reqPart = fmt.Sprintf("txn/s: %.0f", txnPerSec)
	}

	fmt.Printf("[%.0fs] txn: %d | %s | ok: %d | err: %d | ctn: %d (%.1f%%) | p50: %.0fms | p95: %.0fms | p99: %.0fms\n",
		elapsed, total, reqPart, success, errCount, contention, contentionRate, p50, p95, p99)
}

func (r *Reporter) PrintSummary() {
	r.mu.Lock()
	elapsed := r.endTime.Sub(r.startTime).Seconds()
	total := r.totalTxns
	success := r.successCount
	errCount := r.errorCount
	httpTotal := r.httpReqs
	errorsByType := make(map[string]int, len(r.errorsByType))
	for k, v := range r.errorsByType {
		errorsByType[k] = v
	}
	latsCopy := make([]float64, len(r.latencies))
	copy(latsCopy, r.latencies)
	r.mu.Unlock()

	fmt.Println("\n\n--- Stress Test Summary ---")
	fmt.Printf("Duration:         %.1fs\n", elapsed)
	fmt.Printf("Total Txns:       %d\n", total)
	fmt.Printf("Avg txn/s:        %.1f\n", float64(total)/elapsed)
	if total > httpTotal*3/2 {
		fmt.Printf("Total HTTP Reqs:  %d\n", httpTotal)
		fmt.Printf("Avg req/s:        %.1f\n", float64(httpTotal)/elapsed)
		batchAvg := 0.0
		if httpTotal > 0 {
			batchAvg = float64(total) / float64(httpTotal)
		}
		fmt.Printf("Avg batch size:   %.1f\n", batchAvg)
	}
	fmt.Printf("Success:          %d\n", success)
	fmt.Printf("Errors:           %d\n", errCount)
	successRate := 0.0
	if total > 0 {
		successRate = float64(success) / float64(total) * 100
	}
	fmt.Printf("Success Rate:     %.1f%%\n", successRate)

	contention := errorsByType["contention"]
	contentionRate := 0.0
	if total > 0 {
		contentionRate = float64(contention) / float64(total) * 100
	}
	fmt.Printf("Contention:       %d (%.2f%% of requests)\n", contention, contentionRate)

	fmt.Println("\nLatency:")
	fmt.Printf("  p50: %.1fms\n", percentile(latsCopy, 50))
	fmt.Printf("  p95: %.1fms\n", percentile(latsCopy, 95))
	fmt.Printf("  p99: %.1fms\n", percentile(latsCopy, 99))
	fmt.Printf("  max: %.1fms\n", percentile(latsCopy, 100))

	if len(errorsByType) > 0 {
		fmt.Println("\nErrors by type:")
		for errType, count := range errorsByType {
			fmt.Printf("  %s: %d\n", errType, count)
		}
	}
}

func percentile(lats []float64, p int) float64 {
	if len(lats) == 0 {
		return 0
	}
	s := make([]float64, len(lats))
	copy(s, lats)
	sort.Float64s(s)
	idx := int(math.Ceil(float64(p)/100*float64(len(s)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}
