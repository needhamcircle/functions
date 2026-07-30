package functions

import (
	"fmt"
	"log"
	"mime"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

func init() {
	functions.HTTP("SendContact", SendContact)
}

const (
	smtpHost = "smtp.gmail.com"
	smtpAddr = "smtp.gmail.com:587"
)

var whitespaceRuns = regexp.MustCompile(`\s+`)

// contactRequest's field names match the Sinatra ContactForm's param names.
// Website is the same honeypot the submission form uses.
type contactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
	Website string `json:"website"`
}

func (r *contactRequest) trim() {
	r.Name = strings.TrimSpace(r.Name)
	r.Email = strings.TrimSpace(r.Email)
	r.Subject = strings.TrimSpace(r.Subject)
	r.Message = strings.TrimSpace(r.Message)
}

type contactServer struct {
	send    func(replyTo, subject, body string) error
	limiter *rateLimiter
}

func (s *contactServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.limiter.allow(clientIP(r)) {
		tooManyRequests(w, "60")
		return
	}

	var req contactRequest
	if !decodeForm(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Website) != "" {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	req.trim()
	errors := validateContact(&req)
	if len(errors) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errors})
		return
	}

	if err := s.send(req.Email, subjectFor(&req), bodyFor(&req)); err != nil {
		log.Printf("sending contact message: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "We couldn't send your message. Please try again later.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validateContact ports the ContactForm rules and messages field-for-field.
func validateContact(req *contactRequest) fieldErrors {
	errors := fieldErrors{}

	requireString(errors, "name", "Name", req.Name, true, 100)

	requireString(errors, "email", "Email", req.Email, true, 200)
	if req.Email != "" && !emailPattern.MatchString(req.Email) {
		errors.add("email", "Email must be a valid email address.")
	}

	requireString(errors, "subject", "Subject", req.Subject, false, 200)
	requireString(errors, "message", "Message", req.Message, true, 5000)

	return errors
}

// subjectFor mirrors the Ruby Mailer: collapse any whitespace (including
// newlines) in the visitor's subject so it can't smuggle extra headers, fall
// back to a default when blank, and tag it for the organizers' inbox.
func subjectFor(req *contactRequest) string {
	subject := strings.TrimSpace(whitespaceRuns.ReplaceAllString(req.Subject, " "))
	if subject == "" {
		subject = "New contact message"
	}
	return "[Needham Circle] " + subject
}

// bodyFor keeps every piece of the visitor's free text in the body, never in
// a header, matching the Ruby Mailer.
func bodyFor(req *contactRequest) string {
	return fmt.Sprintf("Name: %s\nEmail: %s\n\n%s\n", req.Name, req.Email, req.Message)
}

// sendViaGmail delivers through the organizers' Gmail over SMTP with an app
// password (the Calendar service account can't send as a personal @gmail.com
// — that would need Workspace domain-wide delegation). The message is sent
// from — and to — the organizers' own inbox with the visitor's address in
// Reply-To, so hitting reply in Gmail goes straight back to them; replyTo is
// regexp-validated, so it cannot carry header-splitting characters.
func sendViaGmail(account, password, replyTo, subject, body string) error {
	var msg strings.Builder
	msg.WriteString("From: " + account + "\r\n")
	msg.WriteString("To: " + account + "\r\n")
	msg.WriteString("Reply-To: " + replyTo + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))

	auth := smtp.PlainAuth("", account, password, smtpHost)
	return smtp.SendMail(smtpAddr, auth, account, []string{account}, []byte(msg.String()))
}

var defaultContactServer = sync.OnceValues(func() (*contactServer, error) {
	password := os.Getenv("SMTP_PASSWORD")
	if password == "" {
		return nil, fmt.Errorf("SMTP_PASSWORD is not set")
	}

	account := os.Getenv("SMTP_ACCOUNT")
	if account == "" {
		account = "needhamcircle@gmail.com"
	}

	return &contactServer{
		send: func(replyTo, subject, body string) error {
			return sendViaGmail(account, password, replyTo, subject, body)
		},
		// The Sinatra app rate-limited POST /contact to 5 per minute per IP.
		limiter: newRateLimiter(5, time.Minute),
	}, nil
})

// SendContact is the deployed entry point (and the handler cmd/main.go
// mounts for local development).
func SendContact(w http.ResponseWriter, r *http.Request) {
	if handleCORS(w, r) {
		return
	}

	s, err := defaultContactServer()
	if err != nil {
		log.Printf("configuring contact function: %v", err)
		http.Error(w, "server misconfigured", http.StatusInternalServerError)
		return
	}

	s.handle(w, r)
}
