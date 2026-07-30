package functions

import (
	"net/http"
	"os"
	"strings"
	"sync"
)

// The static site calls every function cross-origin, so responses carry CORS
// headers for the origins listed in ALLOWED_ORIGINS — comma-separated, e.g.
// "https://needham-circle.github.io,http://localhost:4000"; "*" allows any
// origin. Unlisted origins get no CORS headers (the browser refuses to share
// the response), and preflight OPTIONS requests are answered before the
// function's own handler runs.

var allowedOrigins = sync.OnceValue(func() []string {
	var origins []string
	for _, origin := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
})

// handleCORS stamps the CORS headers for the request and reports whether it
// fully handled the request (a preflight); callers return immediately when it
// did.
func handleCORS(w http.ResponseWriter, r *http.Request) bool {
	return applyCORS(allowedOrigins(), w, r)
}

func applyCORS(origins []string, w http.ResponseWriter, r *http.Request) bool {
	headers := w.Header()

	if origin := r.Header.Get("Origin"); origin != "" {
		for _, allowed := range origins {
			if allowed == "*" || allowed == origin {
				// Echo the origin rather than "*" so the header stays valid if
				// credentialed requests are ever needed; Vary keeps caches from
				// serving one origin's response to another.
				headers.Set("Access-Control-Allow-Origin", origin)
				headers.Add("Vary", "Origin")
				break
			}
		}
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
