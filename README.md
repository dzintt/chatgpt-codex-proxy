# chatgpt-codex-proxy

`chatgpt-codex-proxy` is a small Go service that lets standard OpenAI clients talk to ChatGPT Codex accounts.

It exposes an OpenAI-compatible API, translates those requests into the private ChatGPT Codex backend format, and manages one or more locally authenticated Codex accounts.

This project depends on the private `chatgpt.com/backend-api/codex/*` surface. That surface is undocumented and may change at any time.

Use it for local or small-scale deployments.

## What This Project Does

- Exposes OpenAI-style endpoints such as `POST /v1/chat/completions` and `POST /v1/responses`
- Translates those requests into the upstream Codex request format
- Streams upstream events back as OpenAI-style JSON or SSE
- Manages one or more Codex accounts authenticated through ChatGPT device login
- Rotates requests across healthy accounts
- Provides a small admin API for onboarding, quota checks, and routing visibility

## What It Supports

- `POST /v1/chat/completions`
- `POST /v1/responses`
- `GET /v1/models`
- `GET /v1/models/<model_id>`
- Streaming and non-streaming responses
- Tool calling via `tools`
- Legacy Chat Completions `functions` and top-level `function_call`
- Hosted web search tool passthrough
- Structured outputs
- Text and image inputs on Chat Completions
- Text, image, and file inputs on Responses
- `previous_response_id` continuations
- Multi-account rotation with `least_used`, `round_robin`, and `sticky`
- Local JSON persistence for accounts and cached quota state
- Automatic recovery when cached quota or transient cooldown windows expire

## What It Does Not Try To Be

- A full public OpenAI API implementation
- A reimplementation of the ChatGPT desktop app
- A distributed or multi-node service
- A dashboard product
- A generic credential vault

## How It Works

1. A Gin server accepts OpenAI-style HTTP requests.
2. The request is normalized into one internal format.
3. That normalized request is translated into the upstream Codex backend shape.
4. The upstream response stream is converted back into OpenAI-style JSON or SSE.

<img width="1533" height="691" alt="image" src="https://github.com/user-attachments/assets/61af6057-4fce-4767-92a5-18653f938a45" />

The proxy talks to:

- `POST https://chatgpt.com/backend-api/codex/responses`

For continuations, the proxy keeps short-lived in-memory state so a `previous_response_id` request stays pinned to the correct account and preserves the prior turn context.

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
  Upstream Codex types, headers, OAuth, quota parsing, and HTTP transport.
- `internal/codex/wsclient`
  WebSocket client kept for upstream continuation support.
- `internal/accounts`
  Account records, cached quota state, continuation affinity, and rotation logic.
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

Optional:

- `PORT`
  Overrides the default listen port `8080`.

### 2. Run the server

```bash
go run ./cmd/api
```

By default the server listens on `:8080` and stores local state in `./data`.

### 3. Add a Codex account

Start a device login:

```bash
curl -X POST http://localhost:8080/admin/accounts/device-login/start \
  -H "Authorization: Bearer change-me"
```

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
- Tool calling via `tools`
- Legacy `functions` and top-level `function_call` request compatibility
- Hosted web search passthrough
- Structured outputs via `response_format.type = "json_schema"` and `response_format.type = "json_object"`
- Reasoning effort
- Text and image input parts

Compatibility notes:

- Legacy requests are normalized onto the modern tool-calling response shape; responses still use `tool_calls` rather than the older assistant `function_call` field.
- The implementation does not try to honor every OpenAI Chat Completions tuning field. Known unsupported fields are logged as compatibility warnings when present.

### `POST /v1/responses`

Accepts OpenAI Responses requests and translates them into the upstream Codex request shape.

Supported behavior:

- Streaming and non-streaming
- Tools, including the modern Responses API function tool shape
- Structured outputs
- Text, image, and file inputs
- Explicit `previous_response_id` continuation
- Follow-up turns that replay prior `output_text` items from the OpenAI Responses shape

Compatibility notes:

- Structured-output schemas are normalized before they are sent upstream, including tuple-schema handling and stricter object-shape normalization for Codex compatibility.
- Known unsupported fields are logged as compatibility warnings when present.

### `GET /v1/models`

Returns a curated model list for OpenAI-compatible clients. The alias `codex` resolves to the configured default model.

### `GET /v1/models/<model_id>`

Returns one model object in the OpenAI model shape. The alias `codex` resolves to the configured default model and unknown IDs return an OpenAI-style `model_not_found` error.

### `GET /health/live`

Unauthenticated liveness endpoint.

### `GET /health`

Authenticated service health endpoint.

## Admin API

### Accounts

- `GET /admin/accounts`
  List locally known accounts, permanent status, derived eligibility, cooldown state, and cached quota.
- `DELETE /admin/accounts/:account_id`
  Remove an account from local persistence.
- `PATCH /admin/accounts/:account_id`
  Update mutable fields such as `label` or `status`.
- `POST /admin/accounts/:account_id/refresh`
  Force an OAuth token refresh.
- `GET /admin/accounts/:account_id/usage`
  Fetch the runtime quota view and cached quota metadata for one account.

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

### Status model

- Permanent account status is one of `active`, `disabled`, `expired`, or `banned`.
- Transient routing availability is tracked with `cooldown_until` plus the latest cached quota snapshot.
- General account routing is blocked only by primary or secondary quota exhaustion. `code_review_rate_limit` is retained for observability and does not affect normal routing.
- Exhausted quota windows are treated as temporary routing blocks. Accounts automatically become eligible again after the cached reset time passes.

## Persistence

Account state is stored locally in:

- `data/accounts.json`

That file includes:

- Account metadata
- OAuth tokens
- Session cookies
- Cached quota snapshots
- Transient cooldown state
- Admin labels and status flags

Continuation mappings and in-flight device-login coordination are kept in memory and are not persisted across restarts.

## Configuration

Supported environment variables:

- `PROXY_API_KEY`
  Required. Protects both public and admin routes.
- `PORT`
  Optional. Defaults to `8080`.

Everything else is fixed in code on purpose:

- Local state is stored in `./data`.
- The default `codex` alias resolves to `gpt-5.3-codex`.
- The initial rotation strategy is `least_used`, and can be changed at runtime through `PUT /admin/rotation`.
- Upstream base URLs, OAuth client details, request timeouts, fallback cooldowns, and desktop-like headers are implementation constants rather than deployment knobs.

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

- `least_used`
  Prefer healthy accounts with lower cached primary quota usage, then lower secondary usage, then earlier primary reset windows. Accounts without usable cached quota are fallback candidates behind accounts with real quota data.
- `round_robin`
  Cycle healthy accounts in order.
- `sticky`
  Reuse the last successfully used healthy account in memory.

Continuation affinity is handled separately from global rotation. Continuation requests prefer the account that created the earlier response.

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

## Limitations

- The upstream Codex backend is private and may change without notice.
- This project is single-process only.
- There is no database backend.
- There is no dashboard UI.
- Account onboarding is device-auth only.
- The implementation is intentionally small and does not aim to cover every edge case of the public OpenAI platform.
