# chatgpt-codex-proxy

`chatgpt-codex-proxy` is a small Go service that lets standard OpenAI clients talk to ChatGPT Codex accounts.

It exposes an OpenAI-compatible API, translates those requests into the private ChatGPT Codex backend format, and manages one or more locally authenticated Codex accounts.

## Important Note

This project depends on the private `chatgpt.com/backend-api/codex/*` surface. That surface is undocumented and may change at any time.

This proxy is meant for local or small-scale use by developers who want OpenAI SDK compatibility without building a larger proxy stack.

## What This Project Does

- Exposes OpenAI-style endpoints such as `POST /v1/chat/completions` and `POST /v1/responses`
- Translates those requests into the upstream Codex request format
- Streams upstream events back as OpenAI-style JSON or SSE
- Manages one or more Codex accounts authenticated through ChatGPT device login
- Rotates requests across healthy accounts
- Provides a small admin API for onboarding, quota checks, and rotation control

## What It Supports

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `GET /v1/models`
- Streaming and non-streaming responses
- Function tools
- Hosted web search tool passthrough
- Structured outputs
- Text, image, and file inputs
- `previous_response_id` continuations
- Multi-account rotation with `least_used`, `round_robin`, and `sticky`
- Local JSON persistence for accounts and usage state

## What It Does Not Try To Be

- A full public OpenAI API implementation
- A reimplementation of the ChatGPT desktop app
- A distributed or multi-node service
- A dashboard product
- A generic credential vault

## How It Works

At a high level:

1. A Gin server accepts OpenAI-style HTTP requests.
2. The request is normalized into one internal format.
3. That normalized request is translated into the upstream Codex backend shape.
4. The upstream response stream is converted back into OpenAI-style JSON or SSE.

For normal requests, the proxy talks to:

- `POST https://chatgpt.com/backend-api/codex/responses`

For continuations, the proxy keeps short-lived in-memory state so a `previous_response_id` request stays pinned to the correct account and preserves the prior turn context.

## Project Layout

- `cmd/api`
  Server entry point.
- `internal/config`
  Environment-driven configuration.
- `internal/server`
  HTTP routes and handlers.
- `internal/middleware`
  API key auth, request IDs, logging, and panic recovery.
- `internal/openai`
  OpenAI-facing request and response types.
- `internal/translate`
  OpenAI-to-Codex request translation and response shaping.
- `internal/codex`
  Upstream Codex types, headers, OAuth, quota parsing, and HTTP transport.
- `internal/codex/wsclient`
  WebSocket client kept for upstream continuation support.
- `internal/accounts`
  Account records, local usage tracking, continuation affinity, and rotation logic.
- `internal/admin`
  Device login flow orchestration.
- `internal/store`
  Local JSON persistence.
- `internal/observability`
  Structured logging setup.

## Requirements

- Go `1.26.x`
- A valid `PROXY_API_KEY`
- At least one Codex account added through the admin device-login flow

## Quick Start

### 1. Configure the proxy

Create a `.env` file or export the variables in your shell.

Required:

```env
PROXY_API_KEY=change-me
```

Common optional settings:

```env
LISTEN_ADDR=:8080
DATA_DIR=data
DEFAULT_MODEL=gpt-5.3-codex
ROTATION_STRATEGY=least_used
LOG_LEVEL=info
REQUEST_TIMEOUT_SECONDS=120
```

### 2. Run the server

```bash
go run ./cmd/api
```

By default the server listens on `:8080`.

### 3. Add a Codex account

Start a device login:

```bash
curl -X POST http://localhost:8080/admin/accounts/device-login/start \
  -H "Authorization: Bearer change-me"
```

The response includes:

- `login_id`
- `auth_url`
- `user_code`
- `status`

Open `auth_url`, complete the login flow, then poll for completion:

```bash
curl http://localhost:8080/admin/accounts/device-login/<login_id> \
  -H "Authorization: Bearer change-me"
```

When the login status becomes `ready`, the account has been saved locally and can be used for proxy requests.

### 4. Point an OpenAI client at the proxy

Use:

- Base URL: `http://localhost:8080/v1`
- API key: your `PROXY_API_KEY`

Example with `curl`:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer change-me" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "codex",
    "messages": [
      { "role": "system", "content": "Be concise." },
      { "role": "user", "content": "Explain what this repository does." }
    ]
  }'
```

Example `Responses API` request:

```bash
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer change-me" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "codex",
    "input": "Summarize this project in three bullet points."
  }'
```

## Authentication

Every route except `GET /health/live` requires the proxy API key.

The proxy accepts either:

- `Authorization: Bearer <PROXY_API_KEY>`
- `X-API-Key: <PROXY_API_KEY>`

The same API key protects both public and admin routes.

## Public API

### `POST /v1/chat/completions`

Accepts OpenAI Chat Completions requests and translates them into the upstream Codex request shape.

Supported behavior:

- Streaming and non-streaming
- `system` and `developer` instructions
- Function tools
- Hosted web search passthrough
- Structured outputs
- Reasoning effort
- Text and image input parts

### `POST /v1/responses`

Accepts OpenAI Responses requests and translates them into the upstream Codex request shape.

Supported behavior:

- Streaming and non-streaming
- Tools, including the modern Responses API function tool shape
- Structured outputs
- Text, image, and file inputs
- Explicit `previous_response_id` continuation

### `GET /v1/models`

Returns a curated model list for OpenAI-compatible clients. The alias `codex` resolves to the configured default model.

### `GET /health/live`

Unauthenticated liveness endpoint.

### `GET /health`

Authenticated service health endpoint.

## Admin API

### Accounts

- `GET /admin/accounts`
  List locally known accounts and cached state.
- `DELETE /admin/accounts/:account_id`
  Remove an account from local persistence.
- `PATCH /admin/accounts/:account_id`
  Update mutable fields such as `label` or `status`.
- `POST /admin/accounts/:account_id/refresh`
  Force an OAuth token refresh.
- `GET /admin/accounts/:account_id/usage`
  Fetch upstream quota and local usage counters.

### Device login

- `POST /admin/accounts/device-login/start`
  Start a device login flow.
- `GET /admin/accounts/device-login/:login_id`
  Poll a device login flow.

### Rotation

- `GET /admin/rotation`
  Show the current rotation strategy.
- `PUT /admin/rotation`
  Change the rotation strategy.

Valid strategies:

- `least_used`
- `round_robin`
- `sticky`

### Usage summary

- `GET /admin/usage/summary`
  Return aggregate local usage counters across all accounts.

## Persistence

Account state is stored locally in:

- `data/accounts.json`

That file includes:

- Account metadata
- OAuth tokens
- Session cookies
- Cached quota snapshots
- Local token and request counters
- Admin labels and status flags

Continuation mappings and in-flight device-login coordination are kept in memory and are not persisted across restarts.

## Configuration

Supported environment variables:

### Required

- `PROXY_API_KEY`

### Server

- `LISTEN_ADDR`
- `DATA_DIR`
- `LOG_LEVEL`

### Proxy behavior

- `DEFAULT_MODEL`
- `ROTATION_STRATEGY`
- `REQUEST_TIMEOUT_SECONDS`
- `LOGIN_TIMEOUT_SECONDS`
- `USAGE_CACHE_TTL_SECONDS`
- `CONTINUATION_TTL_MINUTES`

### Upstream Codex

- `CODEX_BASE_URL`
- `CODEX_ORIGINATOR`
- `CODEX_OPENAI_BETA`
- `CODEX_RESIDENCY`

### OAuth

- `OPENAI_AUTH_ISSUER`
- `OPENAI_OAUTH_CLIENT_ID`

### Desktop-like client identity

- `USER_AGENT_TEMPLATE`
- `CHROMIUM_VERSION`
- `CLIENT_PLATFORM`
- `CLIENT_HINT_PLATFORM`
- `CLIENT_ARCH`
- `DEFAULT_ACCEPT_LANGUAGE`

## Translation Notes

Both public request styles are normalized into one internal request model before being sent upstream.

Key translation rules:

- `system` and `developer` messages are merged into a single `instructions` string
- `user` and `assistant` messages become upstream input items
- Assistant tool calls become upstream `function_call` items
- Tool outputs become upstream `function_call_output` items
- Text, image, and file content are mapped to `input_text`, `input_image`, and `input_file`
- Responses API assistant replay content such as `output_text` is accepted for stateless continuation reconstruction
- Function tools are accepted in both Chat Completions-style nested form and the modern Responses API top-level form
- Unsupported content types return `400` instead of being dropped silently

## Account Rotation

Rotation is intentionally simple:

- `least_used`
  Prefer accounts with lower cached quota pressure, then lower request count, then older last-used time.
- `round_robin`
  Cycle healthy accounts in order.
- `sticky`
  Reuse the most recently used healthy account.

Continuation affinity is handled separately from global rotation. Continuation requests prefer the account that created the earlier response.

## Observability

The service includes:

- Structured JSON logging via `slog`
- Request IDs
- Panic recovery middleware
- JSON error responses

## Testing

Run unit tests with:

```bash
go test ./...
```

## Limitations

- The upstream Codex backend is private and may change without notice.
- This project is single-process only.
- There is no database backend.
- There is no dashboard UI.
- Account onboarding is device-auth only.
- The implementation is intentionally small and does not aim to cover every edge case of the public OpenAI platform.
