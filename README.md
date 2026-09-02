# Needham Circle functions

The invocation-based Cloud Run functions behind https://needhamcircle.org.
the static site calls these from the browser for everything dynamic. Each
endpoint is its own deployed function; they all share this Go module and are
selected at deploy time with `--entry-point`.

| Endpoint             | Entry point        | Env vars                    |
| -------------------- | ------------------ | --------------------------- |
| events data (GET)    | `ListEvents`       | `EVENTS_CALENDAR_ID`        |
| event submit (POST)  | `CreateSubmission` | `SUBMISSIONS_CALENDAR_ID`   |
| contact form (POST)  | `SendContact`      | `SMTP_PASSWORD` (secret)    |

- `ListEvents` returns upcoming events as JSON (`{"events": [...]}`, with
  Google's own `start`/`end` payloads passed through untouched) and accepts a
  `q` search parameter, passed to the Calendar API.
- `CreateSubmission` and `SendContact` accept JSON bodies whose field names
  match the site's form inputs, return validation failures as 422 with
  `{"errors": {field: [...]}}` for the forms to render inline, carry a
  `website` honeypot field, and rate-limit to 5 requests per minute per IP —
  per instance, which is why they deploy with `--max-instances=1`. The
  client IP is the final `X-Forwarded-For` entry, the one Google's frontend
  appends, which is correct only while each function serves its run.app URL
  directly: a load balancer or CDN in front would append its own address as
  the final entry instead, so `clientIP` in ratelimit.go must learn about
  the extra hop before fronting the functions with one.
- All three answer CORS preflights and stamp CORS headers. The allowlist is
  baked in, nothing to configure: deployed (Cloud Run sets `K_SERVICE`) the
  functions accept https://needhamcircle.org (and www) plus
  https://needhamcircle.github.io, the GitHub Pages mirror, so the site can
  be tested there before it reaches the custom domain; running locally they
  accept the local Jekyll site (localhost:4000). Browsers on any other
  origin can't call them.
- `SendContact` also honors `SMTP_ACCOUNT` (default `needhamcircle@gmail.com`).

## Layout

The function entry points live in the root package (the layout the Cloud Run
functions buildpack expects); `cmd/devserver` is a local development server
that mounts all three on one port.

## Local development

```
EVENTS_CALENDAR_ID=... \
SUBMISSIONS_CALENDAR_ID=... \
SMTP_PASSWORD=... \
SERVICE_ACCOUNT_KEY=... \
PORT=8081 go run ./cmd/devserver
```

Endpoints: `/list-events`, `/create-submission`, `/send-contact` (these paths
exist only locally — deployed, each function has its own URL).
`SERVICE_ACCOUNT_KEY` (base64-encoded service account JSON) is only needed
locally: deployed, the functions authenticate as their runtime service
account via Application Default Credentials, so no key ships with them.

## Tests

```
go test ./...
```

## Deploying

```
gcloud functions deploy needham-circle-events \
  --gen2 --runtime=go127 --region=us-east1 --source=. \
  --entry-point=ListEvents --trigger-http --allow-unauthenticated \
  --service-account=CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --set-env-vars=EVENTS_CALENDAR_ID=...

gcloud functions deploy needham-circle-submit \
  --gen2 --runtime=go127 --region=us-east1 --source=. \
  --entry-point=CreateSubmission --trigger-http --allow-unauthenticated \
  --max-instances=1 \
  --service-account=CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --set-env-vars=SUBMISSIONS_CALENDAR_ID=...

gcloud functions deploy needham-circle-contact \
  --gen2 --runtime=go127 --region=us-east1 --source=. \
  --entry-point=SendContact --trigger-http --allow-unauthenticated \
  --max-instances=1 \
  --service-account=CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --set-secrets=SMTP_PASSWORD=needham-circle-smtp:latest
```
