package functions

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sentMessage struct {
	replyTo, subject, body string
}

func stubContactServer(sendErr error) (*contactServer, *[]sentMessage) {
	sent := &[]sentMessage{}
	return &contactServer{
		send: func(replyTo, subject, body string) error {
			if sendErr != nil {
				return sendErr
			}
			*sent = append(*sent, sentMessage{replyTo, subject, body})
			return nil
		},
		limiter: newRateLimiter(100, time.Minute),
	}, sent
}

func postContact(t *testing.T, s *contactServer, payload any) *httptest.ResponseRecorder {
	t.Helper()
	return postContactFrom(t, s, payload, "")
}

// postContactFrom submits payload with the client address the deployed
// frontend conveys as the final X-Forwarded-For entry.
func postContactFrom(t *testing.T, s *contactServer, payload any, forwardedFor string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling payload: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	rec := httptest.NewRecorder()
	s.handle(rec, request)
	return rec
}

func validContact() map[string]string {
	return map[string]string{
		"name":    "Pat Neighbor",
		"email":   "pat@example.com",
		"subject": "Volunteering",
		"message": "I'd love to help out at the next event.",
	}
}

func TestContactSendsTheMessage(t *testing.T) {
	s, sent := stubContactServer(nil)

	rec := postContact(t, s, validContact())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if len(*sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(*sent))
	}

	message := (*sent)[0]
	if message.replyTo != "pat@example.com" {
		t.Errorf("replyTo = %q", message.replyTo)
	}
	if message.subject != "[Needham Circle] Volunteering" {
		t.Errorf("subject = %q", message.subject)
	}
	want := "Name: Pat Neighbor\nEmail: pat@example.com\n\nI'd love to help out at the next event.\n"
	if message.body != want {
		t.Errorf("body = %q, want %q", message.body, want)
	}
}

func TestContactSubjectCollapsesWhitespaceAndDefaults(t *testing.T) {
	s, sent := stubContactServer(nil)

	payload := validContact()
	payload["subject"] = "Header\r\nInjection:  attempt"
	postContact(t, s, payload)

	if got := (*sent)[0].subject; got != "[Needham Circle] Header Injection: attempt" {
		t.Errorf("subject = %q, want whitespace collapsed", got)
	}

	payload["subject"] = "   "
	postContact(t, s, payload)
	if got := (*sent)[1].subject; got != "[Needham Circle] New contact message" {
		t.Errorf("subject = %q, want the default", got)
	}
}

func TestContactValidationMessages(t *testing.T) {
	s, _ := stubContactServer(nil)

	rec := postContact(t, s, map[string]string{
		"name":    "",
		"email":   "nope",
		"message": "",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}

	var payload struct {
		Errors map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response was not JSON: %v", err)
	}

	for field, want := range map[string]string{
		"name":    "Name is required.",
		"email":   "Email must be a valid email address.",
		"message": "Message is required.",
	} {
		if len(payload.Errors[field]) == 0 || payload.Errors[field][0] != want {
			t.Errorf("errors[%q] = %v, want %q", field, payload.Errors[field], want)
		}
	}
}

func TestContactHoneypotPretendsSuccess(t *testing.T) {
	s, sent := stubContactServer(nil)

	payload := validContact()
	payload["website"] = "https://spam.example"
	rec := postContact(t, s, payload)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(*sent) != 0 {
		t.Error("a honeypot hit must not send mail")
	}
}

func TestContactTurnsSendErrorsInto502(t *testing.T) {
	s, _ := stubContactServer(errors.New("smtp down"))

	rec := postContact(t, s, validContact())
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestContactRateLimits(t *testing.T) {
	s, _ := stubContactServer(nil)
	s.limiter = newRateLimiter(1, time.Minute)

	postContactFrom(t, s, validContact(), "203.0.113.9")
	rec := postContactFrom(t, s, validContact(), "203.0.113.9")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}

	// The limit is keyed on the forwarded client address, so one client
	// hitting it leaves everyone else unaffected.
	other := postContactFrom(t, s, validContact(), "203.0.113.50")
	if other.Code != http.StatusOK {
		t.Errorf("other client: status = %d, want 200", other.Code)
	}
}
