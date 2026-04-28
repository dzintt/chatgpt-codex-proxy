# chatgpt-codex-proxy

`chatgpt-codex-proxy` is a small Go service that lets standard OpenAI clients talk to ChatGPT Codex accounts.

It exposes an OpenAI-compatible API, translates requests into the private `chatgpt.com/backend-api/codex/*` format, and manages one or more locally authenticated Codex accounts.

The upstream Codex surface is private and undocumented. It may change at any time. This project is best suited for local or small-scale deployments.

## Quick Start

Recommended path: Docker Compose.

Requirements:

- Docker Desktop or Docker Engine, or Go `1.26.x`
- A valid `PROXY_API_KEY`
- At least one Codex account before serving public model requests

### 1. Configure

```bash
cp .env.example .env
```

Required:

```env
PROXY_API_KEY=change-me-to-a-long-random-string
```

Optional:

- `PORT`
  Default: `8080`
- `DATA_DIR`
  Default: `data` locally, `/app/data` in Docker
- `DEBUG_LOG_PAYLOADS`
  Default: `false`. Logs raw public request JSON and translated upstream payloads.

Examples below assume:

```bash
export PROXY_URL=http://localhost:8080
export PROXY_API_KEY=change-me-to-a-long-random-string
```

### 2. Start the proxy

Docker Compose:

```bash
docker compose up -d --build
```

Useful commands:

```bash
docker compose logs -f
docker compose down
```

Direct Go run:

```bash
go run ./cmd/api
```

### 3. Add a Codex account

Start device login:

```bash
curl -sS -X POST "${PROXY_URL}/admin/accounts/device-login/start" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

Open the returned `auth_url`, complete the login flow, then poll:

```bash
curl -sS "${PROXY_URL}/admin/accounts/device-login/<login_id>" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

When `status` becomes `ready`, the account is saved locally.

### 4. Test the proxy

Chat Completions:

```bash
curl -sS "${PROXY_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.5",
    "messages": [
      { "role": "system", "content": "Be concise." },
      { "role": "user", "content": "Explain what this repository does." }
    ]
  }'
```

Responses:

```bash
curl -sS "${PROXY_URL}/v1/responses" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.5",
    "input": "Summarize this project in three bullet points."
  }'
```

Responses compact:

```bash
curl -sS "${PROXY_URL}/v1/responses/compact" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.5",
    "input": [
      {
        "role": "assistant",
        "phase": "output",
        "content": [{"type": "output_text", "text": "Long prior answer"}]
      },
      {
        "role": "user",
        "content": "Compact this thread for the next turn."
      }
    ]
  }'
```

### 5. Point an OpenAI client at it

- Base URL: `http://localhost:8080/v1`
- API key: your `PROXY_API_KEY`

## Authentication

Every route except `GET /health/live` requires the proxy API key.

Accepted headers:

- `Authorization: Bearer <PROXY_API_KEY>`
- `X-API-Key: <PROXY_API_KEY>`

The same key protects both public and admin routes.

## Public API

Routes:

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/responses/compact`
- `GET /v1/models`
- `GET /v1/models/:model_id`
- `GET /health/live`
- `GET /health`

Supported behavior:

- Streaming and non-streaming responses
- Tool calling, including custom tools
- Legacy Chat Completions `functions` and `function_call`
- Hosted web search passthrough
- Structured outputs, including `json_schema` and `json_object`
- Text, image, and file inputs
- Reasoning support
- Explicit `previous_response_id` continuation
- Explicit OpenAI-style `response.compaction` support on `/v1/responses/compact`
- Guarded implicit continuation when prior assistant or tool history is replayed
- Runtime model catalog backed by the upstream Codex model list

Important notes:

- `/v1/chat/completions` also accepts a Responses-shaped body when `messages` is omitted.
- `/v1/responses/compact` follows the public OpenAI contract and returns `object: "response.compaction"` instead of raw Codex JSON.
- `/v1/responses/compact` supports explicit `previous_response_id` by expanding locally stored continuation history before calling the private compact backend.
- Compact requests currently support the same text, image, file, reasoning, tool-call, tool-output, and compaction input items used elsewhere in the proxy. Audio input parts are rejected with `unsupported_content_part`.
- Continuations are pinned to the original account when possible.
- When the proxy can derive a stable conversation key, it sets `prompt_cache_key` upstream automatically.
- Some OpenAI compatibility fields are accepted and ignored. See [docs/TRANSLATION.md](docs/TRANSLATION.md) for exact behavior.

## Admin API

Routes:

- `GET /admin/accounts`
- `POST /admin/accounts/device-login/start`
- `GET /admin/accounts/device-login/:login_id`
- `DELETE /admin/accounts/:account_id`
- `PATCH /admin/accounts/:account_id`
- `GET /admin/accounts/:account_id/usage`
- `POST /admin/accounts/:account_id/refresh`
- `GET /admin/rotation`
- `PUT /admin/rotation`

What it does:

- Lists locally known accounts, status, eligibility, cooldown state, and cached quota
- Starts and polls device login
- Removes accounts or updates `label` / `status`
- Refreshes OAuth tokens
- Fetches runtime or cached quota data
- Shows or changes the global rotation strategy

Allowed rotation strategies:

- `least_used`
- `round_robin`
- `sticky`

Account status values:

- `active`
- `disabled`
- `expired`
- `banned`

General routing is blocked by permanent status, active cooldown, missing token, or exhausted primary / secondary quota. `code_review_rate_limit` is kept for observability and does not affect normal routing.

## Deployment and Persistence

The repository includes:

- `compose.yaml`
  Recommended deployment. Persists data in the `chatgpt-codex-proxy-data` volume and runs the service with basic hardening.
- `Dockerfile`
  Multi-stage image build for the API server.

Local state is stored in:

- `${DATA_DIR}/accounts.json`
- `${DATA_DIR}/models-cache.json`

Persisted data includes accounts, OAuth tokens, labels, status flags, cached quota, cooldown state, and the last successful model catalog snapshot.

In-memory only:

- Continuation mappings and conversation affinity
- In-flight device-login coordination

## How It Works

1. The Gin server accepts OpenAI-style requests.
2. Both public request styles are normalized into one internal request model.
3. The proxy selects a ready account, translates the request, and sends it to the private Codex backend over HTTP SSE or WebSocket.
4. Upstream events are converted back into OpenAI-style JSON or SSE.

Architecture overview:

<img width="4599" height="2073" alt="chatgpt-codex-proxy architecture flowchart" src="https://github.com/user-attachments/assets/05cd8446-dd4b-43bc-a3fc-eb370ad917e6" />

The diagram shows the per-request translation path, local account state, and the one-time device-login onboarding flow.

The proxy talks to:

- `POST https://chatgpt.com/backend-api/codex/responses`
- `POST https://chatgpt.com/backend-api/codex/responses/compact`
- `GET https://chatgpt.com/backend-api/codex/usage`
- `GET https://chatgpt.com/backend-api/codex/models`
- `WSS https://chatgpt.com/backend-api/codex/responses`
- `https://auth.openai.com/api/accounts/deviceauth/*`
- `https://auth.openai.com/oauth/token`

## Testing

Unit tests:

```bash
go test ./...
```

Live compatibility tests against a running local proxy:

```bash
OPENAI_API_KEY=change-me-to-a-long-random-string \
OPENAI_MODEL=gpt-5.5 \
OPENAI_BASE_URL="${PROXY_URL}/v1" \
go test -tags=live ./internal/integration -v -count=1
```

## Docs

For the lower-level details moved out of this README:

- [docs/TRANSLATION.md](docs/TRANSLATION.md)
  Exact OpenAI-to-Codex translation behavior and compatibility rules
- [docs/MULTI_ACCOUNT_ROTATION_STRATEGY.md](docs/MULTI_ACCOUNT_ROTATION_STRATEGY.md)
  Detailed account selection and quota-routing behavior
- [docs/CODEX_API_DOCS.md](docs/CODEX_API_DOCS.md)
  Private upstream Codex API behavior as inferred from this codebase

## Limitations

- The upstream Codex backend is private and may change without notice.
- Device-auth is the only onboarding flow.
- The implementation is intentionally small and does not aim to cover every edge case of the public OpenAI platform.
- Continuation state is in memory only and expires with the configured continuation TTL.
