// Serves every function on one local port for developing the static site
// against them:
//
//	PORT=8081 go run ./cmd/devserver
//
// Local runs accept the local Jekyll site's origin (localhost:4000, see
// cors.go), so no CORS configuration is needed. Deployed, each function is
// its own Cloud Run service; these paths exist only locally, and the site's
// local config points its endpoints here.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/needham-circle/functions"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/list-events", functions.ListEvents)
	mux.HandleFunc("/create-submission", functions.CreateSubmission)
	mux.HandleFunc("/send-contact", functions.SendContact)

	log.Printf("functions listening on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
