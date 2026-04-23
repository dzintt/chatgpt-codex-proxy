# CODEX API Docs

This document describes the private ChatGPT Codex API surface used by this repository, based strictly on the current implementation in `chatgpt-codex-proxy`.

It is not an official OpenAI or ChatGPT specification.

Everything here is inferred from:

- Request builders in `internal/codex`
- Response parsers in `internal/codex`, `internal/server`, and `internal/translate`
- Tests that lock in the currently observed behavior

The upstream surface is private and may change without notice.

## Scope

This proxy currently uses two upstream systems:

1. The private Codex backend under `https://chatgpt.com/backend-api`
2. The OpenAI auth/device-login endpoints under `https://auth.openai.com`

The Codex backend endpoints in active use are:

- `POST /codex/responses`
- `POST /codex/responses/compact`
- `WSS /codex/responses`
- `GET /codex/usage`
- `GET /codex/models`

The auth endpoints in active use are:

- `POST /api/accounts/deviceauth/usercode`
- `POST /api/accounts/deviceauth/token`
- `POST /oauth/token`

## Base URLs

Codex backend:

- `https://chatgpt.com/backend-api`

Auth backend:

- `https://auth.openai.com`

## Authentication

### Codex backend auth

Codex API calls use an OAuth access token in the `Authorization` header:

```http
Authorization: Bearer <access_token>
```

The proxy also sends the upstream ChatGPT account ID when known:

```http
ChatGPT-Account-Id: <account_id>
```

### Auth backend auth

The auth/device endpoints do not use a bearer token in this implementation. They are called with:

- JSON request bodies for device-code endpoints
- Form-encoded bodies for `/oauth/token`

## Common Codex Headers

The proxy intentionally mimics a desktop Codex client. These headers are always or usually sent to the Codex backend.

### Always sent

```http
Authorization: Bearer <access_token>
originator: Codex Desktop
x-openai-internal-codex-residency: us
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
sec-ch-ua: "Chromium";v="147", "Not:A-Brand";v="24"
sec-ch-ua-mobile: ?0
sec-ch-ua-platform: "Windows"
Accept-Encoding: gzip, deflate, br, zstd
Accept-Language: en-US,en;q=0.9
sec-fetch-site: same-origin
sec-fetch-mode: cors
sec-fetch-dest: empty
x-client-request-id: req_<timestamp>_<counter>
```

### Sent when available

```http
ChatGPT-Account-Id: <chatgpt account id>
Cookie: <serialized cookies>
x-codex-turn-state: <turn state from previous response>
OpenAI-Beta: responses_websockets=2026-02-06
Content-Type: application/json
Accept: text/event-stream
```

### Notes

- `OpenAI-Beta: responses_websockets=2026-02-06` is sent for response creation requests, compact requests, and websocket continuations.
- `x-codex-turn-state` is reused on continuation requests.
- The proxy preserves a specific header ordering when talking to upstream.

## Data Types

## Response Request Object

This is the canonical request shape the proxy sends to the HTTP Codex responses endpoint.

```json
{
  "model": "gpt-5.4",
  "instructions": "You are a helpful assistant.",
  "input": [],
  "stream": true,
  "store": false,
  "tools": [],
  "tool_choice": {},
  "text": {
    "format": {
      "type": "json_schema",
      "name": "result",
      "schema": {},
      "strict": true
    }
  },
  "reasoning": {
    "effort": "medium",
    "summary": "auto"
  },
  "prompt_cache_key": "01234567-89ab-cdef-0123-456789abcdef",
  "include": [
    "reasoning.encrypted_content"
  ]
}
```

Observed fields:

- `model: string`
- `instructions: string`
- `input: []InputItem`
- `stream: bool`
- `store: bool`
- `tools: []Tool`
- `tool_choice: string | object`
- `text: { format: TextFormat }`
- `reasoning: { effort?: string, summary?: string }`
- `service_tier: string`
- `previous_response_id: string`
- `prompt_cache_key: string`
- `include: []string`

Implementation notes:

- The HTTP path always forces `stream = true`.
- The HTTP path always forces `store = false`.
- The HTTP path clears `previous_response_id` before sending.
- The websocket continuation path does not use this exact object; it wraps the request in a `response.create` envelope.

## Compact Request Object

This is the canonical request shape the proxy sends to the JSON compact endpoint.

```json
{
  "model": "gpt-5.4",
  "instructions": "Summarize the thread state.",
  "input": [
    {
      "role": "assistant",
      "phase": "output",
      "content": [
        {
          "type": "output_text",
          "text": "Long prior answer"
        }
      ]
    },
    {
      "type": "compaction",
      "id": "cmp_existing",
      "encrypted_content": "enc_existing"
    }
  ],
  "text": {
    "format": {
      "type": "json_schema",
      "name": "compact_summary",
      "schema": {},
      "strict": true
    }
  },
  "reasoning": {
    "effort": "high",
    "summary": "auto"
  }
}
```

Observed fields:

- `model: string`
- `instructions: string`
- `input: []InputItem`
- `text: { format: TextFormat }`
- `reasoning: { effort?: string, summary?: string }`

Implementation notes:

- The compact path is plain JSON, not SSE.
- The compact path does not send `stream`, `store`, or `previous_response_id`.
- If a client supplies `previous_response_id` to the proxy's public compact endpoint, the proxy expands saved history locally before calling upstream.

## Compact Response Object

Observed top-level response shape accepted by the proxy:

```json
{
  "id": "cmp_resp_123",
  "object": "response.compaction",
  "created_at": 1730000000,
  "output": [
    {
      "type": "compaction",
      "id": "cmp_123",
      "encrypted_content": "enc"
    }
  ],
  "usage": {
    "input_tokens": 10,
    "output_tokens": 4
  }
}
```

Observed fields:

- `id: string`
- `object: string`
- `created_at: unix timestamp`
- `output: []object`
- `usage: object`

Implementation notes:

- The proxy treats `output` items as opaque JSON objects.
- An observed compact output item type is `compaction`.

## InputItem

Observed item shape sent to upstream:

```json
{
  "role": "user",
  "type": "function_call",
  "phase": "output",
  "content": "hello",
  "call_id": "call_123",
  "name": "Search",
  "input": "raw custom tool input",
  "arguments": "{\"q\":\"hello\"}",
  "output": "tool output",
  "id": "fc_123",
  "status": "completed",
  "summary": [],
  "encrypted_content": "..."
}
```

Observed fields:

- `role: "user" | "assistant" | ...`
- `type: "function_call" | "custom_tool_call" | "function_call_output" | "custom_tool_call_output" | "reasoning" | "compaction"`
- `phase: string`
- `content: string | []ContentPart`
- `call_id: string`
- `name: string`
- `input: string`
- `arguments: string`
- `output: string | []ContentPart`
- `id: string`
- `status: string`
- `summary: []ReasoningPart`
- `encrypted_content: string`

Content serialization rules implemented by the proxy:

- Plain text role messages are serialized as a string `content`
- Structured user content is serialized as an array `content`
- Replayed assistant output messages may include `phase: "output"`
- Tool output may be serialized as:
  - `output: ""`
  - `output: "<text>"`
  - `output: [ ...content parts... ]`

## ContentPart

Observed content part variants:

### Text

```json
{
  "type": "input_text",
  "text": "hello"
}
```

Also observed:

- `type: "output_text"`
- `type: "reasoning_text"`

### Image

```json
{
  "type": "input_image",
  "image_url": "https://example.com/image.png",
  "detail": "high"
}
```

### File

```json
{
  "type": "input_file",
  "file_url": "https://example.com/file.txt",
  "file_data": "base64-or-raw-data",
  "file_id": "file_123",
  "filename": "file.txt",
  "detail": "auto"
}
```

## Tool Object

Observed tool variants:

### Function tool

```json
{
  "type": "function",
  "name": "Search",
  "description": "Run a search",
  "parameters": {
    "type": "object",
    "properties": {},
    "additionalProperties": false
  },
  "strict": true
}
```

### Web search tool

```json
{
  "type": "web_search",
  "search_context_size": "medium",
  "user_location": {
    "country": "US"
  }
}
```

### Custom tools

The proxy accepts and forwards custom tool definitions from the OpenAI-facing API. It does not define a separate typed Codex struct for them; they are passed through as generic tool objects.

## Text Format

Observed text format shapes:

### JSON object

```json
{
  "type": "json_object"
}
```

### JSON schema

```json
{
  "type": "json_schema",
  "name": "result",
  "schema": {
    "type": "object",
    "properties": {},
    "additionalProperties": false
  },
  "strict": true
}
```

Implementation note:

- The proxy rewrites tuple-style JSON Schema `prefixItems` into object-shaped schemas before sending them upstream.

## Codex Backend Endpoints

For the copy-paste examples below, assume a POSIX-compatible shell with:

```bash
export CODEX_BASE_URL=https://chatgpt.com/backend-api
export AUTH_BASE_URL=https://auth.openai.com
export ACCESS_TOKEN=replace-me
export ACCOUNT_ID=replace-me
```

## POST /codex/responses

Create a response stream over HTTP/SSE.

### URL

```http
POST https://chatgpt.com/backend-api/codex/responses
```

### Headers

Typical request headers:

```http
Authorization: Bearer <access_token>
ChatGPT-Account-Id: <account_id>
originator: Codex Desktop
x-openai-internal-codex-residency: us
x-client-request-id: req_<...>
x-codex-turn-state: <turn_state>
OpenAI-Beta: responses_websockets=2026-02-06
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
Content-Type: application/json
Accept: text/event-stream
```

### Request body

The proxy sends the `Request` object described above.

Example:

```json
{
  "model": "gpt-5.4",
  "instructions": "Be concise.",
  "input": [
    {
      "role": "user",
      "content": "Explain this repository."
    }
  ],
  "stream": true,
  "store": false
}
```

Example with tool calls:

```json
{
  "model": "gpt-5.4",
  "instructions": "Be concise.",
  "input": [
    {
      "role": "user",
      "content": "Find README files."
    }
  ],
  "stream": true,
  "store": false,
  "tools": [
    {
      "type": "function",
      "name": "Glob",
      "description": "Find files by glob",
      "parameters": {
        "type": "object",
        "properties": {
          "glob_pattern": {
            "type": "string"
          }
        },
        "required": [
          "glob_pattern"
        ],
        "additionalProperties": false
      }
    }
  ],
  "tool_choice": {
    "type": "function",
    "name": "Glob"
  }
}
```

```bash
curl -sS -N "${CODEX_BASE_URL}/codex/responses" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "ChatGPT-Account-Id: ${ACCOUNT_ID}" \
  -H "OpenAI-Beta: responses_websockets=2026-02-06" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{
    "model": "gpt-5.4",
    "instructions": "Be concise.",
    "input": [
      {
        "role": "user",
        "content": "Explain this repository."
      }
    ],
    "stream": true,
    "store": false
  }'
```

### Response

The proxy expects an SSE stream. Each event is parsed as:

- optional `event: <name>`
- one or more `data: <json>`

If `event:` is omitted, the proxy falls back to `data.type`.

Terminal markers:

- `data: [DONE]`
- stream EOF after a final `response.completed`

### Observed event types

The proxy currently understands these upstream event types:

- `response.output_text.delta`
- `response.output_text.done`
- `response.reasoning_summary_text.delta`
- `response.function_call_arguments.delta`
- `response.function_call_arguments.done`
- `response.custom_tool_call_input.delta`
- `response.custom_tool_call_input.done`
- `response.output_item.added`
- `response.output_item.done`
- `response.content_part.done`
- `response.completed`
- `response.failed`
- `error`
- `codex.rate_limits`

### response.output_text.delta

Observed payload:

```json
{
  "type": "response.output_text.delta",
  "response_id": "resp_123",
  "delta": "partial text"
}
```

Fields used by the proxy:

- `response_id`
- `delta`

### response.output_text.done

Observed payload:

```json
{
  "type": "response.output_text.done",
  "response_id": "resp_123",
  "text": "final text"
}
```

Fields used by the proxy:

- `text`

### response.reasoning_summary_text.delta

Observed payload:

```json
{
  "type": "response.reasoning_summary_text.delta",
  "response_id": "resp_123",
  "delta": "reasoning summary text"
}
```

Fields used by the proxy:

- `delta`

### response.function_call_arguments.delta

Observed payload:

```json
{
  "type": "response.function_call_arguments.delta",
  "response_id": "resp_123",
  "item_id": "fc_123",
  "call_id": "call_123",
  "output_index": 0,
  "name": "Search",
  "delta": "{\"q\":\"hel"
}
```

Fields used by the proxy:

- `item_id`
- `call_id`
- `output_index`
- `name`
- `delta`

### response.function_call_arguments.done

Observed payload:

```json
{
  "type": "response.function_call_arguments.done",
  "response_id": "resp_123",
  "item_id": "fc_123",
  "call_id": "call_123",
  "output_index": 0,
  "name": "Search",
  "arguments": "{\"q\":\"hello\"}"
}
```

Fields used by the proxy:

- `item_id`
- `call_id`
- `output_index`
- `name`
- `arguments`

### response.custom_tool_call_input.delta

Observed payload:

```json
{
  "type": "response.custom_tool_call_input.delta",
  "response_id": "resp_123",
  "item_id": "ctc_123",
  "output_index": 0,
  "delta": "*** Begin Patch\n"
}
```

Fields used by the proxy:

- `item_id`
- `output_index`
- `delta`

### response.custom_tool_call_input.done

Observed payload:

```json
{
  "type": "response.custom_tool_call_input.done",
  "response_id": "resp_123",
  "item_id": "ctc_123",
  "output_index": 0,
  "input": "*** Begin Patch\n..."
}
```

Fields used by the proxy:

- `item_id`
- `output_index`
- `input`

### response.output_item.added

Observed payload:

```json
{
  "type": "response.output_item.added",
  "response_id": "resp_123",
  "output_index": 0,
  "item": {
    "id": "fc_123",
    "call_id": "call_123",
    "type": "function_call",
    "name": "Search",
    "arguments": "{\"q\":\"hello\"}",
    "status": "in_progress"
  }
}
```

Observed item types used by the proxy:

- `message`
- `function_call`
- `custom_tool_call`

### response.output_item.done

Observed payload:

```json
{
  "type": "response.output_item.done",
  "response_id": "resp_123",
  "output_index": 0,
  "item": {
    "id": "fc_123",
    "call_id": "call_123",
    "type": "function_call",
    "name": "Search",
    "arguments": "{\"q\":\"hello\"}",
    "status": "completed"
  }
}
```

### response.content_part.done

Observed payload shape used by the proxy:

```json
{
  "type": "response.content_part.done",
  "part": {
    "text": "..."
  }
}
```

Fields used by the proxy:

- `part.text`

### response.completed

This is the expected terminal success event.

Observed payload:

```json
{
  "type": "response.completed",
  "response": {
    "id": "resp_123",
    "model": "gpt-5.4",
    "status": "completed",
    "output": [],
    "output_text": "final text",
    "usage": {
      "input_tokens": 100,
      "output_tokens": 25,
      "cached_tokens": 10,
      "reasoning_tokens": 5
    }
  }
}
```

Fields used by the proxy:

- `response.id`
- `response.model`
- `response.status`
- `response.output`
- `response.output_text`
- `response.usage`
- `response.error` when present

The proxy considers a response incomplete if:

- no `response.completed` event is seen, or
- no final response ID is available

### response.failed and error

The proxy treats both `response.failed` and `error` as upstream failures.

Observed error field locations:

- `error.message`
- `error.detail`
- `error.code`
- `error.type`
- `error.status_code`
- `error.status`
- `error.resets_in_seconds`
- `error.retry_after`
- `error.resets_at`

Also observed at top level:

- `message`
- `detail`
- `code`
- `type`
- `status_code`
- `status`

Observed example:

```json
{
  "type": "response.failed",
  "error": {
    "message": "quota exhausted",
    "code": "quota_exhausted",
    "status_code": 402,
    "resets_in_seconds": 120
  }
}
```

### codex.rate_limits

This is an internal quota event consumed by the proxy and not forwarded downstream.

Observed payload:

```json
{
  "type": "codex.rate_limits",
  "rate_limits": {
    "primary": {
      "used_percent": 100,
      "window_minutes": 300,
      "reset_at": 4102444800
    },
    "secondary": {
      "used_percent": 45,
      "window_minutes": 10080,
      "reset_at": 4103049600
    },
    "code_review": {
      "used_percent": 100,
      "limit_window_seconds": 86400,
      "reset_at": 4103136000
    }
  }
}
```

Fields used by the proxy:

- `rate_limits.primary.used_percent`
- `rate_limits.primary.window_minutes`
- `rate_limits.primary.limit_window_seconds`
- `rate_limits.primary.reset_at`
- same for `secondary`
- same for `code_review` or `code_review_rate_limit`

## POST /codex/responses/compact

Create a compacted response object over plain JSON HTTP.

### URL

```http
POST https://chatgpt.com/backend-api/codex/responses/compact
```

### Headers

Typical request headers:

```http
Authorization: Bearer <access_token>
ChatGPT-Account-Id: <account_id>
originator: Codex Desktop
x-openai-internal-codex-residency: us
x-client-request-id: req_<...>
OpenAI-Beta: responses_websockets=2026-02-06
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
Content-Type: application/json
Accept: application/json
```

### Request body

The proxy sends the `CompactRequest` object described above.

Example:

```json
{
  "model": "gpt-5.4",
  "input": [
    {
      "role": "assistant",
      "phase": "output",
      "content": [
        {
          "type": "output_text",
          "text": "Long prior answer"
        }
      ]
    },
    {
      "role": "user",
      "content": "Compact this thread for the next turn."
    }
  ]
}
```

Example with an existing compaction artifact:

```json
{
  "model": "gpt-5.4",
  "input": [
    {
      "type": "compaction",
      "id": "cmp_existing",
      "encrypted_content": "enc_existing"
    },
    {
      "role": "user",
      "content": "Refresh the summary with the latest turn."
    }
  ]
}
```

```bash
curl -sS "${CODEX_BASE_URL}/codex/responses/compact" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "ChatGPT-Account-Id: ${ACCOUNT_ID}" \
  -H "OpenAI-Beta: responses_websockets=2026-02-06" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d '{
    "model": "gpt-5.4",
    "input": [
      {
        "role": "assistant",
        "phase": "output",
        "content": [
          {
            "type": "output_text",
            "text": "Long prior answer"
          }
        ]
      },
      {
        "role": "user",
        "content": "Compact this thread for the next turn."
      }
    ]
  }'
```

### Response body

The proxy expects a single JSON object and decodes it as the `CompactResponse` object described above.

Observed output item shape:

```json
{
  "type": "compaction",
  "id": "cmp_123",
  "encrypted_content": "enc"
}
```

## WSS /codex/responses

Create or continue a response stream over websocket.

### URL

```http
WSS wss://chatgpt.com/backend-api/codex/responses
```

The proxy derives this by replacing:

- `https://` -> `wss://`
- `http://` -> `ws://`

and appending `/codex/responses`.

### Headers

The websocket handshake uses the same auth and identity headers as the HTTP path, including:

- `Authorization`
- `ChatGPT-Account-Id`
- `originator`
- `x-openai-internal-codex-residency`
- `x-client-request-id`
- `x-codex-turn-state`
- `OpenAI-Beta`

### Initial message

The proxy sends one JSON message immediately after connecting:

```json
{
  "type": "response.create",
  "model": "gpt-5.4",
  "input": [],
  "instructions": "Be concise.",
  "tools": [],
  "tool_choice": {},
  "text": {
    "format": {
      "type": "json_object"
    }
  },
  "reasoning": {
    "effort": "medium",
    "summary": "auto"
  },
  "previous_response_id": "resp_previous",
  "prompt_cache_key": "01234567-89ab-cdef-0123-456789abcdef",
  "include": [
    "reasoning.encrypted_content"
  ]
}
```

Observed fields:

- `type: "response.create"`
- all request fields from the HTTP `Request` object except `stream` and `store`

### Websocket response messages

The proxy expects each websocket message to be a single JSON object with a `type` field.

It parses the same event types documented for the HTTP SSE path.

There is no practical `curl` command for this route because the continuation path is a WebSocket endpoint rather than a normal HTTP request.

## GET /codex/usage

Fetch account usage and quota information.

### URL

```http
GET https://chatgpt.com/backend-api/codex/usage
```

### Headers

Typical request headers:

```http
Authorization: Bearer <access_token>
ChatGPT-Account-Id: <account_id>
originator: Codex Desktop
x-openai-internal-codex-residency: us
x-client-request-id: req_<...>
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
Accept: application/json
Accept-Encoding: gzip, deflate
```

### Response body

Observed response shape:

```json
{
  "plan_type": "plus",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 25,
      "limit_window_seconds": 3600,
      "reset_after_seconds": 1800,
      "reset_at": 1730000000
    },
    "secondary_window": {
      "used_percent": 10,
      "limit_window_seconds": 604800,
      "reset_after_seconds": 1200,
      "reset_at": 1730001200
    }
  },
  "code_review_rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 5,
      "limit_window_seconds": 86400,
      "reset_after_seconds": 3600,
      "reset_at": 1730003600
    }
  },
  "credits": {
    "has_credits": true,
    "unlimited": false,
    "balance": 19.5,
    "active_limit": "plus"
  }
}
```

Observed fields:

- `plan_type: string`
- `rate_limit.allowed: bool`
- `rate_limit.limit_reached: bool`
- `rate_limit.primary_window`
- `rate_limit.secondary_window`
- `code_review_rate_limit.allowed: bool`
- `code_review_rate_limit.limit_reached: bool`
- `code_review_rate_limit.primary_window`
- `credits.has_credits: bool`
- `credits.unlimited: bool`
- `credits.balance: number`
- `credits.active_limit: string`

Usage window fields:

- `used_percent: number`
- `limit_window_seconds: number`
- `reset_after_seconds: number`
- `reset_at: unix timestamp`

```bash
curl -sS "${CODEX_BASE_URL}/codex/usage" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "ChatGPT-Account-Id: ${ACCOUNT_ID}" \
  -H "Accept: application/json"
```

## GET /codex/models

Model-catalog endpoint used by this repository.

### URL

```http
GET https://chatgpt.com/backend-api/codex/models?client_version=26.409.61251
```

The `client_version` query parameter is currently only attached to `/codex/models`.

### Headers

Typical request headers:

```http
Authorization: Bearer <access_token>
ChatGPT-Account-Id: <account_id>
originator: Codex Desktop
x-openai-internal-codex-residency: us
x-client-request-id: req_<...>
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
Accept: application/json
Accept-Encoding: gzip, deflate
```

### Response body

The proxy now accepts only the Codex-specific top-level shape observed in live testing:

```json
{
  "models": [
    {
      "slug": "gpt-5.4",
      "display_name": "gpt-5.4",
      "description": "Model description",
      "default_reasoning_level": "medium",
      "supported_reasoning_levels": [
        {
          "effort": "low",
          "description": "Fastest responses"
        },
        {
          "effort": "medium",
          "description": "Balanced"
        }
      ]
    }
  ]
}
```

If `/codex/models` returns anything outside that shape, the proxy treats the response as unusable and falls back to its existing cached catalog or bootstrap model list.

### Model entry fields recognized by the proxy

- `slug`
- `id`
- `name`
- `display_name`
- `description`
- `is_default`
- `default_reasoning_effort`
- `default_reasoning_level`
- `supported_reasoning_efforts`
- `supported_reasoning_levels`

### Reasoning effort entry fields recognized

- `reasoning_effort`
- `reasoningEffort`
- `effort`
- `description`

```bash
curl -sS "${CODEX_BASE_URL}/codex/models?client_version=26.409.61251" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "ChatGPT-Account-Id: ${ACCOUNT_ID}" \
  -H "Accept: application/json"
```

## Response Headers Used by the Proxy

## x-codex-turn-state

The proxy reads this header from response streams and stores it for future continuations.

Observed usage:

- read from HTTP stream response headers
- reused as `x-codex-turn-state` on future requests

## Quota headers

The proxy parses response-header quota snapshots from these headers:

### Primary window

```http
X-Codex-Primary-Used-Percent: 82.5
X-Codex-Primary-Window-Minutes: 300
X-Codex-Primary-Reset-At: 4102444800
```

### Secondary window

```http
X-Codex-Secondary-Used-Percent: 12
X-Codex-Secondary-Window-Minutes: 10080
X-Codex-Secondary-Reset-At: 4103049600
```

### Credits

```http
X-Codex-Credits-Has-Credits: true
X-Codex-Credits-Unlimited: false
X-Codex-Credits-Balance: 19.5
X-Codex-Active-Limit: plus
```

Implementation note:

- Credits-only headers do not produce a quota snapshot unless at least a primary or secondary rate-limit header is present.

## Error Semantics

The proxy classifies these upstream statuses specially:

- `401` -> upstream unauthorized / account marked expired
- `403` -> account banned unless the body looks like HTML/Cloudflare
- `402` -> quota exhausted / cooldown applied
- `429` -> rate limited / cooldown applied

The proxy also extracts retry timing from:

- `Retry-After` header
- JSON fields such as:
  - `resets_in_seconds`
  - `retry_after`
  - `resets_at`

## Auth and Device Login Endpoints

These are not part of the private Codex backend itself, but they are required to obtain and refresh the tokens used against it.

## POST /api/accounts/deviceauth/usercode

Start a device-login flow.

### URL

```http
POST https://auth.openai.com/api/accounts/deviceauth/usercode
```

### Headers

```http
Content-Type: application/json
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
```

### Request body

```json
{
  "client_id": "app_EMoamEEZ73f0CkXaXp7hrann"
}
```

### Response body

Observed shape:

```json
{
  "user_code": "ABCD-EFGH",
  "device_auth_id": "dev_123",
  "interval": 15
}
```

Notes:

- `interval` may be either a JSON number or JSON string

```bash
curl -sS -X POST "${AUTH_BASE_URL}/api/accounts/deviceauth/usercode" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id": "app_EMoamEEZ73f0CkXaXp7hrann"
  }'
```

## POST /api/accounts/deviceauth/token

Poll device-login status.

### URL

```http
POST https://auth.openai.com/api/accounts/deviceauth/token
```

### Headers

```http
Content-Type: application/json
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
```

### Request body

```json
{
  "device_auth_id": "dev_123",
  "user_code": "ABCD-EFGH"
}
```

### Successful response body

Observed shape:

```json
{
  "authorization_code": "auth_code_123",
  "code_verifier": "verifier_123"
}
```

### Pending behavior

The proxy treats these statuses as "authorization still pending":

- `403`
- `404`

```bash
curl -sS -X POST "${AUTH_BASE_URL}/api/accounts/deviceauth/token" \
  -H "Content-Type: application/json" \
  -d '{
    "device_auth_id": "dev_123",
    "user_code": "ABCD-EFGH"
  }'
```

## POST /oauth/token

Used for both initial authorization-code exchange and refresh-token exchange.

### URL

```http
POST https://auth.openai.com/oauth/token
```

### Headers

```http
Content-Type: application/x-www-form-urlencoded
User-Agent: Codex Desktop/26.409.61251 (win32; x64)
```

### Authorization-code exchange request

```http
grant_type=authorization_code
code=<authorization_code>
redirect_uri=https://auth.openai.com/deviceauth/callback
client_id=app_EMoamEEZ73f0CkXaXp7hrann
code_verifier=<code_verifier>
```

### Refresh-token request

```http
grant_type=refresh_token
refresh_token=<refresh_token>
client_id=app_EMoamEEZ73f0CkXaXp7hrann
```

### Response body

Observed shape:

```json
{
  "access_token": "<jwt>",
  "refresh_token": "<opaque or jwt>",
  "id_token": "<jwt>",
  "expires_in": 3600
}
```

Notes:

- `expires_in` may be numeric or string-like
- The proxy extracts `chatgpt_account_id` from either:
  - `id_token`
  - `access_token`

Authorization-code exchange:

```bash
curl -sS -X POST "${AUTH_BASE_URL}/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=authorization_code" \
  --data-urlencode "code=<authorization_code>" \
  --data-urlencode "redirect_uri=https://auth.openai.com/deviceauth/callback" \
  --data-urlencode "client_id=app_EMoamEEZ73f0CkXaXp7hrann" \
  --data-urlencode "code_verifier=<code_verifier>"
```

Refresh-token exchange:

```bash
curl -sS -X POST "${AUTH_BASE_URL}/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=refresh_token" \
  --data-urlencode "refresh_token=<refresh_token>" \
  --data-urlencode "client_id=app_EMoamEEZ73f0CkXaXp7hrann"
```

Recognized JWT claim locations:

- top-level `chatgpt_account_id`
- nested `https://api.openai.com/auth.chatgpt_account_id`

## Observed JWT Claims

These claims are read from tokens by the proxy:

### Account metadata claims

- `email`
- `chatgpt_plan_type`
- `chatgpt_user_id`
- `https://api.openai.com/profile.email`
- `https://api.openai.com/profile.chatgpt_user_id`
- `https://api.openai.com/auth.chatgpt_plan_type`
- `https://api.openai.com/auth.chatgpt_user_id`

### Account-ID claims

- `chatgpt_account_id`
- `https://api.openai.com/auth.chatgpt_account_id`

## Implementation Caveats

- This document is intentionally implementation-driven, not protocol-authoritative.
- Any field not mentioned here may still exist upstream; it is simply not used by this proxy today.
- The proxy only documents shapes it either sends or parses.
- The proxy does not maintain a full schema for every upstream event subtype; some event objects are treated as partially typed JSON.
- The websocket path is currently used for continuation requests because it preserves `previous_response_id`.

## Quick Reference

### Upstream write paths

- `POST /codex/responses`
- `POST /codex/responses/compact`
- `WSS /codex/responses`
- `POST /api/accounts/deviceauth/usercode`
- `POST /api/accounts/deviceauth/token`
- `POST /oauth/token`

### Upstream read paths

- `GET /codex/usage`
- `GET /codex/models`

### Stream success terminal

- `response.completed`

### Stream failure terminals

- `response.failed`
- `error`

### Internal quota signal

- `codex.rate_limits`
