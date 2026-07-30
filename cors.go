package functions

import (
	"net/http"
	"os"
	"slices"
	"sync"
)

var productionOrigins = []string{
	"https://needhamcircle.org",
	"https://www.needhamcircle.org",
}

var localOrigins = []string{
	"http://localhost:4000",
	"http://127.0.0.1:4000",
}

// Cloud Run sets K_SERVICE in every deployed instance, which is what
// distinguishes deployed from local.
var allowedOrigins = sync.OnceValue(func() []string {
	if os.Getenv("K_SERVICE") != "" {
		return productionOrigins
	}
	return localOrigins
})

// handleCORS stamps the CORS headers for the request and reports whether it
// fully handled the request (a preflight); callers return immediately when it
// did.
func handleCORS(w http.ResponseWriter, r *http.Request) bool {
	return applyCORS(allowedOrigins(), w, r)
}

func applyCORS(origins []string, w http.ResponseWriter, r *http.Request) bool {
	headers := w.Header()

	if origin := r.Header.Get("Origin"); slices.Contains(origins, origin) {
		headers.Set("Access-Control-Allow-Origin", origin)
		headers.Add("Vary", "Origin")
	}

	if r.Method == http.MethodOptions {
		headers.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "Content-Type")
		headers.Set("Access-Control-Max-Age", "3600")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	return false
}
