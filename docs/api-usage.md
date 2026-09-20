# API usage

[README](../README.md) · [Deployment](deployment.md) · [Configuration reference](../config.example.yaml)

First [deploy the gateway and add a provider credential](deployment.md). Requests use a **client API key** from the gateway's `api-keys` list, separate from the console password, management key, and upstream provider credentials.

## Connect and discover models

These examples use a POSIX shell and curl. Set the gateway origin without a trailing slash or `/v1` suffix:

```sh
export PROXY_URL='http://127.0.0.1:8317'
export PROXY_API_KEY='REPLACE_WITH_CLIENT_API_KEY'

curl --fail-with-body "$PROXY_URL/v1/models" \
  -H "Authorization: Bearer $PROXY_API_KEY"
```

For the console deployment, use your HTTPS origin, such as `https://gateway.example.com`. Copy a model `id` from the response; available IDs depend on configured credentials, aliases, and exclusions. A listed model does not guarantee its upstream account currently has quota.

```sh
export PROXY_MODEL='REPLACE_WITH_MODEL_ID'
```

For a compatible client or SDK, use:

| Setting | Value |
| --- | --- |
| OpenAI-compatible base URL | `http://127.0.0.1:8317/v1` or `https://gateway.example.com/v1` |
| API key | Your gateway client API key |
| Model | An ID from `/v1/models` |

Clients that append `/v1` themselves need the origin instead. Check the final request path if you get a 404.

## Chat completions

```sh
curl --fail-with-body "$PROXY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $PROXY_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{
    \"model\": \"$PROXY_MODEL\",
    \"messages\": [{\"role\": \"user\", \"content\": \"Say hello in one sentence.\"}]
  }"
```

The generated text is in `choices[0].message.content` for a standard text response.

For streaming, set `stream: true` and use curl's `--no-buffer` option:

```sh
curl --fail-with-body --no-buffer "$PROXY_URL/v1/chat/completions" \
  -H "Authorization: Bearer $PROXY_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{
    \"model\": \"$PROXY_MODEL\",
    \"messages\": [{\"role\": \"user\", \"content\": \"Say hello in one sentence.\"}],
    \"stream\": true
  }"
```

The response uses server-sent events (SSE). Chat text arrives in `choices[0].delta.content`; `[DONE]` ends the stream. An error can arrive after HTTP headers have been sent, so streaming clients should inspect events as well as the initial status code.

## Responses

```sh
curl --fail-with-body "$PROXY_URL/v1/responses" \
  -H "Authorization: Bearer $PROXY_API_KEY" \
  -H 'Content-Type: application/json' \
  -d "{
    \"model\": \"$PROXY_MODEL\",
    \"input\": \"Say hello in one sentence.\"
  }"
```

Read text from message content entries in the response's `output` array. Add `"stream": true` and `--no-buffer` for Responses SSE events, which use a different event schema from Chat Completions.

## Claude and Gemini formats

The gateway also accepts these request formats. Use a model ID available for your configured provider; protocol compatibility does not imply support for every provider feature.

Claude Messages accepts the same client key in `x-api-key`:

```sh
curl --fail-with-body "$PROXY_URL/v1/messages" \
  -H "x-api-key: $PROXY_API_KEY" \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d "{
    \"model\": \"$PROXY_MODEL\",
    \"max_tokens\": 256,
    \"messages\": [{\"role\": \"user\", \"content\": \"Say hello.\"}]
  }"
```

For Gemini, discover names through `GET /v1beta/models` using `x-goog-api-key`. Set `GEMINI_MODEL` to the returned model name without its leading `models/`:

```sh
curl --fail-with-body "$PROXY_URL/v1beta/models" \
  -H "x-goog-api-key: $PROXY_API_KEY"

export GEMINI_MODEL='REPLACE_WITH_GEMINI_MODEL_ID'
curl --fail-with-body "$PROXY_URL/v1beta/models/$GEMINI_MODEL:generateContent" \
  -H "x-goog-api-key: $PROXY_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Say hello."}]}]}'
```

For Gemini streaming, use `:streamGenerateContent?alt=sse` and `--no-buffer`.

## Common routes

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/v1/models` | Discover client-visible model IDs |
| POST | `/v1/chat/completions` | Chat, with optional SSE streaming |
| POST | `/v1/responses` | Responses, with optional SSE streaming |
| GET | `/v1/responses` | Responses WebSocket upgrade |
| POST | `/v1/messages` | Claude Messages |
| POST | `/v1/messages/count_tokens` | Claude token counting |
| GET | `/v1beta/models` | Gemini model discovery |
| POST | `/v1beta/models/{model}:generateContent` | Gemini generation |
| POST | `/v1beta/models/{model}:streamGenerateContent` | Gemini streaming |
| POST | `/v1beta/interactions` | Gemini Interactions |

These are the main text API routes, not an exhaustive provider feature matrix. Tools, image input, reasoning controls, and other capabilities depend on the selected model and upstream. For realtime usage, see the [Go realtime example](../examples/realtime-openai-go/README.md). For embedding the gateway itself, see [Go SDK usage](sdk-usage.md).

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `401`, missing or invalid API key | Send a gateway client key, and confirm your reverse proxy preserves the authentication header. |
| Empty model list | Add a provider credential; inspect disabled accounts, model mappings, and exclusions. Creating a client key alone does not add models. |
| `model_not_found` or unknown provider for model | Copy an exact ID from model discovery, including any configured prefix or alias. |
| `auth_unavailable` or upstream quota error | Check the account's credentials, quota, and cooldown state in the console or server logs. |
| `404` on inference | Check the base URL and avoid a doubled `/v1/v1`. Forward inference paths to Go. |
| `404` on `/v0/management` through the console gateway | Expected: public management routes are blocked. Sign in through the console. |
| Streaming arrives all at once | Disable buffering in your client and reverse proxy; use curl `--no-buffer` to inspect SSE. |

Start with model discovery, then a short inference request. The Go-only `/healthz` endpoint checks process health, not credentials or provider availability; the console stack does not route that path to Go publicly.
