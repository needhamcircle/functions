package functions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/calendar/v3"
)

// submissionNow is the fixed "now" the validation tests run against.
var submissionNow = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

func validSubmission() map[string]string {
	return map[string]string{
		"title":      "Concert on the Green",
		"host":       "Needham Circle",
		"email":      "host@example.com",
		"start_time": "2026-08-01T18:30",
		"end_time":   "2026-08-01T20:00",
		"location":   "Needham Common",
		"url":        "https://example.com/concert",
	}
}

func postSubmission(t *testing.T, s *submissionServer, payload any) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling payload: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handle(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
	return rec
}

func submissionErrors(t *testing.T, rec *httptest.ResponseRecorder) map[string][]string {
	t.Helper()

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	var payload struct {
		Errors map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response was not JSON: %v", err)
	}
	return payload.Errors
}

func stubSubmissionServer(insertErr error) (*submissionServer, *[]*calendar.Event) {
	inserted := &[]*calendar.Event{}
	return &submissionServer{
		insert: func(_ context.Context, event *calendar.Event) error {
			if insertErr != nil {
				return insertErr
			}
			*inserted = append(*inserted, event)
			return nil
		},
		limiter: newRateLimiter(100, time.Minute),
		now:     func() time.Time { return submissionNow },
	}, inserted
}

func TestSubmissionInsertsTheEvent(t *testing.T) {
	s, inserted := stubSubmissionServer(nil)

	rec := postSubmission(t, s, validSubmission())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if len(*inserted) != 1 {
		t.Fatalf("inserted %d events, want 1", len(*inserted))
	}

	event := (*inserted)[0]
	if event.Summary != "Concert on the Green" {
		t.Errorf("summary = %q", event.Summary)
	}
	// Wall-clock time with an explicit zone, exactly like the Sinatra app.
	if event.Start.DateTime != "2026-08-01T18:30:00" || event.Start.TimeZone != "America/New_York" {
		t.Errorf("start = %q %q", event.Start.DateTime, event.Start.TimeZone)
	}
	if event.End.DateTime != "2026-08-01T20:00:00" {
		t.Errorf("end = %q", event.End.DateTime)
	}
	// Moderator-only metadata lives in private extended properties.
	if event.ExtendedProperties.Private["email"] != "host@example.com" ||
		event.ExtendedProperties.Private["host"] != "Needham Circle" {
		t.Errorf("private properties = %v", event.ExtendedProperties.Private)
	}
	if event.Source == nil || event.Source.Url != "https://example.com/concert" {
		t.Errorf("source = %+v", event.Source)
	}
}

func TestSubmissionWithoutURLOmitsTheSourceBlock(t *testing.T) {
	s, inserted := stubSubmissionServer(nil)

	payload := validSubmission()
	delete(payload, "url")
	postSubmission(t, s, payload)

	if (*inserted)[0].Source != nil {
		t.Error("a blank url should not attach a source block")
	}
}

func TestSubmissionValidationMirrorsTheEventForm(t *testing.T) {
	s, _ := stubSubmissionServer(nil)

	errs := submissionErrors(t, postSubmission(t, s, map[string]string{
		"title":      "",
		"host":       "",
		"email":      "not-an-email",
		"start_time": "yesterday",
		"end_time":   "",
		"url":        "http://insecure.example",
	}))

	for field, want := range map[string]string{
		"title":      "Title is required.",
		"host":       "Name of host organization/business is required.",
		"email":      "Email must be a valid email address.",
		"start_time": "Start time is required to be a valid time.",
		"end_time":   "End time is required to be a valid time.",
		"url":        "URL must be a valid URL.",
	} {
		if len(errs[field]) == 0 || errs[field][0] != want {
			t.Errorf("errors[%q] = %v, want %q", field, errs[field], want)
		}
	}
}

func TestSubmissionRejectsPastAndInvertedTimes(t *testing.T) {
	s, _ := stubSubmissionServer(nil)

	payload := validSubmission()
	payload["start_time"] = "2026-06-01T18:30" // before submissionNow
	payload["end_time"] = "2026-06-01T17:30"
	errs := submissionErrors(t, postSubmission(t, s, payload))

	if errs["start_time"][0] != "Start time must be in the future." {
		t.Errorf("start errors = %v", errs["start_time"])
	}
	found := false
	for _, message := range errs["end_time"] {
		if message == "End time must be after start time." {
			found = true
		}
	}
	if !found {
		t.Errorf("end errors = %v, want the end-after-start message", errs["end_time"])
	}
}

func TestSubmissionEnforcesMaxLengths(t *testing.T) {
	s, _ := stubSubmissionServer(nil)

	payload := validSubmission()
	payload["title"] = strings.Repeat("x", 201)
	errs := submissionErrors(t, postSubmission(t, s, payload))

	if errs["title"][0] != "Title must be at most 200 characters." {
		t.Errorf("title errors = %v", errs["title"])
	}
}

func TestSubmissionHoneypotPretendsSuccess(t *testing.T) {
	s, inserted := stubSubmissionServer(nil)

	payload := validSubmission()
	payload["website"] = "https://spam.example"
	rec := postSubmission(t, s, payload)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(*inserted) != 0 {
		t.Error("a honeypot hit must not insert an event")
	}
}

func TestSubmissionTurnsInsertErrorsInto502(t *testing.T) {
	s, _ := stubSubmissionServer(errors.New("boom"))

	rec := postSubmission(t, s, validSubmission())
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestSubmissionRateLimits(t *testing.T) {
	s, _ := stubSubmissionServer(nil)
	s.limiter = newRateLimiter(1, time.Minute)

	postSubmission(t, s, validSubmission())
	rec := postSubmission(t, s, validSubmission())

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestSubmissionRejectsNonPost(t *testing.T) {
	s, _ := stubSubmissionServer(nil)

	rec := httptest.NewRecorder()
	s.handle(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
