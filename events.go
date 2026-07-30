// Package functions holds the invocation-based Cloud Run functions behind
// the Needham Circle site. Every function registers its own entry point
// here; deploys share this module and select one with --entry-point (see
// README.md).
package functions

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"google.golang.org/api/calendar/v3"
)

func init() {
	functions.HTTP("ListEvents", ListEvents)
}

// One page of upcoming events. The site treats a response of exactly this
// size as truncated (its calendar view drops the trailing month), so the two
// must agree on the cap.
const maxResults = 250

// eventJSON is the trimmed event shape the site renders. Start/End pass
// Google's own dateTime/date payloads through untouched so date semantics —
// exclusive all-day ends, timezone offsets — reach the client intact.
type eventJSON struct {
	Title       string                  `json:"title"`
	Description string                  `json:"description,omitempty"`
	Location    string                  `json:"location,omitempty"`
	Source      string                  `json:"source,omitempty"`
	URL         string                  `json:"url,omitempty"`
	Start       *calendar.EventDateTime `json:"start"`
	End         *calendar.EventDateTime `json:"end"`
}

type listResponse struct {
	Events []eventJSON `json:"events"`
}

// eventLister fetches the upcoming events for a search query; the real one
// calls the Google Calendar API and tests substitute a stub.
type eventLister func(ctx context.Context, query string) ([]*calendar.Event, error)

type server struct {
	list eventLister
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	items, err := s.list(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		log.Printf("listing events: %v", err)
		http.Error(w, "failed to load events", http.StatusBadGateway)
		return
	}

	events := make([]eventJSON, 0, len(items))
	for _, item := range items {
		events = append(events, viewOf(item))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(listResponse{Events: events}); err != nil {
		log.Printf("encoding response: %v", err)
	}
}

func viewOf(event *calendar.Event) eventJSON {
	view := eventJSON{
		Title:       event.Summary,
		Description: event.Description,
		Location:    event.Location,
		Start:       event.Start,
		End:         event.End,
	}

	if event.ExtendedProperties != nil {
		view.Source = event.ExtendedProperties.Private["source"]
	}

	// The URL passes through unfiltered; EventView owns the presentation rule
	// that only http(s) links render.
	if event.Source != nil {
		view.URL = event.Source.Url
	}

	return view
}

// defaultServer builds the production server once per instance, on the first
// request rather than at cold start, so a misconfiguration surfaces as a
// logged 500 instead of a crash loop.
var defaultServer = sync.OnceValues(func() (*server, error) {
	calendarID := os.Getenv("EVENTS_CALENDAR_ID")
	if calendarID == "" {
		return nil, fmt.Errorf("EVENTS_CALENDAR_ID is not set")
	}

	service, err := calendarService()
	if err != nil {
		return nil, err
	}

	return &server{
		list: func(ctx context.Context, query string) ([]*calendar.Event, error) {
			call := service.Events.List(calendarID).
				SingleEvents(true).
				OrderBy("startTime").
				TimeMin(time.Now().Format(time.RFC3339)).
				MaxResults(maxResults).
				Context(ctx)
			if query != "" {
				call = call.Q(query)
			}

			events, err := call.Do()
			if err != nil {
				return nil, err
			}
			return events.Items, nil
		},
	}, nil
})

// ListEvents is the deployed entry point (and the handler cmd/main.go mounts
// for local development).
func ListEvents(w http.ResponseWriter, r *http.Request) {
	if handleCORS(w, r) {
		return
	}

	s, err := defaultServer()
	if err != nil {
		log.Printf("configuring events function: %v", err)
		http.Error(w, "server misconfigured", http.StatusInternalServerError)
		return
	}

	s.handle(w, r)
}
