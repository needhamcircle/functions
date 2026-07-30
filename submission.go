package functions

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"google.golang.org/api/calendar/v3"
)

func init() {
	functions.HTTP("CreateSubmission", CreateSubmission)
}

// Submitted times are Needham wall-clock strings from datetime-local inputs
// ("2006-01-02T15:04"); they are interpreted in this zone and stored on the
// event with it, regardless of where the function runs.
const submissionTimeZone = "America/New_York"

var eastern = sync.OnceValue(func() *time.Location {
	location, err := time.LoadLocation(submissionTimeZone)
	if err != nil {
		// time/tzdata is embedded (see service.go), so this cannot happen.
		panic(err)
	}
	return location
})

// submissionRequest's field names match the site form's input names, so the
// form serializes as-is. Website is a honeypot: a hidden input humans never
// see, so a filled value marks a bot.
type submissionRequest struct {
	Title       string `json:"title"`
	Host        string `json:"host"`
	Description string `json:"description"`
	Location    string `json:"location"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	URL         string `json:"url"`
	Email       string `json:"email"`
	Website     string `json:"website"`
}

// Every value is stripped before validating, so whitespace-only input fails
// the required checks.
func (r *submissionRequest) trim() {
	r.Title = strings.TrimSpace(r.Title)
	r.Host = strings.TrimSpace(r.Host)
	r.Description = strings.TrimSpace(r.Description)
	r.Location = strings.TrimSpace(r.Location)
	r.URL = strings.TrimSpace(r.URL)
	r.Email = strings.TrimSpace(r.Email)
}

type submissionServer struct {
	insert  func(ctx context.Context, event *calendar.Event) error
	limiter *rateLimiter
	now     func() time.Time
}

func (s *submissionServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.limiter.allow(clientIP(r)) {
		tooManyRequests(w, "60")
		return
	}

	var req submissionRequest
	if !decodeForm(w, r, &req) {
		return
	}

	// A filled honeypot gets a success response so the bot moves on, but
	// nothing is saved.
	if strings.TrimSpace(req.Website) != "" {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	req.trim()
	errors, start, end := validateSubmission(&req, s.now())
	if len(errors) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errors})
		return
	}

	if err := s.insert(r.Context(), submissionEvent(&req, start, end)); err != nil {
		log.Printf("creating submission: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "We couldn't save your submission. Please try again later.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validateSubmission applies the field rules; the messages render inline
// under the site form's fields.
func validateSubmission(req *submissionRequest, now time.Time) (fieldErrors, time.Time, time.Time) {
	errors := fieldErrors{}

	requireString(errors, "title", "Title", req.Title, true, 200)
	requireString(errors, "host", "Name of host organization/business", req.Host, true, 200)
	requireString(errors, "description", "Description", req.Description, false, 2000)
	requireString(errors, "location", "Location", req.Location, false, 200)

	start := timeField(errors, "start_time", "Start time", req.StartTime, now)
	end := timeField(errors, "end_time", "End time", req.EndTime, now)
	if !start.IsZero() && !end.IsZero() && !end.After(start) {
		errors.add("end_time", "End time must be after start time.")
	}

	requireString(errors, "url", "URL", req.URL, false, 500)
	if req.URL != "" && !strings.HasPrefix(req.URL, "https://") {
		errors.add("url", "URL must be a valid URL.")
	}

	requireString(errors, "email", "Email", req.Email, true, 200)
	if req.Email != "" && !emailPattern.MatchString(req.Email) {
		errors.add("email", "Email must be a valid email address.")
	}

	return errors, start, end
}

func requireString(errors fieldErrors, field, human, value string, required bool, maxLength int) {
	if required && value == "" {
		errors.add(field, human+" is required.")
	}
	if utf8.RuneCountInString(value) > maxLength {
		errors.add(field, fmt.Sprintf("%s must be at most %d characters.", human, maxLength))
	}
}

// timeField parses a datetime-local value in Needham's zone; missing or
// unparseable values and past times are rejected.
func timeField(errors fieldErrors, field, human, value string, now time.Time) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02T15:04", value, eastern())
	if err != nil {
		errors.add(field, human+" is required to be a valid time.")
		return time.Time{}
	}
	if !parsed.After(now) {
		errors.add(field, human+" must be in the future.")
	}
	return parsed
}

// submissionEvent builds the calendar event: wall-clock times with an
// explicit zone, the submitter's contact email and host organization in
// moderator-only private extended properties (they never surface publicly),
// and a source block only when there is a URL to link to (Google rejects one
// with a blank url).
func submissionEvent(req *submissionRequest, start, end time.Time) *calendar.Event {
	event := &calendar.Event{
		Summary:     req.Title,
		Description: req.Description,
		Location:    req.Location,
		Start:       &calendar.EventDateTime{DateTime: start.Format("2006-01-02T15:04:05"), TimeZone: submissionTimeZone},
		End:         &calendar.EventDateTime{DateTime: end.Format("2006-01-02T15:04:05"), TimeZone: submissionTimeZone},
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: map[string]string{"email": req.Email, "host": req.Host},
		},
	}

	if req.URL != "" {
		event.Source = &calendar.EventSource{Title: "Event website", Url: req.URL}
	}

	return event
}

var defaultSubmissionServer = sync.OnceValues(func() (*submissionServer, error) {
	calendarID := os.Getenv("SUBMISSIONS_CALENDAR_ID")
	if calendarID == "" {
		return nil, fmt.Errorf("SUBMISSIONS_CALENDAR_ID is not set")
	}

	service, err := calendarService()
	if err != nil {
		return nil, err
	}

	return &submissionServer{
		insert: func(ctx context.Context, event *calendar.Event) error {
			_, err := service.Events.Insert(calendarID, event).Context(ctx).Do()
			return err
		},
		limiter: newRateLimiter(5, time.Minute),
		now:     time.Now,
	}, nil
})

// CreateSubmission is the deployed entry point (and the handler cmd/main.go
// mounts for local development).
func CreateSubmission(w http.ResponseWriter, r *http.Request) {
	if handleCORS(w, r) {
		return
	}

	s, err := defaultSubmissionServer()
	if err != nil {
		log.Printf("configuring submission function: %v", err)
		http.Error(w, "server misconfigured", http.StatusInternalServerError)
		return
	}

	s.handle(w, r)
}
