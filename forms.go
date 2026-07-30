package functions

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
)

// Shared plumbing for the two form-backed functions (CreateSubmission and
// SendContact): JSON responses, request decoding, and the validation
// patterns ported from the Ruby Form fields.

// URI::MailTo::EMAIL_REGEXP, ported from the Ruby email fields.
var emailPattern = regexp.MustCompile(
	"^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?" +
		"(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$",
)

// fieldErrors mirrors the Ruby Form's errors hash: field name to messages,
// serialized as the "errors" object the static forms render inline.
type fieldErrors map[string][]string

func (e fieldErrors) add(field, message string) {
	e[field] = append(e[field], message)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encoding response: %v", err)
	}
}

// decodeForm parses the JSON request body into target, capping the read so an
// oversized body can't balloon memory; the forms' own max-length rules are
// far below this cap.
func decodeForm(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The request body must be JSON."})
		return false
	}
	return true
}

// tooManyRequests answers a rate-limited request with the same message the
// Ruby middleware used.
func tooManyRequests(w http.ResponseWriter, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"error": "Too many submissions. Please try again later.",
	})
}
