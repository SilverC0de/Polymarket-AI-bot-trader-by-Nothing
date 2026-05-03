package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/silver/pmvibes/internal/service"
	"github.com/silver/pmvibes/internal/store"
)

type FinanceHandler struct {
	svc    *service.FinanceService
	simSvc *service.SimulatorService
}

func NewFinanceHandler(svc *service.FinanceService, simSvc *service.SimulatorService) *FinanceHandler {
	return &FinanceHandler{svc: svc, simSvc: simSvc}
}

// GetStatus returns the current simulation status and breakdown.
// Query history_limit (0–MaxFinanceHistoryLimit): when REDIS_URL is set, includes persisted_total and persisted_events (newest first).
func (h *FinanceHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if h.simSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "simulator not initialized")
		return
	}
	limit := parseHistoryLimit(r.URL.Query().Get("history_limit"))
	status := h.simSvc.GetStatus(r.Context(), limit)
	writeJSON(w, http.StatusOK, status)
}

// FinanceHistoryResponse is GET /finance/history JSON body.
type FinanceHistoryResponse struct {
	Total  int64                  `json:"total"`
	Offset int64                  `json:"offset"`
	Limit  int64                  `json:"limit"`
	Events []store.PersistedEvent `json:"events"`
}

// GetHistory returns paginated persisted simulation events from Redis (offset from newest).
func (h *FinanceHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	if h.simSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "simulator not initialized")
		return
	}
	offset := clampQueryInt64(r.URL.Query().Get("offset"), 0, 0, 1_000_000)
	limit := clampQueryInt64(r.URL.Query().Get("limit"), 200, 1, service.MaxFinanceHistoryLimit)

	ctx := r.Context()
	total, err := h.simSvc.PersistedLen(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events, err := h.simSvc.PersistedPaged(ctx, offset, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, FinanceHistoryResponse{
		Total:  total,
		Offset: offset,
		Limit:  limit,
		Events: events,
	})
}

func parseHistoryLimit(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	if n > service.MaxFinanceHistoryLimit {
		return service.MaxFinanceHistoryLimit
	}
	return n
}

func clampQueryInt64(raw string, defaultVal, minVal, maxVal int64) int64 {
	if raw == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return defaultVal
	}
	if n < minVal {
		return minVal
	}
	if n > maxVal {
		return maxVal
	}
	return n
}

// GetPositions returns all current open positions.
func (h *FinanceHandler) GetPositions(w http.ResponseWriter, r *http.Request) {
	positions, err := h.svc.GetOpenPositions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, positions)
}

// GetPredictions returns the latest 5-minute crypto market predictions from Polymarket.
func (h *FinanceHandler) GetPredictions(w http.ResponseWriter, r *http.Request) {
	market := r.URL.Query().Get("market")
	if market == "" {
		writeError(w, http.StatusBadRequest, "market query param is required")
		return
	}

	predictions, err := h.svc.GetPredictions(r.Context(), market)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, predictions)
}

// ExecuteTrade places a trade on Polymarket based on the 5-minute prediction signal.
func (h *FinanceHandler) ExecuteTrade(w http.ResponseWriter, r *http.Request) {
	var req service.TradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.svc.ExecuteTrade(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
