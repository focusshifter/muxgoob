# OrcaRouter completion adapter

`NewClient(apiKey, WithBaseURL(apiRoot), WithHTTPClient(client))` implements the
existing `CreateChatCompletion` interface. No config, key lookup, registry,
publishing, or deployment occurs in this package. Only its supplied key is used.

## Routing contract

- Without terminal `:online`: unchanged JSON Chat Completions request. Native IDs
  `orcarouter/auto`, `orcarouter/free`, and all other IDs remain opaque.
- Anthropic `anthropic/claude-*`: Chat + `web_search_options` medium.
- Google `google/gemini-*`: Chat + reserved native `googleSearch` function marker.
- Grok `grok/grok-*`, `x-ai/grok-*`, and explicit `openai/*`: Responses + built-in
  `web_search`, flat host function tools, `stream:false`, `store:false`.
- `deepseek/*`, `qwen/*`, `moonshot/*`, `kimi/*`, `minimax/*`: Chat with the
  adapter-private `orcarouter_webSearch` function. It calls only the pinned
  `branddev.web-search` paid catalog tool using the same Orca key.
- Other online families, `orcarouter/auto:online`, and `online:auto`: explicit
  unsupported error before network. No model aliases (in particular no
  `x-ai/grok-4.6` -> `grok/grok-4.6` repair), no offline or paid-provider fallback.

Online exposes the relevant search capability; it does not guarantee a model
will use it on every request. The live smoke requires actual search-call
provenance, not merely plausible answer freshness.

## Generic paid search boundary

The API's example `exa.search` was not a real catalog ID in authenticated
inspection. `branddev.web-search` was inspected and a real run verified by the
parent. Its schema requires `{query:string,numResults:10}` and returns
`output.results[]` with title/URL/description. Only that exact tool ID is run.
No dynamic arbitrary catalog selection, provider base URL calls, page fetches,
account metadata propagation, host tool execution, or retry of paid POSTs.

Before each generic completion, GET `/tools/branddev.web-search` must confirm
healthy, for sale, per-call pricing <= $0.003 and the expected input schema.
At most two paid searches occur per `CreateChatCompletion` invocation, each
with ten upstream results, reduced locally to at most five HTTP(S) sources,
240-character titles, 1000-character snippets and 2048-byte URLs. Queries have
at most 500 characters. The published tools contract allows settlement up to
1.25 times quote: at this configured ceiling, at most $0.0075 in standalone
search charges per invocation, excluding model tokens. This is **not** a
conversation-wide or account spending quota. Host-loop re-entry gets a new
invocation budget; configure key quotas for a hard account/conversation policy.

A 120-second overall completion deadline bounds all internal requests. Inspect
has a 15-second subdeadline; paid search has 30 seconds. HTTP responses are
bounded to 4 MiB. Redirects are refused even for injected clients. Errors omit
upstream bodies, URLs, private prompts, keys and data URLs. No automatic retries.

Pure search calls are executed internally and followed with normal tool results.
Mixed search + host calls execute only search, then bubble only host calls and
retain normalized search evidence as an explicitly untrusted assistant-context
block. The outer loop can execute its tools normally without orphan internal
calls. This retains evidence, not a lossless copy of internal search messages.
Reserved function-name collisions fail explicitly. An error/exhausted budget
fails the request rather than returning an ungrounded fallback answer.

Responses requests resend full text/image/tool history statelessly, including
image-only messages, multiple function calls and call-ID-matched results.
Built-in search/reasoning items are not returned as host function calls or as
server-stored references. Incomplete function calls are never executable.
Unsupported multipart types and legacy function history fail explicitly instead
of silently dropping content. Responses `tool_choice` is flattened correctly.

## Verification

```
/usr/local/go/bin/go test -race -count=1 ./internal/orcarouter
/usr/local/go/bin/go vet ./internal/orcarouter
```

The initial TDD RED run failed with undefined Client/NewClient/option exports;
fixture and httptest coverage then went GREEN. Default tests do not use the
network, environment key or production config. An external-package test runs
the actual `tools.RunLoop` with two host calls and a stateless continuation.

Opt-in, paid, unpublished live tests (credentials supplied via environment only):

```
ORCAROUTER_ONLINE_LIVE=1 ORCAROUTER_TEST_MODEL=grok/grok-4.6:online \
  /usr/local/go/bin/go test -count=1 -run '^TestOnlineLiveGrok' -v ./internal/orcarouter

ORCAROUTER_GENERIC_ONLINE_LIVE=1 \
ORCAROUTER_GENERIC_TEST_MODEL=deepseek/deepseek-chat:online \
  /usr/local/go/bin/go test -count=1 -run '^TestOnlineLiveGeneric' -v ./internal/orcarouter
```

Both additionally require `ORCAROUTER_API_KEY`; use an exact live-catalog model
ID and override the generic default if absent. Each smoke requires completed
native `web_search_call` or standalone run evidence, then a harmless constant
host function followed by a full-history continuation consuming its result.
Audit output retains IDs, tool counts and search billing, never keys or bodies.
No smoke was run by this subagent; the parent owns explicit real execution.

## References investigated

Official docs (fetched directly as Markdown):
- https://docs.orcarouter.ai/advanced/web-search.md
- https://docs.orcarouter.ai/api-reference/responses/create-a-response.md
- https://docs.orcarouter.ai/agent-infra/tools/overview.md
- https://docs.orcarouter.ai/agent-infra/tools/quickstart.md
- https://docs.orcarouter.ai/agent-infra/tools/run.md

The authenticated schema/run evidence supplied by the parent supersedes
placeholder catalog examples in the docs. Native search is provider-specific;
standalone tools provide generic grounding, not a hidden model-agnostic native
search option on DeepSeek.
