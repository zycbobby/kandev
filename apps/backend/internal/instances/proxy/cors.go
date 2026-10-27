package proxy

import "net/http"

// The SPA uses the same host on another port. Preflight carries no capability,
// but actual API requests still pass requireBridgeAuth.
//
// The allow-header list mirrors exactly the custom request headers the SPA
// sends on /api/v1 calls (lib/api/client.ts sets the interlock token on
// mutating requests; kanban-api/workspace-api send the delete confirmation;
// session-api sends If-None-Match on conditional reads) — the backend's own
// CORS middleware allows the same names. Anything not listed fails its
// preflight through the proxy.
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
			w.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, Authorization, X-Kandev-Interim-Settings-Interlock, X-Kandev-Task-Delete-Confirmation, If-None-Match",
			)
			w.WriteHeader(http.StatusNoContent)
			return false
		}
	}
	return true
}
