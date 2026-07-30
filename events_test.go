package functions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/calendar/v3"
)

func timedEvent() *calendar.Event {
	return &calendar.Event{
		Summary:     "Concert on the Green",
		Description: "Bring a chair.",
		Location:    "Needham Common",
		Start:       &calendar.EventDateTime{DateTime: "2026-08-01T18:30:00-04:00", TimeZone: "America/New_York"},
		End:         &calendar.EventDateTime{DateTime: "2026-08-01T20:00:00-04:00", TimeZone: "America/New_York"},
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: map[string]string{"source": "volante-farms", "source_id": "123"},
		},
		Source: &calendar.EventSource{Title: "volante-farms", Url: "https://example.com/concert"},
	}
}

func stubServer(items []*calendar.Event, err error) *server {
	return &server{
		list: func(_ context.Context, _ string) ([]*calendar.Event, error) {
			return items, err
		},
	}
}

func decodeEvents(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var payload struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response was not JSON: %v (body %q)", err, rec.Body.String())
	}
	return payload.Events
}

func TestHandleReturnsTrimmedEventsWithPassthroughDates(t *testing.T) {
	rec := httptest.NewRecorder()
	stubServer([]*calendar.Event{timedEvent()}, nil).handle(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	events := decodeEvents(t, rec)
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	event := events[0]
	for field, want := range map[string]string{
		"title":       "Concert on the Green",
		"description": "Bring a chair.",
		"location":    "Needham Common",
		"source":      "volante-farms",
		"url":         "https://example.com/concert",
	} {
		if event[field] != want {
			t.Errorf("event[%q] = %v, want %q", field, event[field], want)
		}
	}

	start, ok := event["start"].(map[string]any)
	if !ok {
		t.Fatalf("event[\"start\"] = %v, want an object", event["start"])
	}
	if start["dateTime"] != "2026-08-01T18:30:00-04:00" {
		t.Errorf("start.dateTime = %v, want the raw RFC3339 value", start["dateTime"])
	}
	if start["timeZone"] != "America/New_York" {
		t.Errorf("start.timeZone = %v, want America/New_York", start["timeZone"])
	}
}

func TestHandlePassesAllDayDatesThrough(t *testing.T) {
	allDay := &calendar.Event{
		Summary: "Town Fair",
		Start:   &calendar.EventDateTime{Date: "2026-08-01"},
		End:     &calendar.EventDateTime{Date: "2026-08-03"},
	}

	rec := httptest.NewRecorder()
	stubServer([]*calendar.Event{allDay}, nil).handle(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	event := decodeEvents(t, rec)[0]
	start := event["start"].(map[string]any)
	if start["date"] != "2026-08-01" {
		t.Errorf("start.date = %v, want 2026-08-01", start["date"])
	}
	if _, present := start["dateTime"]; present {
		t.Error("start.dateTime should be omitted for all-day events")
	}
}

func TestHandleReturnsAnEmptyArrayNotNull(t *testing.T) {
	rec := httptest.NewRecorder()
	stubServer(nil, nil).handle(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response was not JSON: %v", err)
	}
	if string(payload["events"]) != "[]" {
		t.Errorf("events = %s, want []", payload["events"])
	}
}

func TestHandleForwardsTheSearchQuery(t *testing.T) {
	var got string
	s := &server{
		list: func(_ context.Context, query string) ([]*calendar.Event, error) {
			got = query
			return nil, nil
		},
	}

	s.handle(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?q=concert", nil))

	if got != "concert" {
		t.Errorf("query = %q, want %q", got, "concert")
	}
}

func TestHandleTurnsListerErrorsInto502(t *testing.T) {
	rec := httptest.NewRecorder()
	stubServer(nil, errors.New("boom")).handle(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHandleRejectsNonGetMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	stubServer(nil, nil).handle(rec, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestHandleRequiresTheApiKeyWhenConfigured(t *testing.T) {
	s := stubServer([]*calendar.Event{timedEvent()}, nil)
	s.apiKey = "sekrit"

	missing := httptest.NewRecorder()
	s.handle(missing, httptest.NewRequest(http.MethodGet, "/", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Errorf("missing key: status = %d, want 401", missing.Code)
	}

	wrong := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Api-Key", "guess")
	s.handle(wrong, request)
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: status = %d, want 401", wrong.Code)
	}

	right := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Api-Key", "sekrit")
	s.handle(right, request)
	if right.Code != http.StatusOK {
		t.Errorf("right key: status = %d, want 200", right.Code)
	}
}
