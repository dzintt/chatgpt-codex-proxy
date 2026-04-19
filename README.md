# chatgpt-codex-proxy

A minimal Go service that exposes an OpenAI-compatible API and proxies requests to the private ChatGPT Codex backend.

This project is designed for developers who want to use existing OpenAI SDKs and tooling against Codex accounts authenticated through ChatGPT device auth, without carrying over the full complexity of larger proxy implementations.

## Purpose

`chatgpt-codex-proxy` sits between an OpenAI-style client and the ChatGPT Codex backend:

- It accepts OpenAI-style requests such as `POST /v1/chat/completions` and `POST /v1/responses`.
- It translates those requests into the upstream Codex request shape expected by `https://chatgpt.com/backend-api/codex/*`.
- It manages one or more Codex accounts locally.
- It rotates requests across healthy accounts using simple strategies.
- It exposes a small admin API for account onboarding, quota inspection, and rotation control.

The goal is not to mirror every detail of the public OpenAI platform. The goal is to provide a simple, understandable proxy that is compatible enough for normal OpenAI client usage while preserving the private Codex backend behaviors that matter in practice.

## What It Supports

- OpenAI-compatible `POST /v1/chat/completions`
- OpenAI-compatible `POST /v1/responses`
- Streaming and non-streaming responses
- Function tools
- Hosted web search tool passthrough
- Structured outputs
- Text and image inputs
- Continuations for `previous_response_id` using a dedicated WebSocket path
- Multi-account rotation with `least_used`, `round_robin`, and `sticky`
- Device-auth account onboarding
- Local JSON persistence for accounts and usage state
- `httpcloak` for upstream HTTP requests and transport impersonation

## What It Does Not Try To Be

- A public OpenAI API implementation
- A full reimplementation of the upstream desktop app
- A distributed, multi-node service
- A dashboard product
- A generic credential vault

This service is intentionally single-process and local-state-first.

## How It Works

At a high level, the proxy has four layers:

1. A Gin HTTP server exposes OpenAI-style and admin routes.
2. Public requests are translated into a shared internal request model.
3. That normalized request is converted into the upstream Codex backend request shape.
4. Upstream events are translated back into OpenAI-style JSON or SSE responses.

For normal response creation, the proxy uses HTTP SSE against:

- `POST https://chatgpt.com/backend-api/codex/responses`

For explicit continuation requests that depend on `previous_response_id`, the proxy uses:

- `wss://chatgpt.com/backend-api/codex/responses`

This matters because the upstream backend uses server-side conversation state and turn-state headers that cannot be handled correctly by naive HTTP fallback.

## Architecture

The codebase is intentionally modular:

- `cmd/api`
  Entry point and HTTP server startup.
- `internal/config`
  Environment-driven configuration.
- `internal/server`
  Gin route registration and HTTP handlers.
- `internal/middleware`
  API key auth, recovery, and request IDs.
- `internal/openai`
  OpenAI-facing request and error types.
- `internal/translate`
  OpenAI-to-Codex translation and downstream response shaping.
- `internal/codex`
  Upstream Codex request types, headers, OAuth, quota parsing, and HTTP transport.
- `internal/codex/wsclient`
  Dedicated WebSocket client used for continuation requests.
- `internal/accounts`
  Account records, local usage tracking, continuation affinity, and rotation logic.
- `internal/admin`
  Device-login orchestration.
- `internal/store`
  JSON persistence.
- `internal/observability`
  Logging setup.

## Requirements

- Go `1.26.x`
- A valid `PROXY_API_KEY`
- At least one Codex account added through the admin device-login flow

## Quick Start

### 1. Set environment variables

Create a `.env` file or export the variables in your shell.

Required:

```env
PROXY_API_KEY=change-me
```

Common optional settings:

```env
LISTEN_ADDR=:8080
DATA_DIR=data
DEFAULT_MODEL=gpt-5.2-codex
ROTATION_STRATEGY=least_used
LOG_LEVEL=info
REQUEST_TIMEOUT_SECONDS=120
```

### 2. Run the server

```bash
go run ./cmd/api
```

The server will listen on `:8080` by default.

### 3. Add a Codex account

Start a device login:

```bash
curl -X POST http://localhost:8080/admin/accounts/device-login/start \
  -H "Authorization: Bearer change-me"
```

The response will include:

- `login_id`
- `auth_url`
- `user_code`
- `status`

Open `auth_url`, complete the device-login flow, then poll:

```bash
curl http://localhost:8080/admin/accounts/device-login/<login_id> \
  -H "Authorization: Bearer change-me"
```

Once the login status becomes `ready`, the account is persisted locally and can be used for proxy requests.

### 4. Call the proxy with an OpenAI client

Point your client at:

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

Both of these are accepted:

- `Authorization: Bearer <PROXY_API_KEY>`
- `X-API-Key: <PROXY_API_KEY>`

The same API key protects both public and admin routes.

## Public API

### `POST /v1/chat/completions`

Accepts OpenAI Chat Completions format and translates it into the upstream Codex request shape.

Supported behavior:

- streaming and non-streaming
- `system` and `developer` instructions
- function tools
- hosted web search passthrough
- structured outputs
- reasoning effort
- text and image input parts

### `POST /v1/responses`

Accepts OpenAI Responses format and translates it into the upstream Codex request shape.

Supported behavior:

- streaming and non-streaming
- tools
- structured outputs
- text and image inputs
- explicit `previous_response_id` continuation

Continuation requests use the dedicated WebSocket upstream path. If the proxy cannot honor continuation affinity correctly, it fails rather than silently downgrading to a lossy path.

### `GET /v1/models`

Returns a curated model list suitable for OpenAI-compatible clients. The alias `codex` is supported and resolves to the configured default model.

### `GET /health/live`

Unauthenticated liveness endpoint.

### `GET /health`

Authenticated service health endpoint.

## Admin API

### Accounts

- `GET /admin/accounts`
  List accounts and cached state.
- `DELETE /admin/accounts/:account_id`
  Remove an account from local persistence.
- `PATCH /admin/accounts/:account_id`
  Update mutable fields such as `label` or `status`.
- `POST /admin/accounts/:account_id/refresh`
  Force an OAuth token refresh.
- `GET /admin/accounts/:account_id/usage`
  Fetch authoritative upstream quota and local usage counters.

### Device login

- `POST /admin/accounts/device-login/start`
  Start a device login flow.
- `GET /admin/accounts/device-login/:login_id`
  Poll a device login flow.

### Rotation

- `GET /admin/rotation`
  Show the active rotation strategy.
- `PUT /admin/rotation`
  Update the rotation strategy.

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

This includes:

- account metadata
- OAuth tokens
- session cookies
- cached quota snapshots
- local token and request counters
- admin labels and status flags

Continuation mappings and in-flight device-login coordination remain in memory and are not persisted across restarts.

## Configuration

The following environment variables are currently supported.

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

The proxy normalizes both public request styles into a shared internal form before sending them upstream.

Key translation rules:

- `system` and `developer` messages are folded into one `instructions` string.
- `user` and `assistant` messages become upstream input items.
- assistant tool calls become upstream `function_call` items.
- tool outputs become upstream `function_call_output` items.
- text and image content are mapped to `input_text` and `input_image`.
- unsupported content types are rejected with a `400` instead of being dropped silently.

## Account Rotation

Rotation is intentionally simple:

- `least_used`
  Prefers accounts with lower cached quota pressure, then lower request count, then older last-used time.
- `round_robin`
  Cycles healthy accounts in order.
- `sticky`
  Reuses the most recently used healthy account.

Explicit `previous_response_id` continuity is separate from global rotation. Continuation requests always prefer the originating account, regardless of the active rotation strategy.

## Observability

The service includes:

- structured JSON logging via `slog`
- request IDs
- panic recovery middleware
- bounded JSON error responses

## Running Tests

```bash
go test ./...
```

## Limitations

- The upstream `chatgpt.com/backend-api/codex/*` surface is private and may change without notice.
- This project is single-process only.
- There is no database backend.
- There is no dashboard UI.
- Account onboarding is device-auth only.
- Current multimodal support is limited to text and image inputs.
- The implementation is intentionally small and does not attempt to cover every edge case found in larger proxy projects.

## Why `httpcloak`

Upstream HTTP requests use `httpcloak` so the proxy can maintain the browser-like transport and header behavior needed by the Codex backend. The WebSocket continuation path is isolated separately because continuation semantics require a different transport.

## Development Status

This project is functional but still deliberately minimal. The core request path, account management, translation layer, and continuation flow are implemented. The main area that still benefits from ongoing validation is live upstream behavior, since the Codex backend is not a stable documented public API.
