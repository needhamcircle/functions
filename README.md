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
  per instance, which is why they deploy with `--max-instances=1`.
- All three answer CORS preflights and stamp CORS headers. The allowlist is
  baked in, nothing to configure: deployed (Cloud Run sets `K_SERVICE`) the
  functions accept https://needhamcircle.org (and www); running locally they
  accept the local Jekyll site (localhost:4000).
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

One-time project setup (the project must be the one whose service account the
calendars are shared with):

```
gcloud auth login
gcloud config set project PROJECT_ID
gcloud services enable cloudfunctions.googleapis.com run.googleapis.com \
  cloudbuild.googleapis.com artifactregistry.googleapis.com
```

Deploy from this repo's root — `--runtime`: pick the newest Go from
`gcloud functions runtimes list --region=us-east1` (at least the `go` version
declared in go.mod), and `--service-account` must be the account the
calendars are shared with:

```
gcloud functions deploy needham-circle-events \
  --gen2 --runtime=go125 --region=us-east1 --source=. \
  --entry-point=ListEvents --trigger-http --allow-unauthenticated \
  --service-account=CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --set-env-vars=EVENTS_CALENDAR_ID=...

gcloud functions deploy needham-circle-submit \
  --gen2 --runtime=go125 --region=us-east1 --source=. \
  --entry-point=CreateSubmission --trigger-http --allow-unauthenticated \
  --max-instances=1 \
  --service-account=CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --set-env-vars=SUBMISSIONS_CALENDAR_ID=...

gcloud functions deploy needham-circle-contact \
  --gen2 --runtime=go125 --region=us-east1 --source=. \
  --entry-point=SendContact --trigger-http --allow-unauthenticated \
  --max-instances=1 \
  --service-account=CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --set-secrets=SMTP_PASSWORD=needham-circle-smtp:latest
```

The SMTP app password should live in Secret Manager rather than a plain env
var, and the runtime service account must be able to read it:

```
printf '%s' 'the-app-password' | gcloud secrets create needham-circle-smtp --data-file=-
gcloud secrets add-iam-policy-binding needham-circle-smtp \
  --member=serviceAccount:CALENDAR_SA@PROJECT_ID.iam.gserviceaccount.com \
  --role=roles/secretmanager.secretAccessor
``` After deploying, put the three function URLs into the main
repo's `_config.yml`. All three scale to zero between requests; at this
site's traffic they stay inside the free tier.
