# chatgpt-codex-proxy

`chatgpt-codex-proxy` is a small Go service that lets standard OpenAI clients talk to ChatGPT Codex accounts.

It exposes an OpenAI-compatible API, translates those requests into the private ChatGPT Codex backend format, and manages one or more locally authenticated Codex accounts.

This project depends on the private `chatgpt.com/backend-api/codex/*` surface. That surface is undocumented and may change at any time.

Use it for local or small-scale deployments.

## Contents

- [Quick Start](#quick-start)
- [Authentication](#authentication)
- [Configuration](#configuration)
- [Public API](#public-api)
- [Admin API](#admin-api)
- [Docker Deployment](#docker-deployment)
- [Persistence](#persistence)
- [What This Project Does](#what-this-project-does)
- [How It Works](#how-it-works)
- [Project Layout](#project-layout)
- [Account Rotation](#account-rotation)
- [Observability](#observability)
- [Testing](#testing)
- [Limitations](#limitations)

## Quick Start

Recommended path: Docker Compose.

### Requirements

If you want the recommended Docker setup:

- Docker Desktop or Docker Engine
- A valid `PROXY_API_KEY`
- At least one Codex account before the proxy can serve public model requests

If you want to run it directly with Go instead:

- Go `1.26.x`
- A valid `PROXY_API_KEY`
- At least one Codex account before the proxy can serve public model requests

### 1. Configure the proxy

Start from the example file:

```bash
cp .env.example .env
```

Required:

```env
PROXY_API_KEY=change-me
```

Optional:

- `PORT`
  Overrides the default listen port `8080`.
- `DATA_DIR`
  Overrides the default local state directory. Defaults to `data` for local runs. The Docker setup forces this to `/app/data`.
- `DEBUG_LOG_PAYLOADS`
  When set to `true`, logs the raw incoming JSON body sent to the proxy and the translated upstream Codex payload. Leave it off outside local debugging because it logs full request contents.

`.env.example` is intentionally minimal:

```env
PROXY_API_KEY=change-me-to-a-long-random-string
# PORT=8080
# DEBUG_LOG_PAYLOADS=false
```

`docker compose` reads `.env` automatically for variable substitution.

All examples below assume a POSIX-compatible shell with these variables set:

```bash
export PROXY_URL=http://localhost:8080
export PROXY_API_KEY=change-me
```

### 2. Start the proxy

Recommended: Docker Compose

```bash
docker compose up -d --build
```

Useful container commands:

```bash
docker compose logs -f
docker compose down
```

That setup:

- Builds the image from this repository
- Exposes the API on `http://localhost:8080`
- Persists account state in the named Docker volume `chatgpt-codex-proxy-data`
- Runs the service as a non-root user with a read-only root filesystem plus a writable data volume

Alternative: run it directly with Go

```bash
go run ./cmd/api
```

By default the server listens on `:8080` and stores local state in `./data` locally or `/app/data` in Docker.

### 3. Add a Codex account

Start a device login:

```bash
curl -sS -X POST "${PROXY_URL}/admin/accounts/device-login/start" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

The response includes:

- `login_id`
- `auth_url`
- `user_code`
- `status`

Open `auth_url`, complete the login flow, then poll for completion:

```bash
curl -sS "${PROXY_URL}/admin/accounts/device-login/<login_id>" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

When the login status becomes `ready`, the account has been saved locally and can be used for proxy requests.

### 4. Test the proxy

Example Chat Completions request:

```bash
curl -sS "${PROXY_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      { "role": "system", "content": "Be concise." },
      { "role": "user", "content": "Explain what this repository does." }
    ]
  }'
```

Example Responses request:

```bash
curl -sS "${PROXY_URL}/v1/responses" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "input": "Summarize this project in three bullet points."
  }'
```

### 5. Point an OpenAI client at the proxy

- Base URL: `http://localhost:8080/v1`
- API key: your `PROXY_API_KEY`

## Authentication

Every route except `GET /health/live` requires the proxy API key.

The proxy accepts either:

- `Authorization: Bearer <PROXY_API_KEY>`
- `X-API-Key: <PROXY_API_KEY>`

The same API key protects both public and admin routes.

## Configuration

Supported environment variables:

- `PROXY_API_KEY`
  Required. Protects both public and admin routes.
- `PORT`
  Optional. Defaults to `8080`.
- `DATA_DIR`
  Optional. Defaults to `data` for local runs.
- `DEBUG_LOG_PAYLOADS`
  Optional. Defaults to `false`. When enabled, emits structured logs for incoming public API JSON bodies and translated upstream Codex request payloads.

Everything else is fixed in code on purpose:

- The configured default model starts as `gpt-5.4`, but request validation and `/v1/models` are driven by the runtime catalog rather than a hardcoded public model list.
- A fresh data store starts with the `least_used` rotation strategy. Changes made through `PUT /admin/rotation` are persisted and restored on restart.
- Upstream base URLs, OAuth client details, request timeouts, fallback cooldowns, and desktop-like headers are implementation constants rather than deployment knobs.

## Public API

Summary:

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `GET /v1/models`
- `GET /v1/models/<model_id>`
- `GET /health/live`
- `GET /health`

### `POST /v1/chat/completions`

Accepts OpenAI Chat Completions requests and translates them into the upstream Codex request shape.

```bash
curl -sS "${PROXY_URL}/v1/chat/completions" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "messages": [
      { "role": "system", "content": "Be concise." },
      { "role": "user", "content": "Explain what this repository does." }
    ]
  }'
```

Supported behavior:

- Streaming and non-streaming
- `system` and `developer` instructions
- Tool calling via `tools`
- Custom tools, including grammar-based tools such as `ApplyPatch`
- Legacy `functions` and top-level `function_call` request compatibility
- Hosted web search passthrough
- Structured outputs via `response_format.type = "json_schema"` and `response_format.type = "json_object"`
- Reasoning effort
- Reasoning summaries surfaced as `reasoning_content` when the client requested reasoning via `reasoning_effort`
- Text, image, and file input parts
- Explicit `previous_response_id` continuation
- Guarded implicit continuation when the request replays prior assistant or tool history
- A compatibility path that accepts a Responses-shaped request body on this endpoint when `messages` is omitted

Compatibility notes:

- Legacy requests are normalized onto the modern tool-calling response shape; responses still use `tool_calls` rather than the older assistant `function_call` field.
- Native OpenAI custom tools use `type = "custom"`. See the [latest-model guide](https://developers.openai.com/api/docs/guides/latest-model) for the documented custom tool definition shape.
- For streaming Chat Completions responses, the proxy currently emits custom tool calls as function-shaped `tool_calls` deltas for broader client compatibility on the chat-completions streaming path.
- Non-streaming Chat Completions responses currently preserve the native custom tool-call shape. If your client depends on `stream = false` and expects the same compatibility behavior as the streaming path, treat that as a current limitation.
- Chat usage is returned in OpenAI Chat Completions shape: `prompt_tokens`, `completion_tokens`, `total_tokens`, plus token-detail objects when known.
- `previous_response_id` on Chat Completions now uses the upstream WebSocket continuation path instead of local full-history replay.
- When the proxy can derive a stable conversation key, it sets `prompt_cache_key` upstream automatically.
- Implicit resume is opportunistic. If the continuation guards fail or the continuation transport cannot be opened before streaming starts, the proxy falls back to the original full request.
- If both `messages` and Responses-style fields are present on this endpoint, `messages` wins.
- The implementation does not try to honor every OpenAI Chat Completions tuning field. Known unsupported fields are accepted for compatibility and ignored by the proxy. They are not currently surfaced to clients, and no compatibility-warning log is emitted.
- Chat Completions fields currently ignored by the proxy: `n`, `temperature`, `top_p`, `max_tokens`, `presence_penalty`, `frequency_penalty`, `stop`, `user`, `parallel_tool_calls`, `stream_options`, and `service_tier`.
- `system`, `developer`, and `tool` message content are effectively text-only. Non-text content in those roles is rejected with `400`.

### `POST /v1/responses`

Accepts OpenAI Responses requests and translates them into the upstream Codex request shape.

```bash
curl -sS "${PROXY_URL}/v1/responses" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4",
    "input": "Summarize this project in three bullet points."
  }'
```

Supported behavior:

- Streaming and non-streaming
- Tools, including the modern Responses API function tool shape
- Structured outputs
- Text, image, and file inputs
- Reasoning items in `input[]`, including `summary`, `content`, and `encrypted_content`
- Explicit `previous_response_id` continuation
- Guarded implicit continuation when the request replays prior assistant or tool history
- Follow-up turns that replay prior `output_text` items from the OpenAI Responses shape

Compatibility notes:

- Native Responses reasoning summary events are passed through as-is.
- When the proxy can derive a stable conversation key, it sets `prompt_cache_key` upstream automatically.
- Implicit resume is opportunistic. If the continuation guards fail or the continuation transport cannot be opened before streaming starts, the proxy falls back to the original full request.
- Structured-output schemas are normalized before they are sent upstream, including tuple-schema handling and stricter object-shape normalization for Codex compatibility.
- Known unsupported fields are accepted for compatibility and ignored by the proxy. They are not currently surfaced to clients, and no compatibility-warning log is emitted.
- Responses fields currently ignored by the proxy: `temperature`, `top_p`, `max_output_tokens`, `parallel_tool_calls`, `store`, `background`, `user`, `metadata`, `stream_options`, and `service_tier`.

### `GET /v1/models`

Returns the current upstream-backed model catalog in the standard OpenAI model-list shape.

```bash
curl -sS "${PROXY_URL}/v1/models" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

Notes:

- Model IDs mirror upstream exactly. The proxy does not add local aliases such as `codex`.
- A fresh startup begins with the latest cached catalog in `${DATA_DIR}/models-cache.json` when present, otherwise with a small bootstrap fallback list, then refreshes the catalog asynchronously from `GET /codex/models`.
- The latest successful catalog snapshot is cached locally in `${DATA_DIR}/models-cache.json` so `/v1/models` stays populated across restarts.

### `GET /v1/models/<model_id>`

Returns one model object in the OpenAI model shape for one known runtime model ID. Unknown IDs return an OpenAI-style `model_not_found` error.

```bash
curl -sS "${PROXY_URL}/v1/models/gpt-5.4" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

### `GET /health/live`

Unauthenticated liveness endpoint.

```bash
curl -sS "${PROXY_URL}/health/live"
```

### `GET /health`

Authenticated service health endpoint.

```bash
curl -sS "${PROXY_URL}/health" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

The response includes:

- `status`
- `accounts`
- `rotation`
- `continuations`
- `default_model`
- `codex_base_url`
- `request_timeout`
- `continuation_ttl`

## Admin API

The examples below assume you set an account ID and, when polling device login, a login ID:

```bash
export ACCOUNT_ID=acct_example
export LOGIN_ID=login_example
```

### Accounts

- `GET /admin/accounts`
  List locally known accounts, permanent status, derived eligibility, cooldown state, and cached quota.
- `DELETE /admin/accounts/:account_id`
  Remove an account from local persistence.
- `PATCH /admin/accounts/:account_id`
  Update mutable fields such as `label` or `status`. Allowed `status` values are `active` and `disabled`.
- `POST /admin/accounts/:account_id/refresh`
  Force an OAuth token refresh.
- `GET /admin/accounts/:account_id/usage`
  Fetch the runtime quota view and cached quota metadata for one account.
- `GET /admin/accounts/:account_id/usage?cached=true`
  Return the cached-only quota view without calling the upstream `/codex/usage` endpoint. The response includes `cached_quota`, `quota_runtime`, `quota_source`, and `quota_fetched_at`.

```bash
curl -sS "${PROXY_URL}/admin/accounts" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

```bash
curl -sS -X DELETE "${PROXY_URL}/admin/accounts/${ACCOUNT_ID}" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

```bash
curl -sS -X PATCH "${PROXY_URL}/admin/accounts/${ACCOUNT_ID}" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "label": "primary",
    "status": "active"
  }'
```

```bash
curl -sS -X POST "${PROXY_URL}/admin/accounts/${ACCOUNT_ID}/refresh" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

```bash
curl -sS "${PROXY_URL}/admin/accounts/${ACCOUNT_ID}/usage" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

```bash
curl -sS "${PROXY_URL}/admin/accounts/${ACCOUNT_ID}/usage?cached=true" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

### Device login

- `POST /admin/accounts/device-login/start`
  Start a device login flow.
- `GET /admin/accounts/device-login/:login_id`
  Poll a device login flow.

```bash
curl -sS -X POST "${PROXY_URL}/admin/accounts/device-login/start" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

```bash
curl -sS "${PROXY_URL}/admin/accounts/device-login/${LOGIN_ID}" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

### Rotation

- `GET /admin/rotation`
  Show the current rotation strategy.
- `PUT /admin/rotation`
  Change the rotation strategy.

```bash
curl -sS "${PROXY_URL}/admin/rotation" \
  -H "Authorization: Bearer ${PROXY_API_KEY}"
```

```bash
curl -sS -X PUT "${PROXY_URL}/admin/rotation" \
  -H "Authorization: Bearer ${PROXY_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "strategy": "least_used"
  }'
```

Valid strategies:

- `least_used`
- `round_robin`
- `sticky`

### Status model

- Permanent account status is one of `active`, `disabled`, `expired`, or `banned`.
- Transient routing availability is tracked with `cooldown_until` plus the latest cached quota snapshot.
- General account routing is blocked only by primary or secondary quota exhaustion. `code_review_rate_limit` is retained for observability and does not affect normal routing.
- Exhausted quota windows are treated as temporary routing blocks. Accounts automatically become eligible again after the cached reset time passes.

## Docker Deployment

The repository includes:

- `Dockerfile`
  Multi-stage build that compiles the Go binary and runs it in a small Alpine image as a non-root user.
- `compose.yaml`
  Starts the proxy with `PROXY_API_KEY`, defaulted `PORT` and `DEBUG_LOG_PAYLOADS`, forces `DATA_DIR=/app/data`, publishes the configured port, mounts a persistent Docker volume for `/app/data`, and adds basic runtime hardening.
- `.dockerignore`
  Keeps build context small and avoids copying local state or secrets into the image build context.

The container image also sets `GIN_MODE=release`.

If you prefer plain `docker` instead of Compose:

```bash
docker build -t chatgpt-codex-proxy .
docker run -d \
  --name chatgpt-codex-proxy \
  --restart unless-stopped \
  --env-file .env \
  -e DATA_DIR=/app/data \
  --read-only \
  --tmpfs /tmp \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -p 8080:8080 \
  -v chatgpt-codex-proxy-data:/app/data \
  chatgpt-codex-proxy
```

If you set a non-default `PORT` in `.env`, publish that same port from the container.

## Persistence

Account state is stored locally in:

- `${DATA_DIR}/accounts.json`
- `${DATA_DIR}/models-cache.json`

That file includes:

- Account metadata
- OAuth tokens
- A reserved `cookies` field in the persisted schema
- Cached quota snapshots
- Transient cooldown state
- Admin labels and status flags

The model cache file stores the last successful discovered model catalog plus per-route support metadata.

Continuation mappings, conversation affinity, and in-flight device-login coordination are kept in memory and are not persisted across restarts.

When running with `docker compose`, the same account state is stored in the named volume `chatgpt-codex-proxy-data`, mounted at `/app/data`.

## What This Project Does

- Exposes OpenAI-style endpoints such as `POST /v1/chat/completions` and `POST /v1/responses`
- Translates those requests into the upstream Codex request format
- Streams upstream events back as OpenAI-style JSON or SSE
- Manages one or more Codex accounts authenticated through ChatGPT device login
- Rotates requests across healthy accounts
- Provides a small admin API for onboarding, quota checks, and routing visibility

Supported capabilities:

- Streaming and non-streaming responses
- Tool calling via `tools`
- Legacy Chat Completions `functions` and top-level `function_call`
- Hosted web search tool passthrough
- Structured outputs
- Text, image, and file inputs on Chat Completions
- Text, image, and file inputs on Responses
- Chat Completions reasoning summaries via `reasoning_content`
- Responses reasoning-item replay, including `encrypted_content`
- Dynamic public model catalog fetched from the upstream Codex backend
- Explicit `previous_response_id` continuations on both Chat Completions and Responses
- Stable `prompt_cache_key` derivation plus guarded implicit resume when follow-up requests replay prior assistant or tool history
- Multi-account rotation with `least_used`, `round_robin`, and `sticky`
- Local JSON persistence for accounts, cached quota state, and the most recent discovered model catalog
- Automatic recovery when cached quota or transient cooldown windows expire

## How It Works

1. A Gin server accepts OpenAI-style HTTP requests.
2. The request is normalized into one internal format.
3. That normalized request is translated into the upstream Codex backend shape.
4. The upstream response stream is converted back into OpenAI-style JSON or SSE.

<img width="4599" height="2073" alt="image" src="https://github.com/user-attachments/assets/05cd8446-dd4b-43bc-a3fc-eb370ad917e6" />

The proxy talks to:

- `POST https://chatgpt.com/backend-api/codex/responses`
- `GET https://chatgpt.com/backend-api/codex/usage`
- `GET https://chatgpt.com/backend-api/codex/models`
- `WSS https://chatgpt.com/backend-api/codex/responses` for continuation requests
- `https://auth.openai.com/api/accounts/deviceauth/*` and `https://auth.openai.com/oauth/token` for device login and token refresh

For follow-up turns, the proxy keeps short-lived in-memory state so explicit and implicit continuations stay pinned to the correct account, reuse upstream turn state when safe, and set a stable `prompt_cache_key`.

Both public request styles are normalized into one internal request model before being sent upstream.

Key translation rules:

- `system` and `developer` messages are merged into a single `instructions` string
- `user` and `assistant` messages become upstream input items
- Assistant function tool calls become upstream `function_call` items
- Assistant custom tool calls become upstream `custom_tool_call` items
- Tool outputs become upstream `function_call_output` or `custom_tool_call_output` items, depending on the original tool type
- Text, image, and file content are mapped to `input_text`, `input_image`, and `input_file`
- Responses reasoning items are preserved and can be replayed on later turns
- Responses API assistant replay content such as `output_text` is accepted for stateless continuation reconstruction
- Function tools are accepted in both Chat Completions-style nested form and the modern Responses API top-level form
- Chat Completions custom tools are accepted in their native `type = "custom"` form
- On Chat Completions replay turns, function-shaped assistant tool calls are mapped back to upstream custom tool calls when their name matches a declared custom tool. This preserves compatibility with clients that replay custom tools through function-shaped `tool_calls`.
- A stable conversation key is derived from the normalized request and used as `prompt_cache_key` when possible.
- Explicit continuation requests stay pinned to the account that created the earlier response and use the upstream WebSocket continuation transport on both public endpoints.
- Implicit continuation is only attempted when model, instructions, and replayed assistant or tool history line up with a recent in-memory continuation record. Tool outputs are only resumed when their `call_id` values match known prior tool calls.
- Unsupported content types return `400` instead of being dropped silently

## Project Layout

- `cmd/api`
  Server entry point.
- `internal/config`
  Small runtime configuration.
- `internal/server`
  HTTP routes and handlers.
- `internal/middleware`
  API key auth, request IDs, logging, and panic recovery.
- `internal/openai`
  OpenAI-facing request and response types.
- `internal/translate`
  OpenAI-to-Codex request translation and response shaping.
- `internal/codex`
  Upstream Codex types, headers, OAuth, quota parsing, plus HTTP and WebSocket transport for upstream continuation support.
- `internal/accounts`
  Account records, cached quota state, continuation affinity, and rotation logic.
- `internal/admin`
  Device login flow orchestration.
- `internal/store`
  Local JSON persistence.
- `internal/observability`
  Structured logging setup.
- `internal/integration`
  Build-tagged live API tests for end-to-end compatibility checks against a running proxy instance.
- `docs/`
  Supplemental notes, including the multi-account rotation design and the architecture diagram source.

## Account Rotation

- `least_used`
  Prefer healthy accounts with lower cached primary quota usage, then lower secondary usage, then earlier primary reset windows. Accounts without usable cached quota are fallback candidates behind accounts with real quota data.
- `round_robin`
  Cycle healthy accounts in order.
- `sticky`
  Reuse the last successfully used healthy account in memory.

Continuation affinity is handled separately from global rotation. Continuation requests prefer the account that created the earlier response, and explicit continuations fail closed if that account is no longer usable for the requested model.

Quota observations are updated from upstream response headers, explicit `/codex/usage` fetches, and `codex.rate_limits` stream events. Internal `codex.rate_limits` events are consumed by the proxy and are not forwarded to downstream OpenAI clients.

## Observability

- Structured JSON logging via `slog`
- Request IDs
- Panic recovery middleware
- JSON error responses

## Testing

Run unit tests with:

```bash
go test ./...
```

Run the live compatibility tests against a running local proxy with:

```bash
OPENAI_API_KEY=change-me-to-a-long-random-string \
OPENAI_MODEL=gpt-5.2 \
OPENAI_BASE_URL="${PROXY_URL}/v1" \
go test -tags=live ./internal/integration -v -count=1
```

The live suite currently checks:

- Streaming Chat Completions custom-tool round trips, including a replay turn with a tool result

## Limitations

- The upstream Codex backend is private and may change without notice.
- Account onboarding is device-auth only.
- The implementation is intentionally small and does not aim to cover every edge case of the public OpenAI platform.
- Session affinity and implicit continuation state are in memory only and expire with the configured continuation TTL.
