package server

import (
	"net/http"
	"path"
	"strconv"
	"strings"
)

func routeKey(method, p string) string {
	return method + " " + canonicalPath(p)
}

func canonicalPath(p string) string {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "." {
		return "/"
	}
	return p
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
		next.ServeHTTP(w, r)
	})
}

// strictAllowlist returns 404 for any method/path pair not explicitly registered,
// including wrong HTTP methods on known paths (avoids 405 probing).
func strictAllowlist(allowed map[string]struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := routeKey(r.Method, r.URL.Path)
		if _, ok := allowed[key]; ok {
			next.ServeHTTP(w, r)
			return
		}
		if allowedFinanceHistoryPage(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		writeRouteNotFound(w)
	})
}

func writeRouteNotFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}

// allowedFinanceHistoryPage reports GET /finance/history/{page} with page >= 1 (no bare /finance/history).
func allowedFinanceHistoryPage(method, rawPath string) bool {
	if method != http.MethodGet {
		return false
	}
	p := canonicalPath(rawPath)
	const prefix = "/finance/history/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	rest := strings.TrimPrefix(p, prefix)
	if rest == "" || strings.Contains(rest, "/") {
		return false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	return err == nil && n >= 1
}
