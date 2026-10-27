package proxy

import "net/http"

// The SPA uses the same host on another port. Preflight carries no capability,
// but actual API requests still pass requireBridgeAuth.
func bridgeCORS(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		if !allowedOrigin(r) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return false
		}
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
			w.WriteHeader(http.StatusNoContent)
			return false
		}
	}
	return true
}
