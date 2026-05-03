package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/silver/pmvibes/internal/service"
	"github.com/silver/pmvibes/internal/store"
)

type FinanceHandler struct {
	simSvc *service.SimulatorService
	log    *slog.Logger
}

func NewFinanceHandler(simSvc *service.SimulatorService, log *slog.Logger) *FinanceHandler {
	return &FinanceHandler{simSvc: simSvc, log: log}
}

// GetStatus returns the current simulation status and breakdown.
// Query history_limit (0–MaxFinanceHistoryLimit): includes persisted_total and persisted_events (newest first).
func (h *FinanceHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if h.simSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "simulator not initialized")
		return
	}
	limit := parseHistoryLimit(r.URL.Query().Get("history_limit"))
	status := h.simSvc.GetStatus(r.Context(), limit)
	writeJSON(w, http.StatusOK, status)
}

// FinanceHistoryResponse is GET /finance/history/{page} JSON body.
type FinanceHistoryResponse struct {
	Total      int64                  `json:"total"`
	Page       int64                  `json:"page"`
	PageSize   int64                  `json:"page_size"`
	TotalPages int64                  `json:"total_pages"`
	Events     []store.PersistedEvent `json:"events"`
}

// GetHistory returns one page of persisted simulation events (newest first; page 1 = newest).
func (h *FinanceHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	if h.simSvc == nil {
		h.log.Warn("GET /finance/history", "status", http.StatusServiceUnavailable, "reason", "simulator not initialized")
		writeError(w, http.StatusServiceUnavailable, "simulator not initialized")
		return
	}
	rawPage := r.PathValue("page")
	page, err := strconv.ParseUint(rawPage, 10, 64)
	if err != nil || page < 1 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	pageSize := service.HistoryPageSize
	offset := (int64(page) - 1) * pageSize

	ctx := r.Context()
	total, err := h.simSvc.PersistedLen(ctx)
	if err != nil {
		h.log.Error("GET /finance/history", "step", "persisted_len", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events, err := h.simSvc.PersistedPaged(ctx, offset, pageSize)
	if err != nil {
		h.log.Error("GET /finance/history", "step", "persisted_paged", "err", err, "page", page, "offset", offset)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var totalPages int64
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	h.log.Info("GET /finance/history",
		"status", http.StatusOK,
		"remote", r.RemoteAddr,
		"page", page,
		"page_size", pageSize,
		"total", total,
		"returned", len(events),
	)
	writeJSON(w, http.StatusOK, FinanceHistoryResponse{
		Total:      total,
		Page:       int64(page),
		PageSize:   pageSize,
		TotalPages: totalPages,
		Events:     events,
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
