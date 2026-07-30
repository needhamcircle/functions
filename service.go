package functions

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"

	// Submitted event times are Needham wall-clock; embed the zone database so
	// America/New_York loads regardless of the runtime image's zoneinfo.
	_ "time/tzdata"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// calendarService builds the shared Calendar client once per instance, with
// the same read-write events scope the Sinatra app uses (ListEvents reads,
// CreateSubmission writes). Deployed, it authenticates as the runtime service
// account via Application Default Credentials — no key material ships with
// the function. The base64 SERVICE_ACCOUNT_KEY fallback matches the Ruby
// app's env var for local runs outside Google's environment.
var calendarService = sync.OnceValues(func() (*calendar.Service, error) {
	opts := []option.ClientOption{option.WithScopes(calendar.CalendarEventsScope)}
	if key := os.Getenv("SERVICE_ACCOUNT_KEY"); key != "" {
		decoded, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("decoding SERVICE_ACCOUNT_KEY: %w", err)
		}
		opts = append(opts, option.WithCredentialsJSON(decoded))
	}

	service, err := calendar.NewService(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("creating calendar service: %w", err)
	}
	return service, nil
})
