package server

import (
	_ "embed"
	"net/http"
)

//go:embed static/finance_dashboard.html
var financeDashboardHTML []byte

func serveFinanceDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(financeDashboardHTML)
}
