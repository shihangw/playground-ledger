package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/shihangw/playground-ledger/internal/db/generated"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StressHandler handles stress test event logging and metrics
type StressHandler struct {
	queries *generated.Queries
}

// NewStressHandler creates a new stress handler
func NewStressHandler(pool *pgxpool.Pool) *StressHandler {
	return &StressHandler{queries: generated.New(pool)}
}

// LogEventRequest represents a single stress test event
type LogEventRequest struct {
	RunID        string  `json:"run_id"`
	EventType    string  `json:"event_type"`
	AccountID    string  `json:"account_id"`
	Success      bool    `json:"success"`
	LatencyMs    float64 `json:"latency_ms"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

// LogEventsBatchRequest represents a batch of events to log
type LogEventsBatchRequest struct {
	Events []LogEventRequest `json:"events"`
}

// LogEvents logs a batch of stress test events
func (h *StressHandler) LogEvents(w http.ResponseWriter, r *http.Request) {
	var req LogEventsBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "no events provided")
		return
	}

	var errors []string
	logged := 0
	for _, evt := range req.Events {
		var accountID pgtype.UUID
		if err := accountID.Scan(evt.AccountID); err != nil {
			errors = append(errors, fmt.Sprintf("invalid account_id %s: %v", evt.AccountID, err))
			continue
		}

		var errMsg pgtype.Text
		if evt.ErrorMessage != "" {
			errMsg = pgtype.Text{String: evt.ErrorMessage, Valid: true}
		}

		err := h.queries.InsertStressEvent(r.Context(), generated.InsertStressEventParams{
			RunID:        evt.RunID,
			EventType:    evt.EventType,
			AccountID:    accountID,
			Success:      evt.Success,
			LatencyMs:    evt.LatencyMs,
			ErrorMessage: errMsg,
		})
		if err != nil {
			errors = append(errors, fmt.Sprintf("insert failed: %v", err))
			continue
		}
		logged++
	}

	resp := map[string]interface{}{"logged": logged}
	if len(errors) > 0 {
		resp["errors"] = errors
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetRunSummary returns metrics summary for a stress test run
func (h *StressHandler) GetRunSummary(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "missing run_id")
		return
	}

	summary, err := h.queries.GetStressRunSummary(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get summary: "+err.Error())
		return
	}

	qps, err := h.queries.GetStressRunQPS(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get QPS: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"run_id":  runID,
		"summary": summary,
		"qps":     qps,
	})
}

// ListRuns returns recent stress test runs
func (h *StressHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	limit := int32(10)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = int32(parsed)
		}
	}

	runs, err := h.queries.ListStressRuns(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runs: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, runs)
}
